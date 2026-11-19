#!/usr/bin/env python3
"""Safety checks for the git-workflow cleanup script, using disposable repositories only."""
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import time
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('cleanup', Path(__file__).with_name('clean-build-artifacts.py'))
cleanup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cleanup)


class CleanupTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='gateway-cleanup-test-')
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve()
        subprocess.run(['git', 'init', '-q', str(self.root)], check=True)

    def file(self, name, text='artifact'):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
        return path

    def test_preview_apply_preserve_data_and_repeat(self):
        outputs = [self.file('bin/dev/gateway'), self.file('build/dev/Test.app/exe'), self.file('gateway-desktop')]
        protected = [self.file('data/dev/gateway.db'), self.file('.env.release'), self.file('resources/icon.png')]
        with patch.object(cleanup, 'process_snapshot', return_value=([], [])):
            cleanup.clean(list(cleanup.project_outputs(self.root)))
            self.assertTrue(all(path.exists() for path in outputs))
            self.assertFalse(cleanup.clean(list(cleanup.project_outputs(self.root)), apply=True))
            self.assertTrue(all(not path.exists() for path in outputs))
            self.assertTrue(all(path.exists() for path in protected))
            self.assertFalse(cleanup.clean(list(cleanup.project_outputs(self.root)), apply=True))

    def test_tracked_file_symlink_and_builder_are_protected(self):
        tracked = self.file('build/keep.txt')
        subprocess.run(['git', '-C', str(self.root), 'add', 'build/keep.txt'], check=True)
        outside = self.file('outside/keep.txt')
        (self.root / 'bin').symlink_to(outside.parent, target_is_directory=True)
        binary = self.file('gateway-desktop')
        with patch.object(cleanup, 'process_snapshot', return_value=(['/usr/local/go/bin/go'], [])):
            self.assertTrue(cleanup.clean(list(cleanup.project_outputs(self.root)), apply=True))
        self.assertTrue(all(p.exists() for p in [tracked, outside, binary]))

    def test_open_artifact_is_protected_by_real_process_check(self):
        binary = self.file('bin/gateway')
        with binary.open() as handle:
            self.assertFalse(handle.closed)
            self.assertTrue(cleanup.clean(list(cleanup.project_outputs(self.root)), apply=True))
        self.assertTrue(binary.exists())

    def test_recently_updated_nested_output_is_preserved_in_cross_worktree_sweep(self):
        binary = self.file('build/dev/App.app/exe')
        old = time.time() - 7200
        os.utime(self.root / 'build', (old, old))
        with patch.object(cleanup, 'process_snapshot', return_value=([], [])):
            self.assertTrue(cleanup.clean(list(cleanup.project_outputs(self.root)), apply=True, min_age_hours=1))
        self.assertTrue(binary.exists())

    def test_only_old_project_temporary_builds_are_selected(self):
        for name, text in [('go-build1', 'packagefile gatewaylab/internal/api=/tmp/a.a'),
                           ('go-build2', 'packagefile unrelated/module=/tmp/b.a'),
                           ('go-build3', 'packagefile gatewaylab/internal/api=/tmp/c.a')]:
            self.file(f'{name}/b001/importcfg', text)
        old = time.time() - 7200
        for name in ['go-build1', 'go-build2']:
            os.utime(self.root / name, (old, old))
        (self.root / 'go-build4').symlink_to(self.root / 'go-build1', target_is_directory=True)
        self.assertEqual([p.name for p, _ in cleanup.temp_outputs(self.root)], ['go-build1'])

    def test_open_file_inspection_failure_aborts(self):
        with patch.object(cleanup, 'run', return_value=''), patch.object(cleanup.subprocess, 'run') as run:
            run.return_value = subprocess.CompletedProcess([], 1, '', 'cannot inspect processes')
            with self.assertRaises(RuntimeError):
                cleanup.process_snapshot()


if __name__ == '__main__':
    unittest.main()
