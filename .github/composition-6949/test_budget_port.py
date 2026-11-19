import subprocess
import tempfile
from pathlib import Path
import unittest
from unittest import mock
import budget


class LinuxDuDiagnostics(unittest.TestCase):
    def setUp(self):
        self.root = Path('/home/runner/work/_temp/workspace-job-owned')
        self.child = str(self.root/'bazel/sandbox/3/execroot')
        self.platform = mock.patch.object(budget.sys, 'platform', 'linux')
        self.platform.start()
        self.addCleanup(self.platform.stop)

    def result(self, diagnostic):
        return subprocess.CompletedProcess(['/usr/bin/du'], 1, '8\t'+str(self.root)+'\n', diagnostic+'\n')

    def accepted(self, diagnostic):
        return budget._only_vanished_children(self.result(diagnostic), self.root)

    def test_all_native_missing_child_diagnostics(self):
        for program in ['du', '/usr/bin/du']:
            for detail in [f"cannot access '{self.child}'", f"cannot read directory '{self.child}'", f'fts_read failed: {self.child}', f"fts_read failed: '{self.child}'"]:
                with self.subTest(program=program, detail=detail):
                    self.assertTrue(self.accepted(f'{program}: {detail}: No such file or directory'))

    def test_root_ancestor_permissions_and_unknown_messages_refuse(self):
        for detail in [f'fts_read failed: {self.root}', 'fts_read failed: /home/runner/work', f"cannot access '{self.root}/../outside'", f'unknown error: {self.child}']:
            with self.subTest(detail=detail):
                self.assertFalse(self.accepted('/usr/bin/du: '+detail+': No such file or directory'))
        self.assertFalse(self.accepted(f'/usr/bin/du: fts_read failed: {self.child}: Permission denied'))
        self.assertFalse(self.accepted(f'/unknown/du: fts_read failed: {self.child}: No such file or directory'))

    def test_bounded_reread_preserves_partial_peak(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            result = subprocess.CompletedProcess(['/usr/bin/du'], 1, '8\t'+str(root)+'\n', f'/usr/bin/du: fts_read failed: {root}/vanished: No such file or directory\n')
            failure = budget.MetricCommandError('native transient', result)
            with mock.patch.object(budget, 'metric_output', side_effect=[failure, '4\t'+str(root)+'\n']):
                self.assertEqual(budget.directory_size(root), 8*1024)

    def test_unresolved_vanishing_still_fails_after_three_reads(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            result = subprocess.CompletedProcess(['/usr/bin/du'], 1, '8\t'+str(root)+'\n', f'/usr/bin/du: fts_read failed: {root}/vanished: No such file or directory\n')
            failure = budget.MetricCommandError('native transient', result)
            with mock.patch.object(budget, 'metric_output', side_effect=[failure, failure, failure]) as metric:
                with self.assertRaises(budget.MonitorError):
                    budget.directory_size(root)
                self.assertEqual(metric.call_count, 3)


if __name__ == '__main__':
    unittest.main(verbosity=2)
