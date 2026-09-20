"""Install an uploaded signed selfuse bundle on ks-mini; restart is a separate command.

Usage: python3 install-ks-mini.py <bundle-directory> [--check] [--validate-migrations] [--prepare-update]
The bundle contains deploy.json and its named artifact; validation bundles also contain the
validator and migrations. Bundles never contain signing keys.
"""
import datetime
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
import urllib.request
from contextlib import closing
from pathlib import Path


def digest(path):
    result = hashlib.sha256()
    with path.open('rb') as stream:
        for data in iter(lambda: stream.read(1024 * 1024), b''):
            result.update(data)
    return result.hexdigest()


DEFAULT_LAUNCHD_LABEL = 'com.narrafork.server'
LAUNCHD_LABEL_PATTERN = re.compile(r'[A-Za-z0-9][A-Za-z0-9._-]{0,127}')
HEALTH_READ_LIMIT = 65536


def health_url(settings_path):
    server = json.loads(settings_path.read_text()).get('server', {}) if settings_path.is_file() else {}
    host = server.get('host') or '127.0.0.1'
    port = server.get('port') or 7778
    assert isinstance(port, int) and 0 < port < 65536, 'Invalid server port in settings'
    host = {'0.0.0.0': '127.0.0.1', '::': '::1', '::0': '::1'}.get(host, host)
    if ':' in host and not host.startswith('['):
        host = f'[{host}]'
    scheme = 'https' if (server.get('tls') or {}).get('enabled') else 'http'
    return f'{scheme}://{host}:{port}/api/health'


def read_health(url):
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(url, timeout=10) as response:
        body = response.read(HEALTH_READ_LIMIT + 1)
    assert len(body) <= HEALTH_READ_LIMIT, 'Health response exceeded 64 KiB'
    payload = json.loads(body)
    commit, version = payload.get('commit'), payload.get('version')
    assert payload.get('status') == 'ok' and payload.get('readiness') == 'ready', \
        'Running server is not healthy'
    assert isinstance(commit, str) and re.fullmatch(r'[a-f0-9]{8,40}', commit), \
        'Running server commit is unusable'
    assert isinstance(version, str) and version, 'Running server version is unusable'
    return {'commit': commit, 'version': version,
            'stableUpdateSignal': payload.get('stableUpdateSignal')}


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
        raise RuntimeError('Run without Python -O: installation checks are required')
    assert len(sys.argv) in (2, 3, 4), __doc__
    flags = sys.argv[2:]
    allowed = {'--check', '--validate-migrations', '--prepare-update'}
    assert len(set(flags)) == len(flags) and set(flags) <= allowed, __doc__
    check_only = '--check' in flags
    validate_migrations = '--validate-migrations' in flags
    prepare_update = '--prepare-update' in flags
    assert not (check_only and (validate_migrations or prepare_update)), \
        '--check is read-only and cannot be combined with an installing mode'
    home = Path.home()
    assert sys.platform == 'darwin' and str(home) == '/Users/ios' and os.getuid() == 501
    assert os.uname().machine == 'arm64'
    os.umask(0o077)
    state, bin_dir = home / '.narrafork', home / '.local/bin'
    bundle = Path(sys.argv[1]).resolve()
    assert bundle.is_relative_to(state / 'updates')
    config = json.loads((bundle / 'deploy.json').read_text())
    meta = config['metadata']
    assert re.fullmatch(r'narrafork-[0-9.]+-macos-arm64', meta['name'])
    assert meta['platform'] == 'darwin-arm64' and 0 < meta['size'] <= 512 * 1024 * 1024
    assert re.fullmatch(r'[0-9a-f]{64}', meta['sha256'])
    source = bundle / meta['name']
    assert source.is_file() and not source.is_symlink()
    assert source.stat().st_size == meta['size'] and digest(source) == meta['sha256']
    fingerprint = config['certificateSha1']
    assert re.fullmatch(r'[0-9a-fA-F]{40}', fingerprint)
    def verify(path, identifier):
        requirement = f'=identifier "{identifier}" and certificate leaf = H"{fingerprint}"'
        subprocess.run(['/usr/bin/codesign', '--verify', '--strict', '-R', requirement, str(path)],
                       check=True, timeout=30)
    verify(source, 'app.narrafork.selfuse')
    migrations = config['migrations']
    assert isinstance(migrations, list) and 0 < len(migrations) < 1000
    assert len({item['name'] for item in migrations}) == len(migrations)
    assert all(re.fullmatch(r'[0-9]{4}_[a-z0-9_]+\.sql', item['name']) and
               re.fullmatch(r'[0-9a-f]{64}', item['sha256']) for item in migrations)
    validator = None
    migrations_dir = bundle / 'drizzle'
    if validate_migrations:
        validator_meta = config['validator']
        assert validator_meta['name'] == 'validate-local-upgrade'
        assert 0 < validator_meta['size'] <= 512 * 1024 * 1024
        assert re.fullmatch(r'[0-9a-f]{64}', validator_meta['sha256'])
        assert validator_meta['certificateSha1'].lower() == fingerprint.lower()
        validator = bundle / validator_meta['name']
        assert validator.is_file() and not validator.is_symlink()
        assert validator.stat().st_size == validator_meta['size']
        assert digest(validator) == validator_meta['sha256']
        verify(validator, 'app.narrafork.selfuse.validator')
        assert migrations_dir.is_dir() and not migrations_dir.is_symlink()
        for item in migrations:
            migration = migrations_dir / item['name']
            assert migration.is_file() and not migration.is_symlink()
            assert digest(migration) == item['sha256']
        journal_meta_dir = migrations_dir / 'meta'
        assert journal_meta_dir.is_dir() and not journal_meta_dir.is_symlink()
        journal_path = journal_meta_dir / '_journal.json'
        assert journal_path.is_file() and not journal_path.is_symlink()
        journal = json.loads(journal_path.read_text())
        assert [entry['tag'] + '.sql' for entry in journal['entries']] == \
            [item['name'] for item in migrations]
    launchd_label = config.get('launchdLabel', DEFAULT_LAUNCHD_LABEL)
    assert isinstance(launchd_label, str) and LAUNCHD_LABEL_PATTERN.fullmatch(launchd_label), \
        'Invalid launchd label'
    plist = home / 'Library/LaunchAgents' / f'{launchd_label}.plist'
    agent = plistlib.loads(plist.read_bytes())
    args = agent['ProgramArguments']
    assert isinstance(args, list) and len(args) == 1 and isinstance(args[0], str)
    entry = Path(args[0])
    target, cli = bin_dir / 'narrafork-server', bin_dir / 'narrafork'
    assert entry.is_absolute() and entry.parent == bin_dir, 'Launch entry must be in ~/.local/bin'
    assert not target.is_symlink() and target.is_file(), 'The stable binary must be a regular file'
    assert entry.resolve() == target, 'Launch entry must resolve to the stable narrafork-server binary'
    assert entry == target or entry.is_symlink(), 'A versioned launch entry must be a symlink'
    assert cli.is_symlink() or not cli.exists(), 'Do not overwrite an unexpected regular CLI file'
    old_binary = target
    old_stat = old_binary.stat()
    assert old_stat.st_uid == os.getuid(), 'The stable binary is not owned by this user'
    assert old_stat.st_mode & 0o022 == 0, 'The stable binary is group/world writable'
    verify(old_binary, 'app.narrafork.selfuse')
    links = {cli: os.readlink(cli) if cli.is_symlink() else None}
    if entry != target:
        links[entry] = os.readlink(entry)
    placed = state / 'updates/placed-update.json'
    old_placed = placed.read_bytes()
    old_data = json.loads(old_placed)
    assert Path(old_data['newBinaryPath']).resolve() == old_binary
    if check_only:
        print(json.dumps({'preflight': 'ok', 'oldBinary': str(old_binary),
                          'target': str(target), 'sha256': meta['sha256'],
                          'commit': meta['commit'], 'launchdLabel': launchd_label}))
        return

    url, health = None, None
    if prepare_update:
        keep_alive = agent.get('KeepAlive')
        assert agent.get('Label') == launchd_label and (keep_alive is True or (
            isinstance(keep_alive, dict) and keep_alive.get('SuccessfulExit') is False
        )), 'The launch agent must keep the configured job alive after unsuccessful exits'
        url = health_url(state / 'settings.json')
        health = read_health(url)
        assert health['commit'] != meta['commit'], \
            'The running server already reports the build being installed'
        assert health['stableUpdateSignal'] == 'SIGUSR2', \
            'The running server does not advertise SIGUSR2 stable update handoff support'

    required = 2 * (state / 'narrafork.db').stat().st_size + 4 * meta['size'] + 1024**3
    assert shutil.disk_usage(state).free > required, 'Not enough space for safe backup/install'
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
    backup = state / ('backup-before-stable-signing-' + stamp)
    backup.mkdir(mode=0o700)
    shutil.copy2(plist, backup / plist.name)
    (backup / 'placed-update.json').write_bytes(old_placed)
    (backup / 'links.json').write_text(json.dumps({str(k): v for k, v in links.items()}, indent=2))
    shutil.copy2(old_binary, backup / old_binary.name)
    old_sha = digest(old_binary)
    assert digest(backup / old_binary.name) == old_sha
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
            hashes = {r[0] for r in dst.execute('SELECT hash FROM __drizzle_migrations LIMIT 10000')}
            pending_files = [m for m in migrations if m['sha256'] not in hashes]
    backup_sha256 = digest(backup_db)
    pending = [{'tag': Path(item['name']).stem, 'hash': item['sha256']} for item in pending_files]
    migration_validation = None
    if pending:
        assert validate_migrations, ('Validate pending migrations before installing',
                                     [item['name'] for item in pending_files])
        assert [item['name'] for item in pending_files] == ['0012_curved_bloodstorm.sql'], \
            ('Validation is limited to migration 0012', [item['name'] for item in pending_files])
        validation_db = backup / 'narrafork.validation.db'
        shutil.copy2(backup_db, validation_db)
        assert digest(validation_db) == backup_sha256
        isolated_home = backup / 'validation-home'
        isolated_home.mkdir(mode=0o700)
        command = [str(validator),
                   '--database', str(validation_db), '--migrations', str(migrations_dir),
                   '--online-database', str(state / 'narrafork.db'),
                   '--backup-original', str(backup_db), '--backup-sha256', backup_sha256,
                   '--source-sha256', meta['sha256'], '--commit', meta['commit'],
                   '--version', meta['version']]
        env = dict(os.environ, NARRAFORK_HOME=str(isolated_home), NARRAFORK_LOG_LEVEL='error')
        validation_stdout = backup / 'migration-validation.stdout.log'
        validation_stderr = backup / 'migration-validation.stderr.log'
        try:
            with validation_stdout.open('xb') as stdout_log, validation_stderr.open('xb') as stderr_log:
                completed = subprocess.run(command, cwd=bundle, env=env, stdout=stdout_log,
                                           stderr=stderr_log, timeout=300, check=False)
        finally:
            assert digest(backup_db) == backup_sha256, \
                'Migration validation changed the original backup'
        assert validation_stdout.stat().st_size <= 65536 and validation_stderr.stat().st_size <= 65536, \
            'Migration validator output exceeded 64 KiB'
        completed.check_returncode()
        migration_validation = json.loads(validation_stdout.read_text())
        validate_report(migration_validation, backup_sha256=backup_sha256,
                        source_sha256=meta['sha256'], commit=meta['commit'],
                        version=meta['version'], pending=pending)

    signing_dir = state / 'code-signing'
    signing_dir.mkdir(mode=0o700, exist_ok=True)
    assert signing_dir.is_dir() and not signing_dir.is_symlink()
    signing_dir.chmod(0o700)
    identity_path = signing_dir / 'identity.json'
    if identity_path.exists() or identity_path.is_symlink():
        assert identity_path.is_file() and not identity_path.is_symlink()
        identity = json.loads(identity_path.read_text())
        assert identity.get('fingerprint', '').lower() == fingerprint.lower(), \
            'Installed signing fingerprint does not match this bundle'
    else:
        identity_tmp = signing_dir / f'.identity.install-{os.getpid()}.json'
        try:
            with identity_tmp.open('x') as stream:
                json.dump({'fingerprint': fingerprint}, stream)
                stream.flush()
                os.fsync(stream.fileno())
            identity_tmp.chmod(0o600)
            os.link(identity_tmp, identity_path)
        finally:
            if identity_tmp.exists():
                identity_tmp.unlink()

    stable_install = None
    if prepare_update:
        stable_install = {'launchdLabel': launchd_label, 'fromCommit': health['commit'],
                          'toCommit': meta['commit'], 'certificateSha1': fingerprint}
    result = {'backup': str(backup), 'installed': str(target), 'sha256': meta['sha256'],
              'version': meta['version'], 'commit': meta['commit'], 'oldBinary': str(old_binary),
              'oldSha256': old_sha, 'migrationCountBefore': len(hashes),
              'pendingMigrations': [item['tag'] for item in pending],
              'migrationValidation': migration_validation, 'stablePathInstall': stable_install,
              'launchdLabel': launchd_label,
              'signingIdentifier': 'app.narrafork.selfuse', 'restarted': False}
    (backup / 'install-record.json').write_text(json.dumps(result, indent=2))
    updated = dict(old_data)
    updated.update(version=meta['version'], fromVersion=old_data['version'], fileName=meta['name'],
                   newBinaryPath=str(target), updatePath=str(target), placed=True,
                   placedAt=datetime.datetime.now(datetime.timezone.utc).isoformat(),
                   sizeBytes=meta['size'], sha512=meta['sha512'])
    if prepare_update:
        updated['stablePathInstall'] = stable_install
        updated['fromVersion'] = health['version'].split('+')[0]
    else:
        updated.pop('stablePathInstall', None)
    temporary = target.with_name(target.name + '.install-' + stamp)
    placed_tmp = placed.with_name('placed-update.install-' + stamp + '.json')
    link_temps = {p: p.with_name(p.name + '.install-' + stamp) for p in links}
    switched = False
    try:
        shutil.copy2(source, temporary)
        temporary.chmod(old_stat.st_mode & 0o7777)
        temporary_stat = temporary.stat()
        assert temporary_stat.st_uid == old_stat.st_uid and \
            (temporary_stat.st_mode & 0o7777) == (old_stat.st_mode & 0o7777), \
            'Replacement owner/mode does not match the installed binary'
        assert temporary_stat.st_mode & 0o022 == 0, 'Replacement binary is group/world writable'
        assert digest(temporary) == meta['sha256']
        verify(temporary, 'app.narrafork.selfuse')
        with placed_tmp.open('x') as stream:
            json.dump(updated, stream, indent=2)
            stream.flush()
            os.fsync(stream.fileno())
        for p, temp in link_temps.items():
            os.symlink(str(target), temp)
            assert p.is_symlink() or not p.exists(), 'CLI file changed during installation'
            assert (os.readlink(p) if p.is_symlink() else None) == links[p]
        assert placed.read_bytes() == old_placed and digest(old_binary) == old_sha, 'Concurrent installation detected'
        if prepare_update:
            current_health = read_health(url)
            assert current_health['commit'] == health['commit'] and \
                current_health['stableUpdateSignal'] == 'SIGUSR2', \
                'The running server changed while this install was prepared'
        os.replace(temporary, target)
        switched = True
        for p, temp in link_temps.items():
            os.replace(temp, p)
        os.replace(placed_tmp, placed)
        assert all(p.resolve() == target for p in links) and digest(target) == meta['sha256']
    except BaseException:
        if switched:
            if old_binary == target:
                shutil.copy2(backup / old_binary.name, temporary)
                os.replace(temporary, target)
            else:
                target.unlink()
            for p, old in links.items():
                temp = link_temps[p]
                if temp.is_symlink():
                    temp.unlink()
                if old is None:
                    if p.is_symlink():
                        p.unlink()
                else:
                    os.symlink(old, temp)
                    os.replace(temp, p)
            placed_tmp.write_bytes(old_placed)
            os.replace(placed_tmp, placed)
        raise
    finally:
        for p in [temporary, placed_tmp, *link_temps.values()]:
            if p.exists() or p.is_symlink():
                p.unlink()
    print(json.dumps(result), flush=True)


if __name__ == '__main__':
    main()
