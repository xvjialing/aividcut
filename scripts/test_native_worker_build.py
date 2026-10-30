#!/usr/bin/env python3
"""原生构建入口的隔离夹具：不编译/安装SDK，不运行真实媒体进程。"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


MODULES = ("core", "avformat", "qt6", "xml", "plus", "normalize", "resample")


class NativeBuildTest(unittest.TestCase):
    def setUp(self):
        if os.name == "nt":
            self.skipTest("POSIX开发脚本夹具；Windows原生SDK仍需实机验证")
        self.pkg = shutil.which("pkg-config")
        if not self.pkg:
            self.skipTest("需要构建前置工具pkg-config；未执行脚本验收")
        self.temporary = tempfile.TemporaryDirectory(prefix="aividcut native build ")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        (self.root / "scripts").mkdir()
        source = Path(__file__).resolve().parent
        for name in ("native_sdk_env.sh", "build_native_worker.sh", "prepare_native_repository.sh"):
            shutil.copy2(source / name, self.root / "scripts" / name)
        self.sdk = self.make_sdk("SDK one")
        self.cmake = self.root / "fake cmake"
        self.cmake.write_text("""#!/usr/bin/env python3
import json, os, pathlib, sys
root=pathlib.Path(__file__).parent
with (root/'cmake-calls.jsonl').open('a') as log:
    log.write(json.dumps(sys.argv[1:])+'\\n')
if '--help' in sys.argv:
    print('--fresh')
elif '--build' in sys.argv:
    if os.environ.get('AIVIDCUT_FIXTURE_BUILD_FAIL')=='1':
        sys.exit(7)
    target=root/'work/bin/aividcut-mlt-worker'
    target.parent.mkdir(parents=True,exist_ok=True)
    target.write_text('#!/bin/sh\\nexit 0\\n')
    target.chmod(0o755)
""", encoding="utf-8")
        self.cmake.chmod(0o755)

    def make_sdk(self, name):
        sdk = self.root / name
        pc = sdk / "lib/pkgconfig"
        pc.mkdir(parents=True)
        modules = sdk / "lib/mlt"
        modules.mkdir()
        for module in MODULES:
            (modules / ("libmlt" + module + ".so")).write_text("fixture module\n")
        (sdk / "share/mlt/profiles").mkdir(parents=True)
        (sdk / "share/mlt/profiles/dv_pal").write_text("fixture profile\n")
        for package, version in (("mlt-framework-7", "7.42.0"), ("libavformat", "63.1.102"), ("libavcodec", "63.1.102"), ("libavutil", "61.1.102"), ("libavfilter", "12.1.102"), ("libswresample", "7.1.102"), ("libswscale", "10.1.102")):
            (pc / (package + ".pc")).write_text(
                f"prefix={sdk}\nmoduledir=${{prefix}}/lib/mlt\nmltdatadir=${{prefix}}/share/mlt\n"
                f"Name: {package}\nDescription: isolated fixture\nVersion: {version}\nLibs:\nCflags:\n",
                encoding="utf-8",
            )
        return sdk

    def run_build(self, **overrides):
        environment = {key: value for key, value in os.environ.items() if not key.startswith("AIVIDCUT_") and key not in ("PKG_CONFIG_PATH", "CMAKE_PREFIX_PATH")}
        environment.update(AIVIDCUT_MLT_SDK=str(self.sdk), AIVIDCUT_PKG_CONFIG=self.pkg, AIVIDCUT_CMAKE=str(self.cmake))
        environment.update(overrides)
        return subprocess.run(["sh", str(self.root / "scripts/build_native_worker.sh")], env=environment, cwd=self.root, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10)

    def assert_ready(self, sdk):
        self.assertTrue((self.root / "work/bin/.aividcut-native-worker-built").is_file())
        self.assertTrue(os.access(self.root / "work/bin/aividcut-mlt-worker", os.X_OK))
        repository = self.root / "work/mlt-selected-repository"
        self.assertEqual(len(list(repository.iterdir())), len(MODULES))
        for module in MODULES:
            path = repository / ("libmlt" + module + ".so")
            self.assertTrue(path.is_symlink())
            self.assertEqual(path.resolve(), sdk / "lib/mlt" / path.name)
        self.assertTrue((self.root / "work/mlt-profiles").is_symlink())
        self.assertEqual((self.root / "work/mlt-profiles").resolve(), sdk / "share/mlt/profiles")

    def test_build_space_paths_and_switch_sdk(self):
        result = self.run_build()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_ready(self.sdk)
        second = self.make_sdk("SDK two")
        result = self.run_build(AIVIDCUT_MLT_SDK=str(second))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_ready(second)
        calls = [json.loads(row) for row in (self.root / "cmake-calls.jsonl").read_text().splitlines()]
        self.assertTrue(any("--fresh" in call for call in calls))
        self.assertFalse(any("--install" in call for call in calls))

    def test_no_sdk_or_failed_build_never_leaves_success_marker(self):
        result = self.run_build(AIVIDCUT_MLT_SDK="")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("显式设置", result.stderr)
        self.assertFalse((self.root / "cmake-calls.jsonl").exists())
        self.assertEqual(self.run_build().returncode, 0)
        result = self.run_build(AIVIDCUT_FIXTURE_BUILD_FAIL="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.root / "work/bin/.aividcut-native-worker-built").exists())

    def test_unknown_repository_contents_are_preserved(self):
        repository = self.root / "work/mlt-selected-repository"
        repository.mkdir(parents=True)
        note = repository / "user-note.txt"
        note.write_text("不能覆盖的内容")
        result = self.run_build()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(note.read_text(), "不能覆盖的内容")
        self.assertIn("未知内容", result.stderr)
        self.assertFalse((self.root / "work/bin/.aividcut-native-worker-built").exists())

    def test_missing_sdk_module_keeps_previous_repository(self):
        self.assertEqual(self.run_build().returncode, 0)
        original = self.sdk
        second = self.make_sdk("SDK incomplete")
        (second / "lib/mlt/libmltqt6.so").unlink()
        result = self.run_build(AIVIDCUT_MLT_SDK=str(second))
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.root / "work/mlt-selected-repository/libmltcore.so").resolve(), original / "lib/mlt/libmltcore.so")
        self.assertEqual((self.root / "work/mlt-profiles").resolve(), original / "share/mlt/profiles")
        self.assertFalse((self.root / "work/bin/.aividcut-native-worker-built").exists())


if __name__ == "__main__":
    unittest.main(verbosity=2)
