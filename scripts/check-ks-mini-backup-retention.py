"""Small retention self-check; operates only inside an automatically removed temp directory."""
import runpy
import sys
import tempfile
from pathlib import Path

installer = sys.argv[1] if len(sys.argv) == 2 else 'install-ks-mini.py'
assert len(sys.argv) <= 2 and installer in ('install-ks-mini.py', 'install-local.py')
module = runpy.run_path(str(Path(__file__).with_name(installer)))
prune = module['prune_previous_backups']
required = ('narrafork-server', 'narrafork.db', 'placed-update.json', 'install-record.json')
required += (('launcher-target.txt', 'launch-agent.plist', 'narrafork-launchd-supervisor')
             if installer == 'install-local.py' else ('links.json',))

with tempfile.TemporaryDirectory(prefix='nf-backup-retention-') as directory:
    state = Path(directory)
    current = state / 'backup-before-stable-signing-20260921T120000123456Z'
    current.mkdir()
    old = state / 'backup-before-stable-signing-20260920T120000123456Z'
    legacy = state / 'backup-before-stable-signing-20260921T120000Z'
    newer = state / 'backup-before-stable-signing-20260921T120001000000Z'
    versioned = state / 'backup-before-0.7.3-20260914T071818Z'
    versioned_newer = state / 'backup-before-0.7.7-20260922T120000Z'
    special = state / 'KNOWN-GOOD-selfuse'
    labelled = state / 'backup-before-stable-signing-manual-keep'
    for path in (old, legacy, newer, versioned, versioned_newer, special, labelled):
        path.mkdir()
        (path / 'keep.txt').write_text('preserve outside the old automatic backups')
    link = state / 'backup-before-stable-signing-20260919T120000000000Z'
    link.symlink_to(special, target_is_directory=True)
    (old / 'external-link').symlink_to(special, target_is_directory=True)

    # An incomplete current backup must not authorize any deletion.
    try:
        prune(state, current)
    except AssertionError:
        pass
    else:
        raise AssertionError('Incomplete backup was accepted')
    assert old.is_dir() and legacy.is_dir() and versioned.is_dir()

    for name in required:
        (current / name).write_text('verified by the installer before this helper is called')
    result = prune(state, current)
    assert result == {'removed': 3, 'errorCount': 0, 'errors': []}, result
    assert not old.exists() and not legacy.exists() and not versioned.exists()
    assert all(path.is_dir() for path in (current, newer, versioned_newer, special, labelled))
    assert (special / 'keep.txt').is_file() and link.is_symlink()
    assert prune(state, current)['removed'] == 0

    # Failure deleting one backup is reported without undoing the successful install.
    blocked = state / 'backup-before-stable-signing-20260918T120000000000Z'
    blocked.mkdir()
    original = module['shutil'].rmtree

    def fail_one(path, *args, **kwargs):
        if Path(path) == blocked:
            raise PermissionError('simulated cleanup denial')
        return original(path, *args, **kwargs)

    module['shutil'].rmtree = fail_one
    try:
        result = prune(state, current)
        assert result['errorCount'] == 1 and result['removed'] == 0, result
        assert blocked.is_dir() and current.is_dir()
    finally:
        module['shutil'].rmtree = original

print(f'{installer} backup retention self-check passed')
