"""Install an uploaded signed selfuse bundle on ks-mini; restart is a separate command.

Usage: python3 install-ks-mini.py <bundle-directory> [--check]
The bundle contains deploy.json and its named artifact, never signing private keys.
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
from contextlib import closing
from pathlib import Path


def digest(path):
    result = hashlib.sha256()
    with path.open('rb') as stream:
        for data in iter(lambda: stream.read(1024 * 1024), b''):
            result.update(data)
    return result.hexdigest()


def main():
    if not __debug__:
        raise RuntimeError('Run without Python -O: installation checks are required')
    assert len(sys.argv) in (2, 3) and (len(sys.argv) == 2 or sys.argv[2] == '--check'), __doc__
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
    requirement = f'=identifier "app.narrafork.selfuse" and certificate leaf = H"{fingerprint}"'
    def verify(path):
        subprocess.run(['/usr/bin/codesign', '--verify', '--strict', '-R', requirement, str(path)], check=True, timeout=30)
    verify(source)
    migrations = config['migrations']
    assert isinstance(migrations, list) and 0 < len(migrations) < 1000
    assert all(re.fullmatch(r'[0-9a-f]{64}', item['sha256']) for item in migrations)
    plist = home / 'Library/LaunchAgents/com.narrafork.server.plist'
    args = plistlib.loads(plist.read_bytes())['ProgramArguments']
    entry = bin_dir / 'narrafork-0.6.4-macos-arm64'
    assert args == [str(entry)] and entry.is_symlink()
    target, cli = bin_dir / 'narrafork-server', bin_dir / 'narrafork'
    assert not target.is_symlink()
    assert cli.is_symlink() or not cli.exists(), 'Do not overwrite an unexpected regular CLI file'
    old_binary = entry.resolve()
    assert old_binary.is_file() and old_binary.parent == bin_dir
    assert not target.exists() or old_binary == target, 'Unexpected pre-existing stable binary'
    links = {entry: os.readlink(entry), cli: os.readlink(cli) if cli.is_symlink() else None}
    placed = state / 'updates/placed-update.json'
    old_placed = placed.read_bytes()
    old_data = json.loads(old_placed)
    assert Path(old_data['newBinaryPath']).resolve() == old_binary
    if len(sys.argv) == 3:
        print(json.dumps({'preflight': 'ok', 'oldBinary': str(old_binary), 'target': str(target), 'sha256': meta['sha256'], 'commit': meta['commit']}))
        return

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
    with closing(sqlite3.connect((state / 'narrafork.db').as_uri() + '?mode=ro', uri=True, timeout=0.1)) as src:
        with closing(sqlite3.connect(backup / 'narrafork.db')) as dst:
            src.backup(dst, pages=512, progress=progress, sleep=0.01)
            dst.execute('PRAGMA wal_checkpoint(TRUNCATE)')
            dst.execute('PRAGMA journal_mode=DELETE')
            dst.set_progress_handler(lambda: int(time.monotonic() - start > 240), 10000)
            assert dst.execute('PRAGMA quick_check').fetchall() == [('ok',)]
            hashes = {r[0] for r in dst.execute('SELECT hash FROM __drizzle_migrations LIMIT 10000')}
            pending = [m['name'] for m in migrations if m['sha256'] not in hashes]
            assert not pending, ('Validate pending migrations before installing', pending)
    result = {'backup': str(backup), 'installed': str(target), 'sha256': meta['sha256'],
              'version': meta['version'], 'commit': meta['commit'], 'oldBinary': str(old_binary),
              'oldSha256': old_sha, 'migrationCountBefore': len(hashes), 'pendingMigrations': pending,
              'signingIdentifier': 'app.narrafork.selfuse', 'restarted': False}
    (backup / 'install-record.json').write_text(json.dumps(result, indent=2))
    updated = dict(old_data)
    updated.update(version=meta['version'], fromVersion=old_data['version'], fileName=meta['name'],
                   newBinaryPath=str(target), updatePath=str(target), placed=True,
                   placedAt=datetime.datetime.now(datetime.timezone.utc).isoformat(),
                   sizeBytes=meta['size'], sha512=meta['sha512'])
    temporary = target.with_name(target.name + '.install-' + stamp)
    placed_tmp = placed.with_name('placed-update.install-' + stamp + '.json')
    link_temps = {p: p.with_name(p.name + '.install-' + stamp) for p in links}
    switched = False
    try:
        shutil.copy2(source, temporary)
        temporary.chmod(0o755)
        assert digest(temporary) == meta['sha256']
        verify(temporary)
        with placed_tmp.open('x') as stream:
            json.dump(updated, stream, indent=2)
            stream.flush()
            os.fsync(stream.fileno())
        for p, temp in link_temps.items():
            os.symlink(str(target), temp)
            assert p.is_symlink() or not p.exists(), 'CLI file changed during installation'
            assert (os.readlink(p) if p.is_symlink() else None) == links[p]
        assert placed.read_bytes() == old_placed and digest(old_binary) == old_sha, 'Concurrent installation detected'
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
