"""Run: python3 scripts/install-local.test.py (no real keychain, service or user DB access)."""
import contextlib
import hashlib
import importlib.util
import io
import json
import os
import plistlib
import sqlite3
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('installer', Path(__file__).with_name('install-local.py'))
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class InstallTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='nf-install-check-')
        self.addCleanup(self.tmp.cleanup)
        root = Path(self.tmp.name).resolve()
        self.home, self.repo = root / 'home', root / 'repo'
        self.bin = self.home / '.local/bin'
        self.state = self.home / '.narrafork'
        for p in [self.bin, self.state / 'updates', self.state / 'code-signing',
                  self.home / 'Library/LaunchAgents', self.repo / 'dist', self.repo / 'drizzle/meta']:
            p.mkdir(parents=True)
        self.target = self.bin / 'narrafork-server'
        self.placed = self.state / 'updates/placed-update.json'
        self.link = self.bin / 'narrafork'
        self.old = self.bin / 'old-versioned-binary'
        self.old.write_bytes(b'original')
        self.link.symlink_to(self.old)
        self.placed.write_text(json.dumps({'version': '0.7.3', 'newBinaryPath': str(self.old)}))
        supervisor = self.bin / 'narrafork-launchd-supervisor'
        supervisor.write_text('not executed')
        (self.home / 'Library/LaunchAgents/dev.narrafork.server.plist').write_bytes(
            plistlib.dumps({'ProgramArguments': [str(supervisor)]}))
        (self.state / 'code-signing/identity.json').write_text(json.dumps({'fingerprint': 'A' * 40}))
        (self.repo / 'package.json').write_text(json.dumps({'version': '0.7.3'}))
        (self.repo / 'drizzle/meta/_journal.json').write_text(json.dumps({'entries': [{'tag': '0001'}]}))
        sql = self.repo / 'drizzle/0001.sql'
        sql.write_text('SELECT 1;')
        with sqlite3.connect(self.state / 'narrafork.db') as db:
            db.execute('CREATE TABLE __drizzle_migrations (hash TEXT)')
            db.execute('INSERT INTO __drizzle_migrations VALUES (?)', (installer.digest(sql),))
        db.close()
        self.arch = {'arm64': 'arm64', 'x86_64': 'x64'}[os.uname().machine]
        self.source = self.repo / f'dist/narrafork-0.7.3-macos-{self.arch}'
        self.build(b'first signed build')

    def build(self, data):
        self.source.write_bytes(data)
        Path(str(self.source) + '.metadata.json').write_text(json.dumps({
            'version': '0.7.3', 'platform': f'darwin-{self.arch}', 'name': self.source.name,
            'size': len(data), 'sha256': hashlib.sha256(data).hexdigest(), 'sha512': 'fixture',
        }))

    def install(self, bad_resolve=False):
        count = 0
        def resolve(*args, **kwargs):
            nonlocal count
            count += 1
            if bad_resolve and count == 2:
                return 'unexpected-launch-target'
            return json.loads(self.placed.read_text())['newBinaryPath']
        with patch.object(installer, '__file__', str(self.repo / 'scripts/install-local.py')), \
             patch.object(installer.Path, 'home', return_value=self.home), \
             patch.object(installer.sys, 'platform', 'darwin'), \
             patch.object(installer.sys, 'argv', ['install-local.py']), \
             patch.object(installer.subprocess, 'run') as run, \
             patch.object(installer.subprocess, 'check_output', side_effect=resolve), \
             contextlib.redirect_stdout(io.StringIO()):
            installer.main()
            self.assertEqual(run.call_count, 2)
            for call in run.call_args_list:
                self.assertEqual(call.args[0][0], '/usr/bin/codesign')
                self.assertIn('=identifier "app.narrafork.selfuse" and certificate leaf', call.args[0][4])

    def test_repeated_install_keeps_actual_path_and_backs_up_old_binary(self):
        self.install()
        first_inode = self.target.stat().st_ino
        self.assertEqual(self.target.read_bytes(), b'first signed build')
        self.build(b'second signed build')
        self.install()
        self.assertEqual(self.target.read_bytes(), b'second signed build')
        self.assertNotEqual(first_inode, self.target.stat().st_ino)
        self.assertFalse(self.target.is_symlink())
        self.assertEqual(self.link.resolve(), self.target)
        self.assertEqual(json.loads(self.placed.read_text())['newBinaryPath'], str(self.target))
        backups = list(self.state.glob('backup-before-stable-signing-*'))
        self.assertEqual(len(backups), 2)
        self.assertTrue(any((p / 'narrafork-server').exists() and
                            (p / 'narrafork-server').read_bytes() == b'first signed build' for p in backups))

    def test_failed_post_install_check_restores_previous_stable_binary_and_pointers(self):
        self.install()
        old_placed = self.placed.read_bytes()
        self.build(b'rejected build')
        with self.assertRaises(AssertionError):
            self.install(bad_resolve=True)
        self.assertEqual(self.target.read_bytes(), b'first signed build')
        self.assertEqual(self.placed.read_bytes(), old_placed)
        self.assertEqual(self.link.resolve(), self.target)
        self.assertFalse(list(self.bin.glob('*.install-*')))


if __name__ == '__main__':
    unittest.main()
