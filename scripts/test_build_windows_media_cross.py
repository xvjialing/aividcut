"""交叉构建入口的宿主污染与错误目标边界。"""
import importlib.util
from pathlib import Path
import struct
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("cross", Path(__file__).with_name("build_windows_media_cross.py"))
cross = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cross)


class Boundaries(unittest.TestCase):
    def test_foreign_pkg_config_and_driver_flags_rejected(self):
        with tempfile.TemporaryDirectory() as folder:
            sdk = Path(folder).resolve()
            for flags in ["-I/usr/include", "-L/usr/lib", "@response", "-Wl,-rpath,/tmp", "-specs=other", "-Irelative"]:
                with self.subTest(flags=flags), self.assertRaises(ValueError):
                    cross.checked_flags(flags, sdk)
            self.assertEqual(cross.checked_flags(f'-I"{sdk}" -DQT_CORE_LIB -lQt6Core', sdk),
                             ["-I" + str(sdk), "-DQT_CORE_LIB", "-lQt6Core"])

    def test_symlink_escape_rejected(self):
        with tempfile.TemporaryDirectory() as folder:
            sdk = Path(folder).resolve()
            (sdk / "host").symlink_to("/usr/lib")
            with self.assertRaises(ValueError):
                cross.checked_flags(f"-I{sdk}/host", sdk)

    def test_target_header_rejects_host_and_arm64(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "worker.exe"
            for machine, valid in [(b"\x64\x86", True), (b"\x64\xaa", False)]:
                header = bytearray(64)
                header[:2] = b"MZ"
                struct.pack_into("<I", header, 60, 64)
                path.write_bytes(header + b"PE\0\0" + machine)
                if valid:
                    cross.pe_amd64(path)
                else:
                    with self.assertRaises(ValueError):
                        cross.pe_amd64(path)
            path.write_bytes(b"\xcf\xfa\xed\xfe" + bytes(64))
            with self.assertRaises(ValueError):
                cross.pe_amd64(path)

    def test_sources_follow_cmake_and_fail_on_new_syntax(self):
        self.assertEqual(len(cross.source_files(cross.PROJECT)), 6)
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            directory = root / "native/mlt-worker"
            directory.mkdir(parents=True)
            (directory / "CMakeLists.txt").write_text("add_executable(aividcut-mlt-worker ${SOURCES})")
            with self.assertRaises(ValueError):
                cross.source_files(root)


if __name__ == "__main__":
    unittest.main()
