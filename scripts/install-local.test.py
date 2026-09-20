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
    HEAD = 'a' * 40

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
            'commit': self.HEAD[:8],
        }))

    def add_pending_0012(self):
        journal_path = self.repo / 'drizzle/meta/_journal.json'
        journal = json.loads(journal_path.read_text())
        journal['entries'].append({'tag': '0012_curved_bloodstorm'})
        journal_path.write_text(json.dumps(journal))
        (self.repo / 'drizzle/0012_curved_bloodstorm.sql').write_text('SELECT 12;')

    def install(self, bad_resolve=False, validate=False, validator='success'):
        resolve_count = 0

        def check_output(command, **kwargs):
            nonlocal resolve_count
            if command[:3] == ['git', 'rev-parse', 'HEAD']:
                return self.HEAD
            resolve_count += 1
            if bad_resolve and resolve_count == 2:
                return 'unexpected-launch-target'
            return json.loads(self.placed.read_text())['newBinaryPath']

        def run(command, **kwargs):
            if command[0] == '/usr/bin/codesign':
                return installer.subprocess.CompletedProcess(command, 0)
            if validator == 'failure':
                kwargs['stderr'].write(b'validation failed')
                return installer.subprocess.CompletedProcess(command, 1)
            values = dict(zip(command[2::2], command[3::2]))
            pending = [{'tag': '0012_curved_bloodstorm',
                        'hash': installer.digest(self.repo / 'drizzle/0012_curved_bloodstorm.sql')}]
            report = {
                'status': 'ok', 'validator': 1,
                'backupSha256': values['--backup-sha256'],
                'sourceSha256': values['--source-sha256'],
                'commit': values['--commit'], 'version': values['--version'],
                'targetMigrations': pending,
                'checks': {
                    'migrationApplied': True, 'quickCheck': True, 'foreignKeyCheck': True,
                    'schemaMatchesFresh': True, 'rowCountsPreserved': True,
                    'affectedFieldsPreserved': True, 'nextSeqValid': True, 'idempotent': True,
                },
            }
            if validator == 'mismatch':
                report['sourceSha256'] = '0' * 64
            kwargs['stdout'].write(json.dumps(report).encode())
            return installer.subprocess.CompletedProcess(command, 0)

        argv = ['install-local.py'] + (['--validate-migrations'] if validate else [])
        with patch.object(installer, '__file__', str(self.repo / 'scripts/install-local.py')), \
             patch.object(installer.Path, 'home', return_value=self.home), \
             patch.object(installer.sys, 'platform', 'darwin'), \
             patch.object(installer.sys, 'argv', argv), \
             patch.object(installer.subprocess, 'run', side_effect=run) as run_mock, \
             patch.object(installer.subprocess, 'check_output', side_effect=check_output), \
             patch.dict(installer.os.environ, {'NARRAFORK_BUN': '/usr/bin/true'}), \
             contextlib.redirect_stdout(io.StringIO()):
            installer.main()
        codesign_calls = [call for call in run_mock.call_args_list if call.args[0][0] == '/usr/bin/codesign']
        self.assertEqual(len(codesign_calls), 2)
        for call in codesign_calls:
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

    def test_pending_migration_is_rejected_by_default(self):
        self.add_pending_0012()
        old_placed = self.placed.read_bytes()
        with self.assertRaisesRegex(AssertionError, 'Validate pending migrations separately'):
            self.install()
        self.assertFalse(self.target.exists())
        self.assertEqual(self.placed.read_bytes(), old_placed)
        self.assertEqual(self.link.resolve(), self.old)

    def test_validated_pending_migration_allows_install_and_persists_report(self):
        self.add_pending_0012()
        self.install(validate=True)
        self.assertEqual(self.target.read_bytes(), b'first signed build')
        backup = next(self.state.glob('backup-before-stable-signing-*'))
        record = json.loads((backup / 'install-record.json').read_text())
        self.assertEqual(record['migrationValidation']['status'], 'ok')
        self.assertEqual(record['pendingMigrations'], ['0012_curved_bloodstorm'])
        self.assertEqual(installer.digest(backup / 'narrafork.db'),
                         record['migrationValidation']['backupSha256'])

    def test_validation_failure_does_not_replace_binary_or_pointers(self):
        self.add_pending_0012()
        old_placed = self.placed.read_bytes()
        with self.assertRaises(installer.subprocess.CalledProcessError):
            self.install(validate=True, validator='failure')
        self.assertFalse(self.target.exists())
        self.assertEqual(self.placed.read_bytes(), old_placed)
        self.assertEqual(self.link.resolve(), self.old)
        backup = next(self.state.glob('backup-before-stable-signing-*'))
        self.assertEqual((backup / 'migration-validation.stderr.log').read_text(),
                         'validation failed')
        self.assertEqual(installer.digest(backup / 'narrafork.db'),
                         installer.digest(backup / 'narrafork.validation.db'))

    def test_mismatched_validation_report_is_rejected_before_replacement(self):
        self.add_pending_0012()
        old_placed = self.placed.read_bytes()
        with self.assertRaisesRegex(AssertionError, 'report is incomplete'):
            self.install(validate=True, validator='mismatch')
        self.assertFalse(self.target.exists())
        self.assertEqual(self.placed.read_bytes(), old_placed)
        self.assertEqual(self.link.resolve(), self.old)


if __name__ == '__main__':
    unittest.main()
