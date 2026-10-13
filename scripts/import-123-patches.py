#!/usr/bin/env python3
"""Pinned, patch-only rclone import. No service or cloud mutation."""
import argparse
import base64
import ctypes
import hashlib
import json
import os
from pathlib import Path
import re
import selectors
import shutil
import signal
import stat
import subprocess
import tempfile
import time

PLATFORMS = dict(zip(
    ('linux-x64', 'linux-x64-baseline', 'linux-arm64', 'darwin-x64', 'darwin-arm64', 'win-x64', 'win-x64-baseline', 'win-arm64'),
    ('linux-x64', 'linux-x64-baseline', 'linux-arm64', 'macos-x64', 'macos-arm64', 'windows-x64.exe', 'windows-x64-baseline.exe', 'windows-arm64.exe')))
VERSION = r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)'
SHA = r'[0-9a-f]{64}'


class ImportFailure(ValueError):
    """Only fixed, credential-free diagnostics produced by this importer."""


def require(ok, message):
    if not ok:
        raise ImportFailure(message)


def safe(path):
    path = Path(os.path.abspath(path))
    for p in reversed((path, *path.parents)):
        if p.exists() or p.is_symlink():
            require(not stat.S_ISLNK(p.lstat().st_mode), 'symlink rejected')
    return path


def number(v, maximum=1024 * 1024 * 1024):
    return type(v) is int and 0 < v <= maximum


def sha512(v):
    require(isinstance(v, str) and re.fullmatch(r'[A-Za-z0-9+/]{86}==', v), 'invalid SHA512')
    require(len(base64.b64decode(v, validate=True)) == 64, 'invalid SHA512')


def load(path, limit=256 * 1024):
    safe(path)
    require(path.stat().st_size <= limit, 'JSON too large')
    def pairs(items):
        result = {}
        for k, v in items:
            require(k not in result, 'duplicate JSON key')
            result[k] = v
        return result
    return json.loads(path.read_bytes(), object_pairs_hook=pairs)


def digest(path):
    h = hashlib.sha256()
    with path.open('rb') as f:
        for chunk in iter(lambda: f.read(65536), b''):
            h.update(chunk)
    return h.hexdigest()


def download(args, name, target, maximum, deadline):
    remaining = deadline - time.monotonic()
    require(remaining > 0, 'import timeout')
    cmd = [args.rclone, 'copyto', f'{args.remote}:{args.remote_dir}/{name}', str(target),
           '--retries', '1', '--low-level-retries', '1', '--contimeout', '15s',
           '--timeout', '30s', '--max-duration', f'{max(1, int(remaining))}s']
    if args.config:
        cmd += ['--config', args.config]
    proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    streams = selectors.DefaultSelector()
    for stream in (proc.stdout, proc.stderr):
        os.set_blocking(stream.fileno(), False)
        streams.register(stream, selectors.EVENT_READ)
    count = 0
    try:
        while streams.get_map() or proc.poll() is None:
            require(time.monotonic() < deadline, 'rclone timeout')
            if target.exists():
                require(target.stat().st_size <= maximum, 'download exceeds limit')
            for key, _ in streams.select(0.02):
                data = os.read(key.fd, 8192)
                if not data:
                    streams.unregister(key.fileobj)
                count += len(data)
                require(count <= 65536, 'rclone output exceeds limit')
        require(proc.wait() == 0, 'rclone download failed (private diagnostics suppressed)')
        require(target.is_file() and not target.is_symlink() and target.stat().st_size <= maximum, 'invalid download')
    finally:
        # Kill the owned group, including any descendants holding the pipes open.
        try:
            os.killpg(proc.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        proc.wait()
        streams.close()
        proc.stdout.close()
        proc.stderr.close()


def validate_manifest(m, args):
    require(set(m) == {'schemaVersion', 'repository', 'version', 'tag', 'commit', 'channel', 'buildRunId', 'buildRunAttempt', 'bridgeArtifactId', 'bridgeArtifactSha256', 'remoteDirectory', 'files', 'release'}, 'invalid manifest fields')
    require(type(m['schemaVersion']) is int and m['schemaVersion'] == 1 and m['repository'] == 'NarraFork/NarraFork' and m['version'] == args.version and m['tag'] == 'v' + args.version and m['channel'] == 'stable', 'manifest identity mismatch')
    require(re.fullmatch(r'[0-9a-f]{40}', m['commit']) and re.fullmatch(SHA, m['bridgeArtifactSha256']), 'invalid source identity')
    require(number(m['buildRunId'], 2**53-1) and number(m['bridgeArtifactId'], 2**53-1) and type(m['buildRunAttempt']) is int and m['buildRunAttempt'] == 1, 'invalid build identity')
    expected_remote = f"narrafork-ci-relay/NarraFork-NarraFork/v{args.version}/{m['buildRunId']}-1"
    require(args.remote_dir == expected_remote and m['remoteDirectory'] in (expected_remote, expected_remote + '/'), 'remote directory mismatch')
    r = m['release']
    require(set(r) == {'version', 'channel', 'releaseDate', 'releaseNotes', 'platforms'} and r['version'] == args.version and r['channel'] == 'stable', 'release mismatch')
    require(isinstance(r['releaseDate'], str) and set(r['releaseNotes']) == {'en', 'zh-CN'} and all(isinstance(v, str) for v in r['releaseNotes'].values()), 'invalid release notes')
    require(set(r['platforms']) == set(PLATFORMS), 'eight platforms required')
    allowed = {'meta.json': ('release-metadata', None)}
    for platform, suffix in PLATFORMS.items():
        p = r['platforms'][platform]
        require(set(p) == {'filename', 'size', 'sha512', 'hasZstdPatch', 'zstdPatchFromVersion', 'zstdPatchFromVersions'}, 'invalid platform fields')
        require(p['filename'] == f'narrafork-{args.version}-{suffix}' and number(p['size']) and p['hasZstdPatch'] is True, 'invalid platform identity')
        sha512(p['sha512'])
        bases = p['zstdPatchFromVersions']
        require(isinstance(bases, list) and 1 <= len(bases) <= 16 and len(set(bases)) == len(bases) and p['zstdPatchFromVersion'] in bases, 'invalid bases')
        for base in bases:
            require(re.fullmatch(VERSION, base) and tuple(map(int, base.split('.'))) < tuple(map(int, args.version.split('.'))), 'invalid base version')
            name = f"{p['filename']}.from-{base}.zstd-patch"
            allowed[name] = ('patch', platform)
            allowed[name + '.meta.json'] = ('patch-metadata', platform)
        allowed['' + p['filename'] + '.metadata.json'] = ('binary-metadata', platform)
    files = m['files']
    require(isinstance(files, list) and len(files) <= 273, 'invalid file list')
    seen = set()
    for f in files:
        require(set(f) == {'name', 'role', 'platform', 'size', 'sha256'}, 'invalid file fields')
        name = f['name']
        require(name in allowed and name not in seen and allowed[name] == (f['role'], f['platform']), 'unapproved or duplicate file')
        require(number(f['size'], 512*1024*1024 if f['role'] == 'patch' else 256*1024) and re.fullmatch(SHA, f['sha256']), 'invalid file identity')
        seen.add(name)
    require(all(name in seen for name, (role, _) in allowed.items() if role != 'binary-metadata'), 'missing patch pair')
    return files


def relative(f):
    return Path(f['platform']) / f['name'] if f['platform'] else Path(f['name'])


def verify(root, m, releases):
    expected = {str(relative(f)) for f in m['files']}
    actual = set()
    for parent, dirs, files in os.walk(root, followlinks=False):
        for name in dirs + files:
            p = safe(Path(parent) / name)
            if p.is_file():
                actual.add(str(p.relative_to(root)))
            else:
                require(p.is_dir() and str(p.relative_to(root)) in PLATFORMS, 'unexpected destination entry')
    require(actual == expected, 'destination file set mismatch')
    for f in m['files']:
        p = root / relative(f)
        require(p.stat().st_size == f['size'] and digest(p) == f['sha256'], 'file hash or size mismatch')
        if f['role'] == 'release-metadata':
            require(load(p) == m['release'], 'release metadata bytes mismatch')
        if f['role'] == 'binary-metadata':
            sidecar = load(p)
            identity = m['release']['platforms'][f['platform']]
            fields = {'name', 'platform', 'target', 'version', 'commit', 'buildDate', 'size', 'sha256', 'sha512'}
            require(fields <= set(sidecar) <= fields | {'repository', 'helperDistribution'}, 'invalid binary metadata fields')
            require(sidecar['name'] == identity['filename'] and sidecar['platform'] == f['platform'] and sidecar['version'] == m['version'] and sidecar['size'] == identity['size'] and sidecar['sha512'] == identity['sha512'], 'binary metadata identity mismatch')
            require(re.fullmatch(r'[0-9a-f]{7,40}', sidecar['commit']) and m['commit'].startswith(sidecar['commit']) and re.fullmatch(SHA, sidecar['sha256']) and sidecar.get('repository', m['repository']) == m['repository'], 'binary metadata provenance mismatch')
        if f['role'] == 'patch-metadata':
            meta = load(p)
            required = {'fromVersion', 'toVersion', 'oldFileSize', 'oldFileSha512', 'stableEnd', 'newTailSize', 'patchSize', 'newFileSize', 'newFileSha512'}
            require(required <= set(meta) <= required | {'mode'}, 'invalid patch metadata fields')
            platform = f['platform']
            new = m['release']['platforms'][platform]
            base = meta['fromVersion']
            require(base in new['zstdPatchFromVersions'] and meta['toVersion'] == m['version'] and f['name'] == f"{new['filename']}.from-{base}.zstd-patch.meta.json", 'patch version mismatch')
            old_release = load(releases / base / 'meta.json')
            require(old_release.get('version') == base, 'old release version mismatch')
            old = old_release['platforms'][platform]
            for prefix, identity in [('old', old), ('new', new)]:
                sha512(meta[prefix + 'FileSha512'])
                require(number(meta[prefix + 'FileSize']) and meta[prefix + 'FileSize'] == identity['size'] and meta[prefix + 'FileSha512'] == identity['sha512'], 'patch base or target identity mismatch')
            require(meta.get('mode', 'dictionary') in ('dictionary', 'patch-from') and type(meta['stableEnd']) is int and 0 <= meta['stableEnd'] <= min(meta['oldFileSize'], meta['newFileSize']) and type(meta['newTailSize']) is int and meta['newTailSize'] == meta['newFileSize'] - meta['stableEnd'], 'invalid patch geometry')
            require(number(meta['patchSize'], 512*1024*1024) and meta['patchSize'] == p.with_name(p.name[:-10]).stat().st_size, 'patch size mismatch')


def publish(stage, destination):
    libc = ctypes.CDLL(None, use_errno=True)
    require(hasattr(libc, 'renameat2'), 'atomic no-replace publication unavailable')
    result = libc.renameat2(-100, os.fsencode(stage), -100, os.fsencode(destination), 1)
    require(result == 0, 'atomic publication refused')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for flag in ('version', 'remote-dir', 'manifest-sha256', 'data-dir', 'receipt'):
        parser.add_argument('--' + flag, required=True)
    parser.add_argument('--remote', default='nf_update')
    parser.add_argument('--config')
    parser.add_argument('--rclone', default='rclone')
    parser.add_argument('--timeout-seconds', type=float, default=180)
    args = parser.parse_args()
    stage = None
    receipt = None
    try:
        receipt_candidate = safe(args.receipt)
        require(re.fullmatch(VERSION, args.version) and re.fullmatch(SHA, args.manifest_sha256) and re.fullmatch(r'[A-Za-z0-9_-]+', args.remote), 'invalid CLI identity')
        require(0 < args.timeout_seconds <= 3600 and re.fullmatch(r'narrafork-ci-relay/NarraFork-NarraFork/v' + re.escape(args.version) + r'/[1-9][0-9]*-1', args.remote_dir), 'invalid CLI limits or remote path')
        if args.config:
            safe(args.config)
        releases = safe(Path(args.data_dir) / 'products/narrafork/releases')
        require(receipt_candidate != releases and releases not in receipt_candidate.parents and not receipt_candidate.exists(), 'unsafe receipt destination')
        require(receipt_candidate.parent.is_dir(), 'receipt parent missing')
        receipt = receipt_candidate
        releases.mkdir(parents=True, exist_ok=True)
        stage = Path(tempfile.mkdtemp(prefix='.patch-import-', dir=releases))
        require(stage.stat().st_uid == os.getuid() and stat.S_IMODE(stage.stat().st_mode) == 0o700, 'unsafe staging')
        deadline = time.monotonic() + args.timeout_seconds
        manifest_path = stage / 'import-manifest.json'
        download(args, 'import-manifest.json', manifest_path, 256*1024, deadline)
        require(digest(manifest_path) == args.manifest_sha256, 'manifest hash mismatch')
        m = load(manifest_path)
        files = validate_manifest(m, args)
        manifest_path.unlink()
        destination = safe(releases / args.version)
        if destination.exists():
            verify(destination, m, releases)
        else:
            for f in files:
                p = stage / relative(f)
                p.parent.mkdir(exist_ok=True)
                download(args, f['name'], p, f['size'], deadline)
            verify(stage, m, releases)
            publish(stage, destination)
            stage = None
        result = dict(success=True, patchOnly=True, binariesDownloaded=0, version=args.version, files=files,
                      repository=m['repository'], tag=m['tag'], channel=m['channel'], remoteDirectory=m['remoteDirectory'], commit=m['commit'], buildRunId=m['buildRunId'], buildRunAttempt=m['buildRunAttempt'], bridgeArtifactId=m['bridgeArtifactId'], bridgeArtifactSha256=m['bridgeArtifactSha256'], manifestSha256=args.manifest_sha256)
    except (Exception, KeyboardInterrupt) as exc:
        # Never include external exception text: it may contain a credential/path.
        result = dict(success=False, patchOnly=True, binariesDownloaded=0, error=type(exc).__name__,
                      diagnostic=str(exc)[:160] if isinstance(exc, ImportFailure) else 'import failed; external diagnostics suppressed')
    finally:
        if stage is not None:
            shutil.rmtree(stage)
    if receipt is not None:
        safe(receipt.parent)
        fd = os.open(receipt, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'w') as out:
            json.dump(result, out)
    return 0 if result['success'] else 1


if __name__ == '__main__':
    def cancelled(_signum, _frame):
        raise KeyboardInterrupt()
    signal.signal(signal.SIGTERM, cancelled)
    raise SystemExit(main())
