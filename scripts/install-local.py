"""Install a signed selfuse build at a stable actual path; never restart the service.

Usage: python3 scripts/install-local.py [--check | --validate-migrations]
This intentionally supports the local supervisor installation only, not ks-mini.
"""
import datetime
from contextlib import closing
import hashlib
import json
import os
import plistlib
import re
import shutil
import sqlite3
import subprocess
import sys
import time
from pathlib import Path


def digest(path):
    h = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            h.update(chunk)
    return h.hexdigest()


def validate_report(report, *, backup_sha256, source_sha256, commit, version, pending):
    assert report == {
        'status': 'ok',
        'validator': 1,
        'backupSha256': backup_sha256,
        'sourceSha256': source_sha256,
        'commit': commit,
        'version': version,
        'targetMigrations': pending,
        'checks': {
            'migrationApplied': True,
            'quickCheck': True,
            'foreignKeyCheck': True,
            'schemaMatchesFresh': True,
            'rowCountsPreserved': True,
            'affectedFieldsPreserved': True,
            'nextSeqValid': True,
            'idempotent': True,
        },
    }, 'Migration validation report is incomplete or does not match this install'


def main():
    if not __debug__:
        raise RuntimeError('Run without Python -O: installation safety checks are required')
    assert sys.argv[1:] in ([], ['--check'], ['--validate-migrations']), __doc__
    check_only = sys.argv[1:] == ['--check']
    validate_migrations = sys.argv[1:] == ['--validate-migrations']
    assert sys.platform == 'darwin'
    os.umask(0o077)
    home = Path.home()
    state, bin_dir = home / '.narrafork', home / '.local/bin'
    repo = Path(__file__).resolve().parents[1]
    version = json.loads((repo / 'package.json').read_text())['version']
    arch = {'arm64': 'arm64', 'x86_64': 'x64'}[os.uname().machine]
    source = repo / 'dist' / f'narrafork-{version}-macos-{arch}'
    metadata = json.loads(Path(str(source) + '.metadata.json').read_text())
    assert metadata['version'] == version and metadata['platform'] == f'darwin-{arch}'
    assert metadata['name'] == source.name and metadata['size'] == source.stat().st_size
    head = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True, timeout=10).strip()
    assert re.fullmatch(r'[0-9a-f]{40}', head)
    assert metadata.get('commit') and head.startswith(metadata['commit']), 'Build commit does not match HEAD'
    assert 0 < metadata['size'] <= 512 * 1024 * 1024
    expected = metadata['sha256']
    assert re.fullmatch(r'[0-9a-f]{64}', expected) and digest(source) == expected
    identity = json.loads((state / 'code-signing/identity.json').read_text())
    fingerprint = identity['fingerprint']
    assert re.fullmatch(r'[0-9a-fA-F]{40}', fingerprint)
    requirement = f'=identifier "app.narrafork.selfuse" and certificate leaf = H"{fingerprint}"'
    subprocess.run(['/usr/bin/codesign', '--verify', '--strict', '-R', requirement, str(source)], check=True, timeout=30)
    target = bin_dir / 'narrafork-server'
    assert not target.is_symlink(), 'The stable actual binary must not be a symlink'
    placed, link = state / 'updates/placed-update.json', bin_dir / 'narrafork'
    assert link.is_symlink()
    old_link, old_placed = os.readlink(link), placed.read_bytes()
    old_data = json.loads(old_placed)
    old_binary = Path(old_data['newBinaryPath'])
    assert old_binary.is_file() and old_binary.parent == bin_dir
    assert not target.exists() or old_binary == target, 'Unexpected existing stable binary'
    supervisor = bin_dir / 'narrafork-launchd-supervisor'
    plist = home / 'Library/LaunchAgents/dev.narrafork.server.plist'
    assert plistlib.loads(plist.read_bytes())['ProgramArguments'] == [str(supervisor)]
    def resolve():
        return subprocess.check_output([str(supervisor), '--resolve'], text=True, timeout=10).strip()
    assert resolve() == str(old_binary)
    if check_only:
        print(json.dumps({'preflight': 'ok', 'source': str(source), 'sha256': expected, 'target': str(target), 'oldBinary': str(old_binary)}))
        return

    required_space = 2 * (state / 'narrafork.db').stat().st_size + 4 * source.stat().st_size + 1024**3
    assert shutil.disk_usage(state).free > required_space, 'Not enough space for a safe backup and install'
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
    backup = state / ('backup-before-stable-signing-' + stamp)
    backup.mkdir(mode=0o700)
    (backup / 'placed-update.json').write_bytes(old_placed)
    (backup / 'launcher-target.txt').write_text(old_link)
    shutil.copy2(plist, backup / 'launch-agent.plist')
    shutil.copy2(supervisor, backup / supervisor.name)
    shutil.copy2(old_binary, backup / old_binary.name)
    old_sha256 = digest(old_binary)
    assert digest(backup / old_binary.name) == old_sha256
    start = time.monotonic()
    def progress(status, remaining, total):
        if time.monotonic() - start > 120:
            raise TimeoutError('Online database backup exceeded 120 seconds')
    backup_db = backup / 'narrafork.db'
    with closing(sqlite3.connect((state / 'narrafork.db').as_uri() + '?mode=ro', uri=True, timeout=0.1)) as src:
        with closing(sqlite3.connect(backup_db, timeout=5)) as dst:
            src.backup(dst, pages=512, progress=progress, sleep=0.01)
            dst.execute('PRAGMA wal_checkpoint(TRUNCATE)')
            dst.execute('PRAGMA journal_mode=DELETE')
            dst.set_progress_handler(lambda: int(time.monotonic() - start > 240), 10000)
            assert dst.execute('PRAGMA quick_check').fetchall() == [('ok',)]
            applied = {row[0] for row in dst.execute('SELECT hash FROM __drizzle_migrations LIMIT 10000')}
            journal = json.loads((repo / 'drizzle/meta/_journal.json').read_text())
            pending = [{'tag': e['tag'], 'hash': digest(repo / 'drizzle' / (e['tag'] + '.sql'))}
                       for e in journal['entries']
                       if digest(repo / 'drizzle' / (e['tag'] + '.sql')) not in applied]
    backup_sha256 = digest(backup_db)
    migration_validation = None
    if pending:
        assert validate_migrations, ('Validate pending migrations separately before installing',
                                     [entry['tag'] for entry in pending])
        assert [entry['tag'] for entry in pending] == ['0012_curved_bloodstorm'], \
            ('Validation is limited to migration 0012', [entry['tag'] for entry in pending])
        validation_db = backup / 'narrafork.validation.db'
        shutil.copy2(backup_db, validation_db)
        assert digest(validation_db) == backup_sha256
        isolated_home = backup / 'validation-home'
        isolated_home.mkdir(mode=0o700)
        bun = Path(os.environ.get('NARRAFORK_BUN') or shutil.which('bun') or '')
        assert bun.is_file(), 'Bun runtime not found'
        command = [str(bun), str(repo / 'scripts/validate-local-upgrade.ts'),
                   '--database', str(validation_db), '--migrations', str(repo / 'drizzle'),
                   '--online-database', str(state / 'narrafork.db'),
                   '--backup-original', str(backup_db), '--backup-sha256', backup_sha256,
                   '--source-sha256', expected, '--commit', head, '--version', version]
        env = dict(os.environ, NARRAFORK_HOME=str(isolated_home), NARRAFORK_LOG_LEVEL='error')
        validation_stdout = backup / 'migration-validation.stdout.log'
        validation_stderr = backup / 'migration-validation.stderr.log'
        try:
            with validation_stdout.open('xb') as stdout_log, validation_stderr.open('xb') as stderr_log:
                completed = subprocess.run(command, cwd=repo, env=env, stdout=stdout_log,
                                           stderr=stderr_log, timeout=300, check=False)
        finally:
            assert digest(backup_db) == backup_sha256, \
                'Migration validation changed the original backup'
        assert validation_stdout.stat().st_size <= 65536 and validation_stderr.stat().st_size <= 65536, \
            'Migration validator output exceeded 64 KiB'
        completed.check_returncode()
        migration_validation = json.loads(validation_stdout.read_text())
        validate_report(migration_validation, backup_sha256=backup_sha256,
                        source_sha256=expected, commit=head, version=version, pending=pending)

    temporary = target.with_name(target.name + '.install-' + stamp)
    placed_tmp = placed.with_name('placed-update.install-' + stamp + '.json')
    link_tmp = link.with_name(link.name + '.install-' + stamp)
    updated = dict(old_data)
    updated.update(version=version, fromVersion=old_data['version'], fileName=source.name,
                   newBinaryPath=str(target), updatePath=str(target), placed=True,
                   placedAt=datetime.datetime.now(datetime.timezone.utc).isoformat(),
                   sizeBytes=metadata['size'], sha512=metadata['sha512'])
    result = {'backup': str(backup), 'installed': str(target), 'sha256': expected,
              'sourceCommit': head, 'migrationCountBefore': len(applied),
              'embeddedMigrations': len(journal['entries']),
              'pendingMigrations': [entry['tag'] for entry in pending],
              'migrationValidation': migration_validation,
              'oldBinary': str(old_binary), 'oldSha256': old_sha256,
              'signingIdentifier': 'app.narrafork.selfuse', 'restarted': False}
    # Persist recovery locations before switching anything. This installer never restarts NF.
    (backup / 'install-record.json').write_text(json.dumps(result, indent=2))
    switched = False
    try:
        shutil.copy2(source, temporary)
        temporary.chmod(0o755)
        assert digest(temporary) == expected
        subprocess.run(['/usr/bin/codesign', '--verify', '--strict', '-R', requirement, str(temporary)], check=True, timeout=30)
        with placed_tmp.open('x') as stream:
            json.dump(updated, stream, indent=2)
            stream.flush()
            os.fsync(stream.fileno())
        os.symlink(str(target), link_tmp)
        assert placed.read_bytes() == old_placed and os.readlink(link) == old_link, 'Installation changed concurrently'
        assert digest(old_binary) == old_sha256, 'Installed binary changed concurrently'
        os.replace(temporary, target)
        switched = True
        os.replace(link_tmp, link)
        os.replace(placed_tmp, placed)
        assert resolve() == str(target) and link.resolve() == target.resolve()
        assert digest(target) == expected
    except BaseException:
        if switched:
            if old_binary == target:
                shutil.copy2(backup / old_binary.name, temporary)
                os.replace(temporary, target)
            else:
                target.unlink()
            if link_tmp.is_symlink():
                link_tmp.unlink()
            os.symlink(old_link, link_tmp)
            os.replace(link_tmp, link)
            placed_tmp.write_bytes(old_placed)
            os.replace(placed_tmp, placed)
        raise
    finally:
        for path in [temporary, placed_tmp, link_tmp]:
            if path.exists() or path.is_symlink():
                path.unlink()
    print(json.dumps(result), flush=True)


if __name__ == '__main__':
    main()
