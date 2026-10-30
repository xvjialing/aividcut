"""装配边界回归，真正Qt/媒体链路另外保留实际运行证据。"""
import importlib.util
from pathlib import Path
import platform
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('package_media', Path(__file__).with_name('package_macos_media.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


@unittest.skipUnless(module.sys.platform == 'darwin', 'Apple装配需本机工具')
class PackageBoundaryTests(unittest.TestCase):
    def test_existing_output_keeps_user_contents(self):
        with tempfile.TemporaryDirectory() as folder:
            output = Path(folder) / 'already-there'
            output.mkdir()
            sentinel = output / 'keep.txt'
            sentinel.write_text('保留')
            c = dict.fromkeys(['worker', 'ffmpeg', 'ffprobe', 'repository', 'profiles', 'data',
                              'macdeployqt', 'manifestTool', 'id', 'version'], '/not-used')
            c.update(output=str(output), arch={'arm64': 'arm64', 'x86_64': 'amd64'}[platform.machine()],
                     qtPlatformPlugins=[], libraryPaths=[])
            with self.assertRaisesRegex(ValueError, '输出已存在'):
                module.package(c)
            self.assertEqual(sentinel.read_text(), '保留')

    def test_external_symlink_is_rejected_before_mutation(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder) / 'sdk'
            root.mkdir()
            outside = Path(folder) / 'source'
            outside.write_bytes(b'original')
            (root / 'alias').symlink_to(outside)
            with self.assertRaisesRegex(ValueError, '外部链接'):
                list(module.macho_images(root))
            self.assertEqual(outside.read_bytes(), b'original')


if __name__ == '__main__':
    unittest.main()
