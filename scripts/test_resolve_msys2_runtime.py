"""锁定快照/归档边界回归，不执行SDK或访问网络。"""
from pathlib import Path
import hashlib
import importlib.util
import io
import json
from copy import deepcopy
import tarfile
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('resolver', Path(__file__).with_name('resolve_msys2_runtime.py'))
resolver = importlib.util.module_from_spec(spec)
spec.loader.exec_module(resolver)


def archive(path, entries):
    with tarfile.open(path, 'w:gz') as output:
        for name, contents in entries.items():
            data = contents.encode() if isinstance(contents, str) else contents
            info = tarfile.TarInfo(name)
            info.size = len(data)
            output.addfile(info, io.BytesIO(data))


def metadata(name, version='1'):
    filename = name + '-' + version + '-any.pkg.tar.gz'
    return '\n'.join(['%NAME%', name, '', '%VERSION%', version, '', '%FILENAME%', filename,
                      '', '%SHA256SUM%', 'a' * 64, '', '%CSIZE%', '100', ''])


class ResolverTests(unittest.TestCase):
    def test_development_profile_excludes_runtime_and_tools(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            cache, lock = self.make_lock(root, {'ucrt64/include/api.h': b'header',
                                               'ucrt64/lib/api.dll.a': b'import',
                                               'ucrt64/lib/pkgconfig/api.pc': b'pkg',
                                               'ucrt64/share/qt6/mkspecs/common/qplatformdefs.h': b'header',
                                               'ucrt64/bin/api.dll': b'runtime',
                                               'ucrt64/bin/moc.exe': b'tool', '.INSTALL': b'never'})
            original = deepcopy(lock['packages'][0])
            lock['packages'] = [dict(original, name=resolver.PREFIX + name)
                                for name in ['mlt', 'ffmpeg', 'qt6-base', 'qt6-svg']]
            result = resolver.apply_lock(lock, root / 'out', cache, '/not-called', 'development')
            self.assertEqual(result['files'], 4)
            self.assertTrue((root / 'out/ucrt64/include/api.h').is_file())
            self.assertFalse((root / 'out/ucrt64/bin').exists())
            lock['packages'].pop()
            with self.assertRaisesRegex(ValueError, '四包'):
                resolver.apply_lock(lock, root / 'other', cache, '/not-called', 'development')

    def test_provider_collision_requires_explicit_choice_and_matching_snapshot(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            a, b = resolver.PREFIX + 'a', resolver.PREFIX + 'b'
            entries = {a + '-1/desc': metadata(a), b + '-1/desc': metadata(b)}
            archive(root / 'db.gz', entries)
            archive(root / 'files.gz', {**entries, a + '-1/files': '%FILES%\nucrt64/bin/shared.dll\n',
                                      b + '-1/files': '%FILES%\nucrt64/bin/shared.dll\n'})
            lock = resolver.make_lock(root / 'db.gz', root / 'files.gz', ['shared.dll'], [], '/not-called')
            self.assertEqual(len(lock['unresolved']), 1)
            chosen = resolver.make_lock(root / 'db.gz', root / 'files.gz', ['shared.dll'], [], '/not-called', {'shared.dll': a})
            self.assertEqual(chosen['packages'][0]['name'], a)
            archive(root / 'files.gz', {a + '-1/desc': metadata(a, '2')})
            with self.assertRaisesRegex(ValueError, '快照版本不一致'):
                resolver.make_lock(root / 'db.gz', root / 'files.gz', [], [a], '/not-called')

    def make_lock(self, root, entries):
        name = resolver.PREFIX + 'test-1-any.pkg.tar.gz'
        cache = root / 'cache'
        cache.mkdir()
        archive(cache / name, entries)
        data = (cache / name).read_bytes()
        return cache, {'schemaVersion': 1, 'repository': resolver.BASE, 'unresolved': [],
                       'packages': [{'filename': name, 'sha256': hashlib.sha256(data).hexdigest(),
                                     'url': resolver.BASE + name, 'downloadBytes': len(data)}]}

    def test_replay_only_runtime_regular_files_and_no_overwrite(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            cache, lock = self.make_lock(root, {'ucrt64/bin/code.dll': b'fixture',
                                               'ucrt64/bin/ffmpeg.exe': b'fixture-exe',
                                               'ucrt64/bin/sdk-tool.exe': b'not-runtime',
                                               '.INSTALL': b'not-executed'})
            result = resolver.apply_lock(lock, root / 'out', cache, '/not-called')
            self.assertEqual(result['files'], 2)
            self.assertFalse((root / 'out/.INSTALL').exists())
            self.assertFalse((root / 'out/ucrt64/bin/sdk-tool.exe').exists())
            with self.assertRaisesRegex(ValueError, '输出已存在'):
                resolver.apply_lock(lock, root / 'out', cache, '/not-called')

    def test_tampered_cache_rejected_before_extracting(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            cache, lock = self.make_lock(root, {'ucrt64/bin/code.dll': b'fixture'})
            lock['packages'][0]['sha256'] = '0' * 64
            with self.assertRaisesRegex(ValueError, '摘要不匹配'):
                resolver.apply_lock(lock, root / 'out', cache, '/not-called')
            self.assertFalse((root / 'out').exists())

    def test_archive_paths_and_device_names_rejected(self):
        for name in ['../escape', '/absolute', 'ucrt64/bin/CON.dll', 'ucrt64/bin/a.dll ', 'C:/escape']:
            with self.subTest(name=name), self.assertRaises(ValueError):
                resolver.safe_name(name)
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            cache, lock = self.make_lock(root, {'../escape': b'bad'})
            with self.assertRaises(ValueError):
                resolver.apply_lock(lock, root / 'out', cache, '/not-called')
            self.assertFalse((root / 'escape').exists())


if __name__ == '__main__':
    unittest.main()
