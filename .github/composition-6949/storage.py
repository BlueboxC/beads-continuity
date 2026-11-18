"""One owned, capacity-bounded ext4 image; no scans of a changing build tree."""
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import threading
import time

MIB = 1024**2
GIB = 1024**3


class OwnedStorage:
    def __init__(self, runner_temp, budget_bytes, source_bytes, artifact_cap):
        assert sys.platform.startswith('linux') and os.environ.get('GITHUB_ACTIONS') == 'true'
        assert os.getuid() != 0
        self.capacity = ((budget_bytes-source_bytes-artifact_cap-64*MIB)//MIB)*MIB
        assert 5*GIB <= self.capacity < budget_bytes
        self.root = Path(tempfile.mkdtemp(prefix='composition-storage-', dir=runner_temp)).resolve()
        self.identity = (self.root.stat().st_dev, self.root.stat().st_ino)
        self.image = self.root/'owned.ext4'
        self.mount = self.root/'fs'
        self.mounted = False
        self.mount_identity = None
        self.image_identity = None
        self.device = None
        try:
            self.mount.mkdir()
            with self.image.open('xb') as stream:
                stream.truncate(self.capacity)
            self.image_identity = (self.image.stat().st_dev, self.image.stat().st_ino)
            subprocess.run(['/usr/sbin/mkfs.ext4', '-q', '-F', '-m', '0', str(self.image)], check=True, timeout=90)
            assert self.image.stat().st_size == self.capacity
            subprocess.run(['sudo', '-n', 'mount', '-o', 'loop,nosuid,nodev,noatime', str(self.image), str(self.mount)], check=True, timeout=30)
            self.mounted = True
            subprocess.run(['sudo', '-n', 'chown', str(os.getuid())+':'+str(os.getgid()), str(self.mount)], check=True, timeout=10)
            self.mount_identity = (self.mount.stat().st_dev, self.mount.stat().st_ino)
            self.validate_mount()
            self.private_tmp = self.mount/'private-tmp'
            self.modules = self.mount/'modules'
            self.private_tmp.mkdir(mode=0o700)
            self.modules.mkdir(mode=0o700)
            self.root_ids = {}
        except BaseException:
            self.close()
            raise

    def validate_mount(self):
        assert (self.root.stat().st_dev,self.root.stat().st_ino) == self.identity and not self.root.is_symlink()
        assert (self.image.stat().st_dev,self.image.stat().st_ino) == self.image_identity and self.image.stat().st_size == self.capacity and not self.image.is_symlink()
        assert (self.mount.stat().st_dev,self.mount.stat().st_ino) == self.mount_identity and os.path.ismount(self.mount)
        entries=[]
        for line in Path('/proc/self/mountinfo').read_text().splitlines():
            left,right=line.split(' - ',1)
            if left.split()[4] == str(self.mount):entries.append((right.split(),set(left.split()[5].split(','))))
        assert len(entries)==1 and entries[0][0][0]=='ext4' and re.fullmatch(r'/dev/loop\d+',entries[0][0][1])
        assert {'nosuid','nodev','noatime'} <= entries[0][1]
        device=entries[0][0][1]
        if self.device is not None:assert device == self.device
        self.device=device
        backing=Path('/sys/block/'+Path(device).name+'/loop/backing_file').read_text().strip()
        assert Path('/'+backing.lstrip('/')).resolve() == self.image

    def used_bytes(self):
        self.validate_mount()
        status=os.statvfs(self.mount)
        assert 0<=status.f_bfree<=status.f_blocks and status.f_frsize>0
        assert status.f_blocks*status.f_frsize<=self.capacity
        return self.capacity-status.f_bfree*status.f_frsize

    def measure_roots(self, roots):
        for root in roots:
            root=Path(root).resolve()
            assert root.is_relative_to(self.mount) and root.stat().st_dev==self.mount_identity[0] and not root.is_symlink()
            identity=(root.stat().st_dev,root.stat().st_ino)
            if root in self.root_ids:assert self.root_ids[root]==identity
            self.root_ids[root]=identity
        return self.used_bytes()

    def probe(self):
        before=self.used_bytes()
        file=self.mount/'counter-proof'
        with file.open('xb') as stream:
            stream.write(b'x'*MIB)
            stream.flush()
            os.fsync(stream.fileno())
        after=self.used_bytes()
        assert after>=before+MIB
        file.unlink()
        churn=self.mount/'churn-proof'
        churn.mkdir()
        failures=[]
        def mutate():
            try:
                for index in range(100):
                    path=churn/str(index)
                    path.write_bytes(b'x'*4096)
                    time.sleep(.002)
                    path.unlink()
            except BaseException as error:
                failures.append(str(error))
        thread=threading.Thread(target=mutate)
        thread.start()
        samples=[]
        try:
            for _ in range(20):
                samples.append(self.used_bytes())
                time.sleep(.002)
        finally:
            thread.join(timeout=5)
        assert not thread.is_alive() and not failures and all(0<=sample<=self.capacity for sample in samples)
        churn.rmdir()
        return {'image':str(self.image),'mount':str(self.mount),'device':self.device,'capacity_bytes':self.capacity,'used_before_probe':before,'used_after_fsync':after,'write_accounted':True,'stable_counter_reads_during_mutation':len(samples),'mount_identity':self.mount_identity,'root_identity':self.identity,'backing_identity':self.image_identity}

    def close(self):
        if os.path.ismount(self.mount):
            if self.mount_identity is None:
                self.mount_identity=(self.mount.stat().st_dev,self.mount.stat().st_ino)
            self.validate_mount()
            subprocess.run(['sudo','-n','umount',str(self.mount)],check=True,timeout=30)
            self.mounted=False
            assert not os.path.ismount(self.mount)
        assert (self.root.stat().st_dev,self.root.stat().st_ino)==self.identity and not self.root.is_symlink()
        if self.image_identity is not None:
            assert (self.image.stat().st_dev,self.image.stat().st_ino)==self.image_identity and not self.image.is_symlink()
            def associated():
                result=subprocess.run(['sudo','-n','losetup','--list','--associated',str(self.image),'--noheadings','--output','NAME'],capture_output=True,text=True,check=True,timeout=10)
                devices=result.stdout.split()
                assert len(devices)<=4 and all(re.fullmatch(r'/dev/loop\d+',device) for device in devices)
                return devices
            for device in associated():
                backing=Path('/sys/block/'+Path(device).name+'/loop/backing_file').read_text().strip()
                assert Path('/'+backing.lstrip('/')).resolve()==self.image
                subprocess.run(['sudo','-n','losetup','--detach',device],check=True,timeout=10)
            assert not associated()
        shutil.rmtree(self.root)
        assert not self.root.exists()
