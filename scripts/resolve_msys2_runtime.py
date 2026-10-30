#!/usr/bin/env python3
"""从明确MSYS2快照生成包锁；按显式范围重放运行文件或开发头/导入库，不执行SDK。"""
import argparse
import contextlib
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import tarfile
import tempfile
import urllib.request

BASE = 'https://mirror.msys2.org/mingw/ucrt64/'
PREFIX = 'mingw-w64-ucrt-x86_64-'
MAX_PACKAGE = 256 << 20
MAX_TOTAL = 2 << 30


def digest(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for chunk in iter(lambda: stream.read(65536), b''):
            h.update(chunk)
    return h.hexdigest()


def safe_name(name):
    p = PurePosixPath(name)
    if p.is_absolute() or '..' in p.parts or '\\' in name or ':' in name or '\x00' in name:
        raise ValueError('归档含越界或不可移植路径')
    if any(part.endswith(('.', ' ')) for part in p.parts):
        raise ValueError('归档含Windows不可移植路径')
    reserved = {'CON', 'PRN', 'AUX', 'NUL', *('COM' + str(i) for i in range(1, 10)), *('LPT' + str(i) for i in range(1, 10))}
    if any(part.split('.')[0].upper() in reserved for part in p.parts):
        raise ValueError('归档含Windows设备名称')
    return p


@contextlib.contextmanager
def archive_file(path, zstd):
    path = Path(path)
    with path.open('rb') as stream:
        magic = stream.read(4)
    if magic != b'\x28\xb5\x2f\xfd':
        with tarfile.open(path, 'r:*') as archive:
            yield archive
        return
    with tempfile.TemporaryFile() as expanded:
        process = subprocess.Popen([zstd, '-d', '-q', '-c', str(path)], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        count = 0
        try:
            for chunk in iter(lambda: process.stdout.read(65536), b''):
                count += len(chunk)
                if count > 1 << 30:
                    raise ValueError('解压归档超过1GiB')
                expanded.write(chunk)
            if process.wait(timeout=20) != 0:
                raise ValueError('Zstd解压失败')
            expanded.seek(0)
            with tarfile.open(fileobj=expanded) as archive:
                yield archive
        finally:
            if process.poll() is None:
                process.kill()
            process.wait()
            process.stdout.close()
            process.stderr.close()


def fields(text):
    result = {}
    key = None
    for line in text.splitlines():
        if line.startswith('%') and line.endswith('%'):
            key = line.strip('%')
            if key in result:
                raise ValueError('元数据字段重复')
            result[key] = []
        elif line and key:
            result[key].append(line)
    return result


def read_database(path, zstd, files=False):
    if not Path(path).is_file() or Path(path).stat().st_size > 64 << 20:
        raise ValueError('仓库快照必须为64MiB以内普通文件')
    descriptions, inventories = {}, {}
    with archive_file(path, zstd) as archive:
        entries = 0
        for member in archive:
            safe_name(member.name)
            entries += 1
            if entries > 60000 or member.size > 8 << 20:
                raise ValueError('仓库元数据超过边界')
            if not member.isfile():
                continue
            directory, kind = member.name.rsplit('/', 1)
            if kind not in ('desc', 'files'):
                continue
            value = fields(archive.extractfile(member).read().decode('utf-8'))
            if kind == 'desc':
                descriptions[directory] = value
            elif files:
                inventories[directory] = value.get('FILES', [])
    return descriptions, inventories


def make_lock(database, file_database, missing, roots, zstd, selections=None):
    selections = selections or {}
    descriptions, _ = read_database(database, zstd)
    file_descriptions, inventories = read_database(file_database, zstd, True)
    packages, providers = {}, {}
    for directory, description in descriptions.items():
        def one(key):
            value = description.get(key, [])
            if len(value) != 1:
                raise ValueError('包元数据缺少单值字段：' + key)
            return value[0]
        name, filename, version, sha = [one(k) for k in ['NAME', 'FILENAME', 'VERSION', 'SHA256SUM']]
        if not name.startswith(PREFIX) or not filename.startswith(PREFIX) or not filename.endswith(('.pkg.tar.zst', '.pkg.tar.gz', '.pkg.tar.xz')) or not re.fullmatch(r'[A-Za-z0-9._+~\-]+', filename):
            raise ValueError('仓库含非预期UCRT64包或文件名')
        if not re.fullmatch('[0-9a-f]{64}', sha):
            raise ValueError('包SHA256无效')
        if directory not in file_descriptions or file_descriptions[directory].get('VERSION') != [version]:
            raise ValueError('db与files快照版本不一致：' + name)
        packages[name] = {'name': name, 'version': version, 'filename': filename, 'sha256': sha,
                          'url': BASE + filename, 'downloadBytes': int(one('CSIZE')),
                          'declaredLicenses': description.get('LICENSE', []),
                          'declaredDependencies': description.get('DEPENDS', [])}
        for path in inventories.get(directory, []):
            if path.startswith('ucrt64/bin/') and path.lower().endswith('.dll'):
                key = PurePosixPath(path).name.lower()
                providers.setdefault(key, set()).add(name)
    selected, unresolved = set(roots), []
    for name in selected:
        if name not in packages:
            raise ValueError('根包不在快照中：' + name)
    for dll in sorted(set(missing), key=str.lower):
        choices = providers.get(dll.lower(), set())
        selection = selections.get(dll.lower())
        if selection:
            if selection not in choices:
                raise ValueError('明确选择不在该DLL提供者中：' + dll)
            selected.add(selection)
        elif len(choices) != 1:
            unresolved.append({'dll': dll, 'providers': sorted(choices)})
        else:
            selected.update(choices)
    items = [packages[name] for name in sorted(selected)]
    if len(items) > 256 or sum(p['downloadBytes'] for p in items) > MAX_TOTAL:
        raise ValueError('包锁超过数量/下载边界')
    return {'schemaVersion': 1, 'repository': BASE, 'databaseSHA256': digest(database),
            'filesSHA256': digest(file_database), 'packages': items, 'unresolved': unresolved,
            'providerSelections': selections, 'signatureVerified': False, 'releaseVerified': False}


def runtime_member(path):
    name = path.as_posix()
    return (name.startswith('ucrt64/bin/') and (path.suffix.lower() == '.dll' or path.name.lower() in ('ffmpeg.exe', 'ffprobe.exe')) or
            name.startswith('ucrt64/lib/mlt/') and path.suffix.lower() == '.dll' or
            name.startswith('ucrt64/share/qt6/plugins/') and path.suffix.lower() == '.dll' or
            name.startswith('ucrt64/share/mlt/') or name.startswith('ucrt64/share/licenses/'))


def development_member(path):
    name = path.as_posix()
    return (name.startswith('ucrt64/include/') or
            name.startswith('ucrt64/lib/') and name.endswith('.dll.a') or
            name.startswith('ucrt64/lib/pkgconfig/') and path.suffix == '.pc' or
            name.startswith('ucrt64/share/qt6/mkspecs/') or
            name.startswith('ucrt64/share/licenses/'))


def apply_lock(lock, output, cache, zstd, profile='runtime'):
    output, cache = Path(output).absolute(), Path(cache).absolute()
    if output.exists() or output.is_symlink():
        raise ValueError('SDK输出已存在，未覆盖')
    if lock.get('schemaVersion') != 1 or lock.get('repository') != BASE or lock.get('unresolved'):
        raise ValueError('包锁来源/版本无效或仍有未解析DLL')
    packages = lock.get('packages', [])
    if not packages or len(packages) > 256:
        raise ValueError('包锁数量无效')
    if any(not isinstance(p.get('downloadBytes'), int) or p['downloadBytes'] <= 0 or p['downloadBytes'] > MAX_PACKAGE for p in packages) or sum(p['downloadBytes'] for p in packages) > MAX_TOTAL:
        raise ValueError('包锁下载大小声明无效或超限')
    if profile == 'development':
        required = {PREFIX + name for name in ['mlt', 'ffmpeg', 'qt6-base', 'qt6-svg']}
        packages = [p for p in packages if p.get('name') in required]
        if {p['name'] for p in packages} != required:
            raise ValueError('开发头/导入库需要包锁中的MLT/FFmpeg/Qt6 base/svg四包')
        select_member = development_member
    elif profile == 'runtime':
        select_member = runtime_member
    else:
        raise ValueError('未知提取范围')
    cache.mkdir(parents=True, exist_ok=True)
    stage = Path(tempfile.mkdtemp(prefix='.msys2-runtime-stage-', dir=output.parent))
    total, count, names = 0, 0, {}
    try:
        for package in packages:
            filename, sha = package['filename'], package['sha256']
            if not filename.startswith(PREFIX) or not filename.endswith(('.pkg.tar.zst', '.pkg.tar.gz', '.pkg.tar.xz')) or not re.fullmatch(r'[A-Za-z0-9._+~\-]+', filename) or not re.fullmatch('[0-9a-f]{64}', sha) or package['url'] != BASE + filename:
                raise ValueError('包锁地址或摘要无效')
            compressed = cache / filename
            if not compressed.exists():
                partial = cache / (filename + '.part')
                with urllib.request.urlopen(package['url'], timeout=60) as response, partial.open('xb') as stream:
                    if not response.geturl().startswith('https://'):
                        raise ValueError('下载重定向离开HTTPS')
                    downloaded = 0
                    for chunk in iter(lambda: response.read(65536), b''):
                        downloaded += len(chunk)
                        if downloaded > MAX_PACKAGE:
                            raise ValueError('下载包过大')
                        stream.write(chunk)
                partial.rename(compressed)
            if compressed.stat().st_size != package['downloadBytes'] or digest(compressed) != sha:
                raise ValueError('包摘要不匹配：' + filename)
            with archive_file(compressed, zstd) as archive:
                for member in archive:
                    path = safe_name(member.name)
                    if not select_member(path) or member.isdir():
                        continue
                    if not member.isfile() or member.size > 512 << 20:
                        raise ValueError('运行内容不是受限普通文件')
                    total += member.size
                    count += 1
                    if total > MAX_TOTAL or count > 16384:
                        raise ValueError('SDK运行内容超过边界')
                    key = path.as_posix().lower()
                    target = stage / Path(*path.parts)
                    target.parent.mkdir(parents=True, exist_ok=True)
                    h = hashlib.sha256()
                    with archive.extractfile(member) as source:
                        if key in names:
                            for chunk in iter(lambda: source.read(65536), b''):
                                h.update(chunk)
                            if h.hexdigest() != names[key]:
                                raise ValueError('包内同名内容冲突')
                            continue
                        with target.open('xb') as stream:
                            for chunk in iter(lambda: source.read(65536), b''):
                                h.update(chunk)
                                stream.write(chunk)
                    names[key] = h.hexdigest()
        (stage / 'sdk-source-lock.json').write_text(json.dumps(lock, indent=2) + '\n', encoding='utf-8')
        if profile != 'runtime':
            (stage / 'sdk-extraction.json').write_text(json.dumps({'profile': profile, 'packages': [p['name'] for p in packages], 'releaseVerified': False}, indent=2))
        if output.exists() or output.is_symlink():
            raise ValueError('提交前输出已变化，未覆盖')
        stage.rename(output)
    except BaseException:
        print('仅保留本次隔离stage供核对：' + str(stage), file=__import__('sys').stderr)
        raise
    return {'files': len(names), 'bytes': total, 'packages': len(packages), 'releaseVerified': False}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--zstd', required=True)
    sub = parser.add_subparsers(dest='mode', required=True)
    plan = sub.add_parser('plan')
    for name in ['database', 'files-database', 'missing', 'output-lock']:
        plan.add_argument('--' + name, required=True)
    plan.add_argument('--root-package', action='append', default=[])
    plan.add_argument('--provider', action='append', default=[], help='冲突时明确DLL=包名，不猜第一个提供者')
    replay = sub.add_parser('apply')
    replay.add_argument('--profile', choices=['runtime', 'development'], default='runtime',
                        help='默认运行库；development仅提取四个根包的头文件/导入库/pkg-config，不执行SDK工具')
    for name in ['lock', 'output', 'cache']:
        replay.add_argument('--' + name, required=True)
    args = parser.parse_args()
    if args.mode == 'plan':
        missing = json.loads(Path(args.missing).read_text(encoding='utf-8'))['missingNames']
        selections = dict(item.split('=', 1) for item in args.provider)
        lock = make_lock(args.database, args.files_database, missing, args.root_package, args.zstd, {k.lower(): v for k, v in selections.items()})
        with Path(args.output_lock).open('x', encoding='utf-8') as stream:
            json.dump(lock, stream, indent=2)
        print('包锁：', len(lock['packages']), '未解析：', len(lock['unresolved']))
    else:
        lock = json.loads(Path(args.lock).read_text(encoding='utf-8'))
        print(json.dumps(apply_lock(lock, args.output, args.cache, args.zstd, args.profile)))


if __name__ == '__main__':
    main()
