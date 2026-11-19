"""One focused, bounded fork-CI composition. No source installation or merge."""
import hashlib
import errno
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request

HERE = Path(__file__).resolve().parent
WORKSPACE = HERE.parent.parent
ARTIFACTS = WORKSPACE / 'composition-artifacts'
GIB = 1024**3
ARTIFACT_LIMIT = 64*1024*1024
LOG_LIMIT = 16*1024*1024


def unit_name():
    run = os.environ['GITHUB_RUN_ID']
    attempt = os.environ['GITHUB_RUN_ATTEMPT']
    assert run.isdigit() and attempt.isdigit()
    return f'beads-compose-{run}-{attempt}.service'


def unit_state():
    result = subprocess.run(['sudo', '-n', 'systemctl', 'show', unit_name(), '-p', 'LoadState', '-p', 'ActiveState', '-p', 'ControlGroup', '-p', 'Result', '-p', 'ExecMainStatus'], capture_output=True, text=True, timeout=10, check=False)
    state = dict(line.split('=', 1) for line in result.stdout.splitlines() if '=' in line)
    assert result.returncode == 0 or state.get('LoadState') == 'not-found', 'Cannot inspect owned transient unit'
    return state


def stop_unit():
    state = unit_state()
    if state.get('LoadState') != 'not-found':
        subprocess.run(['sudo', '-n', 'systemctl', 'stop', unit_name()], capture_output=True, timeout=15, check=True)
        if unit_state().get('LoadState') != 'not-found':
            subprocess.run(['sudo', '-n', 'systemctl', 'reset-failed', unit_name()], capture_output=True, timeout=10, check=True)
    final = unit_state()
    assert final.get('LoadState') == 'not-found' or final.get('ActiveState') in ('inactive', 'failed'), final
    if final.get('ControlGroup'):
        path = Path('/sys/fs/cgroup')/final['ControlGroup'].lstrip('/')
        assert path.name == unit_name() and path.resolve().is_relative_to(Path('/sys/fs/cgroup'))
        assert all(not p.read_text().strip() for p in [path/'cgroup.procs', *path.rglob('*/cgroup.procs')] if p.exists())
    previous = json.loads((ARTIFACTS/'teardown.json').read_text())['attempts'] if (ARTIFACTS/'teardown.json').exists() else []
    save('teardown.json', {'attempts':[*previous, {'unit':unit_name(), 'before':state, 'after':final, 'processes_retired':True, 'private_tmp_namespace_retired':True}]})


def owned_cgroup_members(group, inode):
    assert group.resolve().is_relative_to(Path('/sys/fs/cgroup')) and group.name==unit_name()
    assert group.stat().st_ino==inode, 'Owned cgroup identity changed'
    # The root membership is mandatory; a vanished child subgroup is not the root.
    members={int(x) for x in (group/'cgroup.procs').read_text().split()}
    def propagate(error):raise error
    for directory, _, _ in os.walk(group,onerror=propagate,followlinks=False):
        directory=Path(directory)
        if directory==Path(group):continue
        try:members.update(int(x) for x in (directory/'cgroup.procs').read_text().split())
        except FileNotFoundError as error:
            try:directory.stat()
            except FileNotFoundError:continue
            raise error
    assert group.stat().st_ino==inode, 'Owned cgroup identity changed during membership read'
    return members


def read_process_status(pid, group, inode, reader=None):
    try:
        return (reader if reader is not None else lambda:Path(f'/proc/{pid}/status').read_text())()
    except OSError as error:
        if error.errno not in (errno.ENOENT,errno.ESRCH):raise
        if pid in owned_cgroup_members(group,inode):raise
        name=f'proc-retirements-{os.getpid()}.json'
        prior=json.loads((ARTIFACTS/name).read_text()) if (ARTIFACTS/name).exists() else {'count':0,'last':[]}
        save(name,{'observer_pid':os.getpid(),'count':prior['count']+1,'last':[*prior['last'],{'pid':pid,'errno':error.errno,'absent_from_fresh_owned_membership':True}][-16:]})
        return None


def proc_retirement_preflight(group, inode):
    child=subprocess.Popen([sys.executable,'-c','import sys;sys.stdin.buffer.read(1)'],stdin=subprocess.PIPE)
    descriptor=None
    try:
        assert child.pid in owned_cgroup_members(group,inode)
        live=read_process_status(os.getpid(),group,inode)
        assert live is not None and re.search(r'^VmRSS:\s+\d+ kB$',live,re.M), 'Live process RSS unavailable'
        descriptor=Path(f'/proc/{child.pid}/status').open()
        child.stdin.close();assert child.wait(timeout=5)==0
        observed=None
        try:descriptor.read()
        except OSError as error:observed=error.errno
        assert observed==errno.ESRCH, f'Expected native dead-task ESRCH, got {observed}'
        assert read_process_status(child.pid,group,inode,reader=descriptor.read) is None
        refusals=[]
        for number,token in [(errno.EACCES,'permission'),(errno.ESRCH,'still-owned')]:
            def unavailable():raise OSError(number,'preflight injected metric loss')
            refused=False
            try:read_process_status(os.getpid(),group,inode,reader=unavailable)
            except OSError as error:refused=error.errno==number
            assert refused,token
            refusals.append(token)
        save('proc-retirement-preflight.json',{'native_dead_task_errno':observed,'fresh_membership_absent':True,'live_status_read':True,'permissions_refused':True,'still_owned_read_loss_refused':True,'root_membership_required':True,'resource_limits_unchanged':True})
    finally:
        if descriptor is not None:descriptor.close()
        if child.poll() is None:child.kill();child.wait(timeout=5)


def active_cgroup_rss():
    proof = ARTIFACTS / 'cgroup.json'
    if not proof.exists():
        return 0
    data = json.loads(proof.read_text())
    group = Path('/sys/fs/cgroup') / data['cgroup'].lstrip('/')
    assert group.resolve().is_relative_to(Path('/sys/fs/cgroup')) and group.name == unit_name()
    if not group.exists():
        state = unit_state()
        assert state.get('LoadState') == 'not-found' or state.get('ActiveState') in ('inactive', 'failed')
        return 0
    assert group.stat().st_ino == data['inode']
    assert int((group/'memory.max').read_text()) == 3*GIB
    assert int((group/'memory.swap.max').read_text()) == 512*1024*1024
    assert not Path(data['private_tmp_probe']).exists(), 'PrivateTmp did not isolate the test namespace'
    bound_probe=Path(os.environ['COMPOSITION_PRIVATE_TMP'])/Path(data['private_tmp_probe']).name
    assert bound_probe.is_file() and bound_probe.stat().st_dev==int(os.environ['COMPOSITION_STORAGE_DEVICE'])
    save('cgroup-observation.json', {'cgroup':str(group), 'memory_peak':int((group/'memory.peak').read_text()), 'memory_current':int((group/'memory.current').read_text()), 'memory_events':(group/'memory.events').read_text()})
    pids=owned_cgroup_members(group,data['inode'])
    total = 0;processes=[]
    for pid in pids:
        status=read_process_status(pid,group,data['inode'])
        if status is None:continue
        match = re.search(r'^VmRSS:\s+(\d+) kB$', status, re.M)
        if match:
            rss=int(match[1])*1024;total+=rss
            name=re.search(r'^Name:\s+(.+)$',status,re.M)
            processes.append({'pid':pid,'name':name[1] if name else 'unknown','rss_bytes':rss})
    stats=dict(line.split() for line in (group/'memory.stat').read_text().splitlines())
    save('cgroup-memory-last.json',{'rss_bytes':total,'processes':sorted(processes,key=lambda p:p['rss_bytes'],reverse=True),'memory_stat':{k:int(stats[k]) for k in ['anon','file','kernel','slab','file_dirty','file_writeback'] if k in stats}})
    profile=ARTIFACTS/'cgroup-process-profile.json'
    prior=json.loads(profile.read_text()) if profile.exists() else {'peak_rss_bytes':0}
    if total>=prior['peak_rss_bytes']:
        save('cgroup-process-profile.json',{'peak_rss_bytes':total,'processes':sorted(processes,key=lambda p:p['rss_bytes'],reverse=True),'memory_stat':{k:int(stats[k]) for k in ['anon','file','kernel','slab','file_dirty','file_writeback'] if k in stats}})
    (ARTIFACTS/'cgroup-validated').touch()
    return total


def cgroup_rss():
    try:
        return active_cgroup_rss()
    except FileNotFoundError:
        state = unit_state()
        if state.get('LoadState') == 'not-found' or state.get('ActiveState') in ('inactive', 'failed'):
            return 0
        raise


def launch_service():
    keys = ['PATH', 'TMPDIR', 'TMP', 'TEMP', 'GOTMPDIR', 'GOCACHE', 'GOMODCACHE', 'GOLANGCI_LINT_CACHE', 'GOTOOLCHAIN', 'GOMEMLIMIT', 'GOGC', 'GOMAXPROCS', 'GITHUB_SHA', 'GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT', 'RUNNER_TEMP', 'COMPOSITION_PRIVATE_TMP', 'COMPOSITION_STORAGE_DEVICE', 'BD_DISABLE_METRICS', 'BD_DISABLE_EVENT_FLUSH', 'DOLT_DISABLE_EVENT_FLUSH', 'DOLT_METRICS_DISABLED']
    env = [key+'='+os.environ[key] for key in keys if key in os.environ]
    uid, gid = os.getuid(), os.getgid()
    assert uid != 0
    command = ['sudo', '-n', 'systemd-run', '--quiet', '--wait', '--pipe', '--service-type=exec', '--unit='+unit_name(), '--uid='+str(uid), '--gid='+str(gid), '--working-directory='+str(WORKSPACE), '--property=PrivateMounts=yes', '--property=BindPaths='+os.environ['COMPOSITION_PRIVATE_TMP']+':/tmp', '--property=MemoryAccounting=yes', '--property=MemoryMax='+str(3*GIB), '--property=MemorySwapMax='+str(512*1024*1024), '--property=CPUQuota=100%', '--property=RuntimeMaxSec=5400', '--property=TimeoutStopSec=5', '--property=KillMode=control-group', '--property=OOMPolicy=kill', '/usr/bin/env', *env, 'COMPOSITION_UID='+str(uid), 'COMPOSITION_GID='+str(gid), sys.executable, str(Path(__file__).resolve()), '--worker']
    return subprocess.run(command, check=False).returncode


def bounded_command(command, log, timeout, stderr_log=None, log_limit=LOG_LIMIT):
    process = subprocess.Popen(command, cwd=WORKSPACE, stdout=subprocess.PIPE, stderr=subprocess.PIPE if stderr_log else subprocess.STDOUT)
    states=[];threads=[];capture_lock=threading.Lock()
    def drain(pipe, destination, state):
        try:
            with destination.open('wb') as output:
                while True:
                    chunk=pipe.read1(65536)
                    if not chunk:break
                    with capture_lock:
                        # Leave room for controller/exit receipts even on failure.
                        remaining=max(0,min(log_limit-state['bytes'],ARTIFACT_LIMIT-1024*1024-artifact_size()))
                        output.write(chunk[:remaining]);output.flush()
                        state['bytes']+=min(len(chunk),remaining)
                    if len(chunk)>remaining:
                        state['truncated']=True;process.terminate()
        except Exception as error:
            state['error']=str(error);process.terminate()
    streams=[(process.stdout,log)]
    if stderr_log:streams.append((process.stderr,stderr_log))
    for pipe,destination in streams:
        state={'bytes':0,'truncated':False,'error':None};states.append(state)
        thread=threading.Thread(target=drain,args=(pipe,destination,state),daemon=True);threads.append(thread);thread.start()
    try:
        code=process.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        process.kill();process.wait(timeout=10);raise
    finally:
        for thread in threads:thread.join(timeout=10)
        if any(thread.is_alive() for thread in threads):raise RuntimeError('Log capture did not finish')
    assert not any(x['truncated'] for x in states),'Capture limit reached; phase fails and bounded diagnostics are retained'
    assert all(x['error'] is None for x in states),'Log capture failed: '+str([x['error'] for x in states if x['error']])
    return code


def save(name, value):
    ARTIFACTS.mkdir(exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix='.receipt-', dir=ARTIFACTS)
    try:
        with os.fdopen(descriptor, 'w') as stream:
            stream.write(json.dumps(value, indent=2) + '\n')
        os.replace(temporary, ARTIFACTS/name)
    finally:
        if Path(temporary).exists():
            Path(temporary).unlink()


def artifact_size():
    total = 0
    for path in ARTIFACTS.iterdir():
        if path.name.startswith('.receipt-'):
            continue
        try:
            total += path.stat().st_size if path.is_file() else 0
        except FileNotFoundError:
            # Atomic replacement of an owned receipt may race this inventory.
            continue
    return total


def assert_selected(log, expected):
    # Go -test.v=test2json prefixes framing lines with exactly one SYN byte.
    # Normalize only that documented marker on a copy; retain the raw log.
    text, framed = re.subn(r'(?m)^\x16', '', log.read_text())
    passed = set(re.findall(r'^\s*--- PASS: (\S+)', text, re.M))
    skipped = set(re.findall(r'^\s*--- SKIP: (\S+)', text, re.M))
    failed = set(re.findall(r'^\s*--- FAIL: (\S+)', text, re.M))
    missing = set(expected) - passed
    relevant_skips = {n for n in skipped if n.split('/')[0] in expected}
    if missing or relevant_skips or failed:
        raise RuntimeError(f'Nonvacuous gate failed: missing={sorted(missing)}, skipped={sorted(relevant_skips)}, failed={sorted(failed)}')
    return {'required_roots': expected, 'passed': sorted(passed), 'skipped': sorted(skipped), 'failed': sorted(failed), 'test2json_frame_markers':framed}


def link_arguments(action):
    return [*action.get('arguments',[]), *[arg for entry in action.get('paramFiles',[]) for arg in entry.get('arguments',[])]]


def verify_link_options(actions):
    assert actions, 'No GoLink action'
    proof=[]
    for action in actions:
        args=link_arguments(action)
        assert {'-s','-w','-race'}<=set(args), 'Missing original upstream GoLink flags'
        assert not any(arg.startswith(('-w=','-s=','-race=')) for arg in args), 'Conflicting GoLink flag override'
        proof.append(sorted({'-s','-w','-race'} & set(args)))
    return proof


def verify_action_environments(graph):
    owners={str(t['id']):t['label'].removeprefix('@@') for t in graph.get('targets',[])}
    root=[];unchanged=[]
    for action in graph.get('actions',[]):
        owner=owners[str(action['targetId'])]
        assert owner in ('//cmd/bd:bd_composition_test','//cmd/bd/doctor:doctor_test','//internal/ceiling:ceiling'),owner
        (unchanged if owner=='//internal/ceiling:ceiling' else root).append(action)
    expected={'GOMEMLIMIT':'384MiB','GOGC':'20','GOMAXPROCS':'1'}
    for label in ['//cmd/bd:bd_composition_test','//cmd/bd/doctor:doctor_test']:
        assert {'GoCompilePkg','RunNogo','GoLink'}<={a['mnemonic'] for a in root if owners[str(a['targetId'])]==label}
    assert any(a['mnemonic']=='GoCompilePkg' for a in unchanged)
    for actions,controlled in [(root,True),(unchanged,False)]:
        for action in actions:
            actual={v['key']:v.get('value','') for v in action.get('environmentVariables',[])}
            assert (all(actual.get(k)==v for k,v in expected.items()) if controlled else all(k not in actual for k in expected)),action['mnemonic']
    return root,unchanged,expected


def worker(manifest):
    worker_deadline=time.monotonic()+5350  # inside the existing5400s service limit
    assert os.getuid() == int(os.environ['COMPOSITION_UID']) and os.getgid() == int(os.environ['COMPOSITION_GID']) and os.getuid() != 0
    cgroup = Path('/proc/self/cgroup').read_text().strip().split('0::')[-1]
    group = Path('/sys/fs/cgroup')/cgroup.lstrip('/')
    assert group.resolve().is_relative_to(Path('/sys/fs/cgroup')) and group.name == unit_name()
    assert int((group/'memory.max').read_text()) == 3*GIB
    assert int((group/'memory.swap.max').read_text()) == 512*1024*1024
    quota, period = (group/'cpu.max').read_text().split()
    assert quota.isdigit() and int(quota) <= int(period)
    descriptor, probe_name = tempfile.mkstemp(prefix='composition-owned-', dir='/tmp')
    os.close(descriptor)
    private_probe = Path(probe_name)
    assert private_probe.stat().st_dev==int(os.environ['COMPOSITION_STORAGE_DEVICE'])
    detached = subprocess.check_output([sys.executable, '-c', "import os;from pathlib import Path;os.setsid();print(Path('/proc/self/cgroup').read_text().strip())"], text=True)
    assert detached.strip() == '0::'+cgroup
    save('cgroup.json', {'unit':unit_name(), 'cgroup':cgroup, 'inode':group.stat().st_ino, 'memory_max':3*GIB, 'swap_max':512*1024*1024, 'uid':os.getuid(), 'gid':os.getgid(), 'detached_child_in_same_cgroup':True, 'private_tmp_probe':str(private_probe), 'private_tmp_on_bounded_volume':True})
    deadline = time.monotonic()+15
    while not (ARTIFACTS/'cgroup-validated').exists():
        assert time.monotonic()<deadline, 'Controller did not validate cgroup and PrivateTmp'
        time.sleep(.1)
    subprocess.run([sys.executable, str(HERE/'test_budget_port.py')], cwd=WORKSPACE, check=True, timeout=10)
    proc_retirement_preflight(group,group.stat().st_ino)
    scratch = Path(os.environ['TMPDIR'])
    binary = scratch / 'bazel-9.3.1'
    assert (WORKSPACE / '.bazelversion').read_text().strip() == '9.3.1'
    expected = '7f775b54bbec487efaf0f49ccda1d2ed02cd34f582edfdd6aeeec76e39ef585c'
    url = 'https://github.com/bazelbuild/bazel/releases/download/9.3.1/bazel-9.3.1-linux-x86_64'
    with urllib.request.urlopen(url, timeout=120) as stream, binary.open('wb') as out:
        shutil.copyfileobj(stream, out, 1024*1024)
    assert hashlib.sha256(binary.read_bytes()).hexdigest() == expected
    binary.chmod(0o755)
    startup = [str(binary), '--batch', '--nosystem_rc', '--nohome_rc', '--output_user_root='+str(scratch/'bazel'), '--host_jvm_args=-Xmx512m']
    limits = ['--config=fork-cache', '--remote_executor=', '--noremote_upload_local_results', '--remote_max_connections=1', '--nocache_test_results', '--discard_analysis_cache', '--nokeep_state_after_build', '--notrack_incremental_state', '--jobs=1', '--local_resources=memory=1024', '--local_resources=cpu=1', '--local_test_jobs=1', '--loading_phase_threads=1', '--repository_cache=', '--test_output=all', '--test_arg=-test.parallel=1', '--action_env=GOMEMLIMIT=384MiB', '--action_env=GOGC=20', '--action_env=GOMAXPROCS=1', '--test_env=GOMEMLIMIT=384MiB', '--test_env=GOGC=20', '--test_env=GOMAXPROCS=1']
    # rules_go supplies an explicit action env; --action_env alone does not
    # constrain its compiler. Verify the actual patched action before compiling.
    query = 'mnemonic("(GoCompilePkg|RunNogo|GoLink)", set(//cmd/bd:bd_composition_test //cmd/bd/doctor:doctor_test //internal/ceiling:ceiling))'
    environment_log = ARTIFACTS/'go-action-environment.log'
    query_args = ['aquery', query, '--@rules_go//go/config:race', '--output=jsonproto', '--include_artifacts=false', '--include_commandline=true', '--include_param_files=true', '--repository_cache=', '--noshow_progress', '--ui_event_filters=-info,-warning,-debug', '--jobs=1', '--loading_phase_threads=1']
    query_code = bounded_command(startup+query_args, environment_log, 600, stderr_log=ARTIFACTS/'go-action-environment-stderr.log', log_limit=2*1024*1024)
    assert query_code==0, 'Go action environment preflight failed'
    query_text=environment_log.read_text()
    action_graph=json.loads(query_text)
    root_actions,unchanged_actions,expected_env=verify_action_environments(action_graph)
    links=[a for a in root_actions if a['mnemonic']=='GoLink']
    link_proofs=verify_link_options(links)
    save('go-action-environment.json', {'expected':expected_env,'observed_actions':[{'mnemonic':a['mnemonic'],'budget_environment':{v['key']:v.get('value','') for v in a.get('environmentVariables',[]) if v['key'] in expected_env}} for a in root_actions],'precompile_verified':True,'race_enabled':True,'baseline_go_link_flags_verified':True,'go_link_race_verified':True,'actual_go_link_flags':link_proofs,'unchanged_dependency_default_env_verified':True,'unchanged_dependency_actions':len(unchanged_actions),'read_only_build_cache':True,'remote_execution':False,'fresh_test_results':True,'memory_policy':['discard_analysis_cache','nokeep_state_after_build','notrack_incremental_state']})
    unit = manifest['unit_tests']
    phases = [
        ('focused-unit', ['test', '//cmd/bd:bd_composition_test', '//cmd/bd/doctor:doctor_test', '--config=ci', *limits, '--test_filter=^('+'|'.join(unit)+')$'], unit, 3300),
        ('focused-server', ['test', '//cmd/bd:bd_composition_server_test', '--config=doltserver-cmd', *limits, '--test_sharding_strategy=disabled', '--test_arg=-test.run=^TestVersionBumpDirtyTableRefusalKeepsLocalVersionMarker$'], ['TestVersionBumpDirtyTableRefusalKeepsLocalVersionMarker'], 1800),
    ]
    results = []
    for name, args, expected_tests, timeout in phases:
        log = ARTIFACTS / (name+'.log')
        print('Running', name, 'with canonical Bazel selection and unchanged ceilings', flush=True)
        started = time.monotonic()
        remaining=int(worker_deadline-time.monotonic());assert remaining>0, 'Existing service deadline exhausted'
        code = bounded_command(startup+args, log, min(timeout,remaining))
        tail = '\n'.join(log.read_text().splitlines()[-24:])[-8192:]
        print(tail, flush=True)
        assert code == 0, f'{name} Bazel exit {code}'
        proof = assert_selected(log, expected_tests)
        results.append({'phase':name, 'exit':code, 'seconds':time.monotonic()-started, **proof})
        save('tests.json', results)
        print(json.dumps({'phase':name, 'named_root_passes':len(proof['required_roots']), 'all_pass_lines':len(proof['passed']), 'skips':len(proof['skipped'])}), flush=True)
        logs = WORKSPACE / 'bazel-testlogs'
        if logs.exists():
            for xml in logs.resolve().rglob('test.xml'):
                relative = str(xml.relative_to(logs.resolve())).replace('/', '__')
                if xml.stat().st_size < 4*1024*1024:
                    assert artifact_size()+xml.stat().st_size < ARTIFACT_LIMIT
                    shutil.copyfile(xml, ARTIFACTS / (name+'__'+relative))
        assert artifact_size() < ARTIFACT_LIMIT
        save('cgroup-peak.json', {'memory_peak':int((group/'memory.peak').read_text()), 'memory_max':int((group/'memory.max').read_text())})
    return 0


def main():
    manifest = json.loads((HERE/'manifest.json').read_text())
    ARTIFACTS.mkdir(exist_ok=True)
    if sys.argv[1:] == ['--worker']:
        return worker(manifest)
    if sys.argv[1:] == ['--launch-service']:
        return launch_service()
    assert not sys.argv[1:]
    parent = subprocess.check_output(['git', 'rev-parse', 'HEAD^'], cwd=WORKSPACE, text=True).strip()
    subprocess.run(['git', 'merge-base', '--is-ancestor', manifest['base_6949'], 'HEAD'], cwd=WORKSPACE, check=True)
    changed = subprocess.check_output(['git', 'diff', '--name-only', manifest['base_6949'], 'HEAD'], cwd=WORKSPACE, text=True).splitlines()
    assert sorted(changed) == sorted([*manifest['files'], '.github/composition-6949/manifest.json'])
    for name, expected in manifest['files'].items():
        assert hashlib.sha256((WORKSPACE/name).read_bytes()).hexdigest() == expected, name
    for name, expected in manifest['fixture_files'].items():
        assert hashlib.sha256((WORKSPACE/name).read_bytes()).hexdigest()==expected, name
    save('source.json', {'head':os.environ['GITHUB_SHA'], 'parent':parent, 'base_6949':manifest['base_6949'], 'manifest':manifest, 'changed':changed})
    import budget
    original_rss, original_stop = budget.process_rss, budget.stop
    def account_all_service_processes():
        groups = original_rss()
        try:
            rss = cgroup_rss()
        except (OSError, ValueError, AssertionError) as error:
            detail={'error_class':type(error).__name__,'errno':getattr(error,'errno',None),'message':str(error)[:1000],'stage':'owned-service-cgroup'}
            save('measurement-failure.json',detail)
            raise budget.MonitorError('Cannot account owned service cgroup: '+json.dumps(detail)) from error
        return {key:value+rss for key,value in groups.items()}
    def stop_all_service_processes(child):
        stop_unit()
        try:
            child.wait(timeout=5)
        except subprocess.TimeoutExpired:
            pass
        original_stop(child)
    budget.process_rss = account_all_service_processes
    budget.stop = stop_all_service_processes
    free = shutil.disk_usage(WORKSPACE).free
    # A lower per-job budget is permitted; reserve and existing maxima stay fixed.
    admitted_gib = min(12, (free - 20*GIB - 64*1024*1024)//GIB)
    save('admission.json', {'free_bytes':free, 'disk_budget_gib':admitted_gib, 'max_disk_gib':12, 'reserve_gib':20, 'rss_gib':3, 'swap_growth_mib':512, 'module_gib':8, 'sampled_guard':True})
    print('Admission:', (ARTIFACTS/'admission.json').read_text(), flush=True)
    if admitted_gib < 6:
        raise RuntimeError('Insufficient disk for minimum focused job plus unchanged20GiB reserve')
    runner_temp = Path(os.environ['RUNNER_TEMP']).resolve()
    assert runner_temp.is_absolute() and not runner_temp.is_relative_to(Path('/tmp'))
    from storage import OwnedStorage
    source_bytes=budget.directory_size(WORKSPACE)
    original_refusal=budget.refusal
    budget.refusal=lambda current_free, initial_free, limit:original_refusal(current_free, free+source_bytes, limit)
    def interrupted(number, frame):
        raise SystemExit(128+number)
    signal.signal(signal.SIGINT,interrupted)
    signal.signal(signal.SIGTERM,interrupted)
    storage=None
    try:
        storage=OwnedStorage(runner_temp,int(admitted_gib)*GIB,source_bytes,ARTIFACT_LIMIT)
        save('storage.json',storage.probe())
        tempfile.tempdir=str(storage.mount)
        modules=storage.modules
        os.environ['GOMODCACHE'] = str(modules)
        os.environ['COMPOSITION_PRIVATE_TMP']=str(storage.private_tmp)
        os.environ['COMPOSITION_STORAGE_DEVICE']=str(storage.mount_identity[0])
        os.environ.update(GOMEMLIMIT='384MiB', GOGC='20', GOMAXPROCS='1')
        def consistent_owned_size(roots):
            try:
                used=storage.measure_roots(roots)
                storage.observed_peak=max(getattr(storage,'observed_peak',0),used)
                save('storage-observation.json',{'used_bytes':used,'peak_bytes':storage.observed_peak,'capacity_bytes':storage.capacity,'source_bytes':source_bytes,'artifacts_bytes':artifact_size(),'total_budget_bytes':int(admitted_gib)*GIB})
                return used+source_bytes+artifact_size()
            except (OSError,ValueError,AssertionError) as error:
                raise budget.MonitorError('Owned bounded filesystem metric unavailable') from error
        budget.owned_directory_size=consistent_owned_size
        result = budget.run([sys.executable, str(Path(__file__).resolve()), '--launch-service'], int(admitted_gib)*GIB, temporary=True, memory_budget=3*GIB, swap_growth=512*1024*1024, go_cache_budget=8*GIB)
        save('result.json', {'exit_code':result, 'composition_passed':result==0, 'head':os.environ['GITHUB_SHA'], 'parent':parent, 'upstream_required_gates_unchanged':True})
        return result
    finally:
        stop_unit()
        if storage is not None:
            storage.close()
            save('storage-teardown.json',{'owned_root_removed':not storage.root.exists(),'mounted':False,'source_bytes':source_bytes,'image_cap_bytes':storage.capacity,'max_artifacts_bytes':ARTIFACT_LIMIT,'total_budget_bytes':int(admitted_gib)*GIB})
        assert artifact_size() < ARTIFACT_LIMIT


if __name__ == '__main__':
    sys.exit(main())
