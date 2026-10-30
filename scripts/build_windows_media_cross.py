#!/usr/bin/env python3
"""使用明确的 MinGW UCRT 工具链交叉构建；不下载软件、不执行目标 PE。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import struct
import subprocess
import sys

PROJECT = Path(__file__).resolve().parents[1]
MODULES = ["mlt-framework-7", "libavformat", "libavcodec", "libavutil",
           "libavfilter", "libswresample", "libswscale", "Qt6Widgets"]


def digest(path):
    hasher = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1048576), b""):
            hasher.update(block)
    return hasher.hexdigest()


def source_files(project):
    directory = project / "native/mlt-worker"
    content = (directory / "CMakeLists.txt").read_text()
    match = re.search(r"add_executable\(aividcut-mlt-worker\s+([^)]*)\)", content)
    if not match:
        raise ValueError("无法读取 CMake worker 源文件列表")
    names = match.group(1).split()
    if not names or any(not re.fullmatch(r"[a-z_]+\.cpp", name) for name in names):
        raise ValueError("CMake 源列表格式变化，请更新交叉构建入口")
    return [(directory / name).resolve(strict=True) for name in names]


def checked_flags(output, sdk):
    """阻止错误的宿主 pkg-config 路径或额外驱动参数进入 Windows 链接。"""
    flags = shlex.split(output)
    if not flags:
        raise ValueError("SDK 没有输出编译或链接参数")
    for flag in flags:
        if flag.startswith(("-I", "-L")):
            path = Path(flag[2:])
            try:
                valid = path.is_absolute() and path.resolve(strict=True).is_relative_to(sdk)
            except OSError:
                valid = False
            if not valid:
                raise ValueError(f"pkg-config 路径超出明确 SDK：{flag}")
        elif not re.fullmatch(r"-(?:D[A-Za-z_][A-Za-z_0-9]*(?:=[A-Za-z_0-9]+)?|l[A-Za-z_0-9.+-]+|pthread)", flag):
            raise ValueError(f"未支持的 pkg-config 参数：{flag}")
    return flags


def pe_amd64(path):
    with path.open("rb") as stream:
        header = stream.read(64)
        if len(header) != 64 or header[:2] != b"MZ":
            raise ValueError("编译产物不是 PE")
        offset = struct.unpack_from("<I", header, 60)[0]
        if offset > 1048576:
            raise ValueError("PE 头偏移无效")
        stream.seek(offset)
        if stream.read(6) != b"PE\0\0\x64\x86":
            raise ValueError("编译产物不是 Windows amd64")


def package_config(runtime, worker, output):
    plugins = [{"source": str(runtime / "share/qt6/plugins/platforms" / name),
                "destination": "plugins/platforms/" + name}
               for name in ["qoffscreen.dll", "qwindows.dll"]]
    plugins += [{"source": str(path), "destination": "plugins/imageformats/" + path.name}
                for path in sorted((runtime / "share/qt6/plugins/imageformats").glob("*.dll"))]
    if len(plugins) == 2:
        raise ValueError("运行库缺少 Qt 图像插件")
    config = {
        "id": "aividcut-media-windows", "version": "development-cross", "arch": "amd64",
        "worker": str(worker), "ffmpeg": str(runtime / "bin/ffmpeg.exe"),
        "ffprobe": str(runtime / "bin/ffprobe.exe"), "output": str(output),
        "modules": [str(runtime / f"lib/mlt/libmlt{name}.dll") for name in
                    ["core", "avformat", "qt6", "xml", "plus", "normalize", "resample"]],
        "plugins": plugins, "dllDirectories": [str(runtime / "bin")],
        "systemDlls": json.loads((PROJECT / "build/windows-system-dlls.json").read_text()),
        "data": str(runtime / "share/mlt"),
    }
    for path in [config["ffmpeg"], config["ffprobe"], *config["modules"],
                 *(plugin["source"] for plugin in plugins)]:
        if not Path(path).is_file():
            raise ValueError(f"运行库输入不存在：{path}")
    if not Path(config["data"]).is_dir():
        raise ValueError("缺少 MLT 数据目录")
    return config


def build(args):
    compiler = args.compiler.resolve(strict=True)
    pkg_config = args.pkg_config.resolve(strict=True)
    sysroot = args.sysroot.resolve(strict=True)
    sdk = args.sdk_root.resolve(strict=True)
    runtime = args.runtime_sdk_root.resolve(strict=True)
    directory = args.build_directory.absolute()
    output = args.package_output.absolute()
    if directory.exists() or directory.is_symlink() or output.exists() or output.is_symlink():
        raise ValueError("构建目录及装配输出必须不存在，未覆盖")
    if output == directory or directory in output.parents or output in directory.parents:
        raise ValueError("构建目录和装配输出不能互相包含")
    if not output.parent.is_dir():
        raise ValueError("请先创建装配输出的父目录；装配输出自身必须不存在")
    if sdk.name != "ucrt64" or runtime.name != "ucrt64":
        raise ValueError("此入口需要明确的 ucrt64 开发和运行前缀")
    sources = source_files(PROJECT)
    config = package_config(runtime, directory / "aividcut-mlt-worker.exe", output)
    directory.mkdir(parents=True, exist_ok=False)
    (directory / "tmp").mkdir()
    # 不继承登录凭据、宿主 include/library 和动态加载器环境。
    environment = {"PATH": os.pathsep.join([str(compiler.parent), "/usr/bin", "/bin"]),
                   "HOME": str(directory), "TMPDIR": str(directory / "tmp"),
                   "LC_ALL": "C", "PKG_CONFIG_LIBDIR": str(sdk / "lib/pkgconfig"),
                   "PKG_CONFIG_PATH": str(sdk / "lib/pkgconfig"),
                   "PKG_CONFIG_SYSROOT_DIR": str(sdk.parent),
                   "PKG_CONFIG_DISABLE_UNINSTALLED": "1", "SOURCE_DATE_EPOCH": "1793059200"}
    commands = []

    def run(name, command, timeout=60):
        commands.append({"name": name, "argv": command})
        (directory / "commands.json").write_text(json.dumps(commands, ensure_ascii=False, indent=2))
        result = subprocess.run(command, env=environment, cwd=PROJECT, timeout=timeout,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        (directory / f"{name}.log").write_bytes(result.stdout)
        if result.returncode:
            raise ValueError(f"{name} 失败，返回 {result.returncode}；保留日志 {directory}")
        return result.stdout.decode("utf-8", errors="replace").strip()

    triple = run("target", [str(compiler), "-dumpmachine"])
    if triple != "x86_64-w64-mingw32":
        raise ValueError("需要 x86_64-w64-mingw32 编译器")
    version = run("compiler-version", [str(compiler), "--version"])
    versions = run("sdk-versions", [str(pkg_config), "--modversion", *MODULES])
    cflags = checked_flags(run("cflags", [str(pkg_config), "--cflags", *MODULES]), sdk)
    libraries = checked_flags(run("libraries", [str(pkg_config), "--libs", *MODULES]), sdk)
    probe = directory / "ucrt-probe.cpp"
    probe.write_text('#include <crtdefs.h>\n#ifndef _UCRT\n#error UCRT required\n#endif\n')
    common = [str(compiler), "--sysroot=" + str(sysroot), "-std=c++17"]
    run("ucrt-probe", [*common, "-fsyntax-only", str(probe)])
    worker = directory / "aividcut-mlt-worker.exe"
    run("build", [*common, "-O2", "-s", "-DNDEBUG", "-DAIVIDCUT_QT_METRICS=1",
                  "-D_CRT_SECURE_NO_WARNINGS", "-Wall", "-Wextra", "-Wpedantic",
                  "-ffile-prefix-map=" + str(PROJECT) + "=.", "-Wl,--no-insert-timestamp",
                  *cflags, *(str(path) for path in sources), *libraries, "-o", str(worker)], 900)
    pe_amd64(worker)
    (directory / "windows-runtime-config.json").write_text(json.dumps(config, ensure_ascii=False, indent=2))
    report = {"schemaVersion": 1, "target": triple, "ucrtProbe": True, "qtMetrics": True,
              "compiler": {"path": str(compiler), "sha256": digest(compiler), "version": version},
              "sdkVersions": versions.splitlines(),
              "sources": {str(path.relative_to(PROJECT)): digest(path) for path in sources},
              "worker": {"sha256": digest(worker), "bytes": worker.stat().st_size},
              "windowsExecutionVerified": False, "releaseVerified": False}
    (directory / "cross-build-report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))
    print(json.dumps({"buildDirectory": str(directory), "worker": str(worker),
                      "config": str(directory / "windows-runtime-config.json"),
                      "packageOutput": str(output), "mediaRoot": str(output / "media"),
                      "windowsExecutionVerified": False}, ensure_ascii=False))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ["compiler", "pkg-config", "sysroot", "sdk-root", "runtime-sdk-root",
                 "build-directory", "package-output"]:
        parser.add_argument("--" + name, type=Path, required=True)
    args = parser.parse_args()
    if any(not value.is_absolute() for value in vars(args).values()):
        parser.error("所有输入和输出必须为明确绝对路径")
    try:
        build(args)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        print(error, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
