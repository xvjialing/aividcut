#!/usr/bin/env python3
"""显式SDK的私有macOS装配；只写新目录，不批准许可或发布。"""
import argparse
import json
import os
from pathlib import Path
import plistlib
import re
import platform
import shutil
import subprocess
import sys
import tempfile


def inside(root, path):
    try:
        path.relative_to(root)
        return True
    except ValueError:
        return False


def macho_images(root):
    magic = {b'\xcf\xfa\xed\xfe', b'\xfe\xed\xfa\xcf', b'\xce\xfa\xed\xfe',
             b'\xfe\xed\xfa\xce', b'\xca\xfe\xba\xbe', b'\xbe\xba\xfe\xca'}
    for p in sorted(root.rglob('*')):
        if p.is_symlink():
            if Path(os.readlink(p)).is_absolute() or not inside(root, p.resolve(strict=True)):
                raise ValueError('装配目录含外部链接：' + str(p))
        elif p.is_file():
            with p.open('rb') as stream:
                if stream.read(4) in magic:
                    yield p


def imports(image):
    # -L包含dylib自身ID，必须分开识别，不能当作外部导入。
    lines = subprocess.check_output(['/usr/bin/otool', '-D', str(image)], text=True).splitlines()[1:]
    identity = lines[0].strip() if lines else None
    names = []
    for line in subprocess.check_output(['/usr/bin/otool', '-L', str(image)], text=True).splitlines()[1:]:
        name = line.strip().split(' (compatibility version', 1)[0]
        if name and name != identity:
            names.append(name)
    return identity, names


def normalize_links(sdk, app, log, arch):
    frameworks = app / 'Contents/Frameworks'
    exe_dir = app / 'Contents/MacOS'
    images = list(macho_images(sdk))
    records = []
    for image in images:
        architectures = subprocess.check_output(['/usr/bin/lipo', '-archs', str(image)], text=True).split()
        expected = {'arm64': 'arm64', 'amd64': 'x86_64'}[arch]
        if expected not in architectures:
            raise ValueError('Mach-O缺少目标架构：' + str(image))
        image.chmod(image.stat().st_mode | 0o200)
        identity, names = imports(image)
        for name in names:
            if name.startswith(('/System/Library/', '/usr/lib/')):
                continue
            if name.startswith('@loader_path/'):
                target = image.parent / name[len('@loader_path/'):]
            elif name.startswith('@executable_path/'):
                target = exe_dir / name[len('@executable_path/'):]
            elif name.startswith('@rpath/'):
                target = frameworks / name[len('@rpath/'):]
            elif '.framework/' in name:
                first = name.index('.framework/')
                start = name.rfind('/', 0, first) + 1
                target = frameworks / name[start:]
            else:
                target = frameworks / Path(name).name
            target = target.resolve(strict=True)
            if not inside(sdk, target):
                raise ValueError('依赖仍在装配包外：' + name)
            relative = '@loader_path/' + os.path.relpath(target, image.parent)
            if relative != name:
                run(['/usr/bin/install_name_tool', '-change', name, relative, str(image)], log)
        load = subprocess.check_output(['/usr/bin/otool', '-l', str(image)], text=True)
        for rpath in re.findall(r'cmd LC_RPATH\s+cmdsize \d+\s+path (.*?) \(offset', load):
            if rpath.startswith('/') and not rpath.startswith(('/System/Library/', '/usr/lib/')):
                run(['/usr/bin/install_name_tool', '-delete_rpath', rpath, str(image)], log)
        if identity:
            run(['/usr/bin/install_name_tool', '-id', '@loader_path/' + image.name, str(image)], log)
        # 改写后再读一遍，不把Qt工具退出0当作闭包成立。
        _, updated = imports(image)
        for name in updated:
            if name.startswith(('/System/Library/', '/usr/lib/')):
                continue
            if not name.startswith('@loader_path/'):
                raise ValueError('依赖未转为确定的相对路径：' + name)
            target = (image.parent / name[len('@loader_path/'):]).resolve(strict=True)
            if not inside(sdk, target):
                raise ValueError('相对依赖越出媒体目录')
        records.append({'image': image.relative_to(sdk).as_posix(), 'architectures': architectures, 'imports': updated})
    return records


def require_file(value):
    path = Path(value).resolve(strict=True)
    if not path.is_file():
        raise ValueError('输入不是普通文件：' + str(path))
    return path


def run(args, log):
    with log.open('ab') as stream:
        stream.write(('执行：' + args[0] + '\n').encode())
        env = {k: v for k, v in os.environ.items() if not k.startswith(('DYLD_', 'QT_', 'QML_'))}
        env['PATH'] = '/usr/bin:/bin'
        subprocess.run(args, stdout=stream, stderr=subprocess.STDOUT, check=True, timeout=180, env=env)


def package(config):
    if sys.platform != 'darwin':
        raise ValueError('macOS装配需要本机Qt/Apple工具；Windows保留独立装配路线')
    allowed = {'output', 'worker', 'ffmpeg', 'ffprobe', 'repository', 'profiles', 'data',
               'macdeployqt', 'qtPlatformPlugins', 'libraryPaths', 'manifestTool', 'id', 'version', 'arch'}
    if set(config) - allowed or not allowed <= set(config):
        raise ValueError('装配配置字段缺失或未知')
    if config['arch'] != {'arm64': 'arm64', 'x86_64': 'amd64'}.get(platform.machine()):
        raise ValueError('此装配入口只验证本机架构SDK，不能冒充跨架构部署')
    for key in ['qtPlatformPlugins', 'libraryPaths']:
        if not isinstance(config[key], list) or len(config[key]) > 64 or any(not isinstance(p, str) or not Path(p).is_absolute() for p in config[key]):
            raise ValueError('SDK路径列表必须为最多64个明确绝对路径：' + key)
    output = Path(config['output']).absolute()
    if output.exists() or output.is_symlink():
        raise ValueError('输出已存在，未覆盖：' + str(output))
    if not output.parent.is_dir():
        raise ValueError('输出父目录必须存在')
    worker, ffmpeg, ffprobe, deploy, manifest = [require_file(config[k]) for k in
                                              ['worker', 'ffmpeg', 'ffprobe', 'macdeployqt', 'manifestTool']]
    repo, profiles, data_root = [Path(config[k]).resolve(strict=True) for k in ['repository', 'profiles', 'data']]
    if not repo.is_dir() or not profiles.is_dir() or not data_root.is_dir():
        raise ValueError('MLT输入必须为明确目录')
    if any(inside(p, output.parent.resolve()) for p in [repo, profiles, data_root]):
        raise ValueError('输出不能位于输入目录内')
    plugins = [require_file(p) for p in config['qtPlatformPlugins']]
    if not any(p.name == 'libqoffscreen.dylib' for p in plugins):
        raise ValueError('必须明确提供worker需要的Qt离屏平台插件')
    stage = Path(tempfile.mkdtemp(prefix='.' + output.name + '-stage-', dir=output.parent))
    sdk = stage / 'media'
    app = sdk / 'AIVidCutMedia.app'
    exe = app / 'Contents/MacOS'
    resources = app / 'Contents/Resources'
    exe.mkdir(parents=True)
    resources.mkdir()
    log = stage / 'assembly.log'
    try:
        for source, name in [(worker, 'AIVidCutMedia'), (ffmpeg, 'ffmpeg'), (ffprobe, 'ffprobe')]:
            shutil.copy2(source, exe / name)
            (exe / name).chmod(0o755)
        (resources / 'mlt').mkdir()
        modules = []
        for source in sorted(repo.iterdir()):
            target = require_file(source)
            destination = resources / 'mlt' / source.name
            shutil.copy2(target, destination)
            destination.chmod(destination.stat().st_mode | 0o200)
            modules.append(destination)
        if not modules:
            raise ValueError('模块目录为空')
        shutil.copytree(data_root, resources / 'mlt-data', symlinks=True)
        if (resources / 'mlt-data/profiles').exists():
            shutil.rmtree(resources / 'mlt-data/profiles')
        shutil.copytree(profiles, resources / 'mlt-data/profiles', symlinks=True)
        platform_dir = app / 'Contents/PlugIns/platforms'
        platform_dir.mkdir(parents=True)
        for plugin in plugins:
            shutil.copy2(plugin, platform_dir / plugin.name)
            (platform_dir / plugin.name).chmod(0o755)
        with (app / 'Contents/Info.plist').open('wb') as f:
            plistlib.dump({'CFBundlePackageType': 'APPL', 'CFBundleName': 'AIVidCutMedia',
                          'CFBundleExecutable': 'AIVidCutMedia', 'CFBundleIdentifier': 'com.aividcut.media'}, f)
        # macOS的Prefix相对Contents，不是MacOS可执行目录。
        (resources / 'qt.conf').write_text('[Paths]\nPrefix = .\nPlugins = PlugIns\n', encoding='utf-8')
        args = [str(deploy), str(app), '-no-strip', '-no-codesign', '-verbose=2']
        args += ['-libpath=' + str(Path(p).resolve(strict=True)) for p in config['libraryPaths']]
        args += ['-executable=' + str(p) for p in [exe / 'ffmpeg', exe / 'ffprobe', *modules,
                                                 *sorted(platform_dir.iterdir())]]
        run(args, log)
        closure = normalize_links(sdk, app, log, config['arch'])
        (stage / 'link-closure.json').write_text(json.dumps(closure, indent=2) + '\n', encoding='utf-8')
        # 所有变更完成后再签名，之后生成内容清单；不使用用户签名私钥。
        # MLT的.so位于Resources，不属于codesign --deep自动识别的全部代码位置。
        for record in closure:
            run(['/usr/bin/codesign', '--force', '--sign', '-', str(sdk / record['image'])], log)
        run(['/usr/bin/codesign', '--force', '--deep', '--sign', '-', str(app)], log)
        run(['/usr/bin/codesign', '--verify', '--deep', '--strict', str(app)], log)
        prefix = 'AIVidCutMedia.app/Contents/'
        spec = {'schemaVersion': 2, 'id': config['id'], 'version': config['version'], 'os': 'darwin',
                'arch': config['arch'], 'paths': {'ffmpeg': prefix + 'MacOS/ffmpeg',
                'ffprobe': prefix + 'MacOS/ffprobe', 'worker': prefix + 'MacOS/AIVidCutMedia',
                'repository': prefix + 'Resources/mlt', 'profiles': prefix + 'Resources/mlt-data/profiles',
                'data': prefix + 'Resources/mlt-data'}}
        spec_file = stage / 'manifest-spec.json'
        spec_file.write_text(json.dumps(spec, indent=2) + '\n', encoding='utf-8')
        run([str(manifest), '-root', str(sdk), '-spec', str(spec_file)], log)
        stage.rename(output)
    except BaseException:
        print('装配未完成，保留本次隔离目录供核对：' + str(stage), file=sys.stderr)
        raise
    return output


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True)
    args = parser.parse_args()
    p = Path(args.config)
    if p.stat().st_size > 65536:
        raise ValueError('配置超过64KiB')
    config = json.loads(p.read_text(encoding='utf-8'))
    # 配置显式使用绝对路径，避免发布进程cwd改变输入来源。
    for key in ['output', 'worker', 'ffmpeg', 'ffprobe', 'repository', 'profiles', 'data', 'macdeployqt', 'manifestTool']:
        if not Path(config[key]).is_absolute():
            raise ValueError('装配路径必须为绝对路径：' + key)
    print('已生成私有装配目录：' + str(package(config)))


if __name__ == '__main__':
    main()
