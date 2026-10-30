#!/usr/bin/env python3
"""从实际Go产物和前端构建清单收集许可证据，不下载或判定发行授权。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

MAX_FILE = 2 << 20
MAX_TOTAL = 64 << 20


def digest(data):
    return hashlib.sha256(data).hexdigest()


def read_regular(path, limit=MAX_FILE):
    path = Path(path)
    if path.is_symlink() or not path.is_file():
        raise ValueError('输入须为非链接普通文件')
    before = path.stat()
    if before.st_size > limit:
        raise ValueError('输入超过读取预算')
    with path.open('rb') as handle:
        data = handle.read(limit + 1)
        after = os.fstat(handle.fileno())
    if len(data) > limit or (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns) != (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns):
        raise ValueError('读取期间输入变化')
    return data


def read_json(path):
    return json.loads(read_regular(path))


def inside(root, path):
    try:
        return path.resolve().relative_to(root.resolve())
    except ValueError as error:
        raise ValueError('依赖路径越出明确根目录') from error


def command_json(args, directory, multiple=False):
    environment = os.environ.copy()
    # 只使用已有模块缓存，不为收集证据执行网络下载。
    environment.update(GOPROXY='off', GOSUMDB='off', GOTOOLCHAIN='local')
    result = subprocess.run(args, cwd=directory, env=environment, capture_output=True, check=True, timeout=30)
    if len(result.stdout) > 4 << 20 or len(result.stderr) > 1 << 20:
        raise ValueError('Go元数据输出超过预算')
    text = result.stdout.decode('utf-8')
    if not multiple:
        return json.loads(text)
    decoder, values = json.JSONDecoder(), []
    while text.strip():
        value, end = decoder.raw_decode(text.lstrip())
        values.append(value)
        text = text.lstrip()[end:]
    return values


def frontend_lock(frontend):
    helper = Path(__file__).resolve().parents[1] / 'frontend/build/pnpm-lock.ts'
    result = subprocess.run(['node', str(helper), str(frontend)], capture_output=True,
                            check=True, timeout=30)
    if len(result.stdout) > 4 << 20 or len(result.stderr) > 1 << 20:
        raise ValueError('pnpm锁文件解析结果超过预算')
    return json.loads(result.stdout)


def notice_files(directory):
    if not directory.is_dir():
        return []
    result = []
    # 顶层原许可/声明。模块内嵌第三方及文件级例外明确留待闭包审查。
    entries = []
    for path in directory.iterdir():
        entries.append(path)
        if len(entries) > 4096:
            raise ValueError('依赖根目录条目超过4096')
    for path in sorted(entries):
        if re.match(r'^(LICENSE|LICENCE|COPYING|NOTICE|COPYRIGHT|AUTHORS|PATENTS)([._-].*)?$', path.name, re.I):
            if path.is_symlink() or not path.is_file():
                raise ValueError('声明文件不能为链接或特殊文件')
            if path.suffix.lower() in ['.go', '.js', '.ts', '.map', '.zip', '.gz', '.exe']:
                continue
            result.append(path)
    if len(result) > 32:
        raise ValueError('单依赖声明文本超过32份')
    return result


def collect(config, output):
    project = Path(config['projectRoot']).resolve()
    frontend = Path(config['frontendRoot']).resolve()
    inventory_path = Path(config['frontendInventory'])
    inventory_raw = read_regular(inventory_path)
    inventory = json.loads(inventory_raw)
    lock_raw = read_regular(frontend / 'pnpm-lock.yaml', 4 << 20)
    lock = frontend_lock(frontend)
    if (inventory.get('schemaVersion') != 2 or inventory.get('lockfileName') != 'pnpm-lock.yaml'
            or inventory.get('lockfileSHA256') != digest(lock_raw)
            or lock.get('lockfileSHA256') != digest(lock_raw)
            or inventory.get('packageManager') != lock.get('packageManager')):
        raise ValueError('前端产物清单与锁文件不匹配')
    if not 1 <= len(inventory.get('packages', [])) <= 512 or not 1 <= len(config['artifacts']) <= 8:
        raise ValueError('依赖或产物数量超过预算')
    for chunk in inventory['chunks']:
        name = chunk['file']
        if not isinstance(name, str) or '\\' in name or Path(name).is_absolute() or '..' in Path(name).parts:
            raise ValueError('chunk路径无效')
        path = inventory_path.parent / name
        inside(inventory_path.parent, path)
        data = read_regular(path, 16 << 20)
        if digest(data) != chunk['sha256'] or len(data) != chunk['bytes']:
            raise ValueError('前端chunk与构建清单不一致')
    modules = command_json(['go', 'list', '-m', '-json', 'all'], project, multiple=True)
    cache = {(m['Path'], m.get('Version', '')): m for m in modules}
    report = {'schemaVersion': 1, 'frontendInventorySHA256': digest(inventory_raw),
              'lockfileName': 'pnpm-lock.yaml', 'lockfileSHA256': digest(lock_raw), 'packageManager': lock['packageManager'],
              'artifactHashScope': '收集时输入；后续签名可改变字节，最终交付由外置binding/包内容SHA绑定',
              'artifacts': [], 'components': [], 'issues': [], 'licenseApproval': False,
              'limitations': ['模块/包顶层声明文本不覆盖内嵌第三方及文件级例外', 'go.sum/npm integrity为绑定元数据，未逐文件验证缓存与原发布归档全部内容', '声明许可或文件存在不代表义务履行/对应源码/重链接/专利/签名已核准', '媒体SDK文本和构建闭包需另审，不能由Go/npm清单替代']}
    go_components = {}
    names = set()
    for artifact in config['artifacts']:
        name = artifact['name']
        if not re.fullmatch(r'[a-z0-9-]{1,64}', name) or name in names:
            raise ValueError('产物名无效或重复')
        names.add(name)
        path = Path(artifact['path'])
        before = read_regular(path, 512 << 20)
        build = command_json(['go', 'version', '-m', '-json', str(path)], project)
        if digest(read_regular(path, 512 << 20)) != digest(before):
            raise ValueError('读取构建信息期间产物变化')
        report['artifacts'].append({'name': name, 'sha256': digest(before), 'bytes': len(before), 'goVersion': build['GoVersion'],
                                    'target': {s['Key']: s['Value'] for s in build.get('Settings', []) if s['Key'] in ['GOOS', 'GOARCH', 'CGO_ENABLED', '-trimpath']}})
        for module in build.get('Deps', []):
            replacement = module.get('Replace')
            identity = replacement or module
            key = identity['Path'], identity.get('Version', '')
            if not key[1] or key not in cache:
                raise ValueError('实际Go依赖未找到精确缓存版本，不自动下载或猜测')
            cached = cache[key]
            if identity.get('Sum') and cached.get('Sum') != identity['Sum']:
                raise ValueError('实际Go模块校验和与缓存元数据不同')
            component = go_components.setdefault(key, {'ecosystem': 'go', 'name': key[0], 'version': key[1], 'sum': identity.get('Sum', ''), 'artifacts': [], 'directory': Path(cached['Dir'])})
            component['artifacts'].append(name)
            if replacement:
                report['issues'].append({'component': key[0] + '@' + key[1], 'reason': '实际产物使用替换模块，需要复核原始身份与许可'})
    components = list(go_components.values())
    versions = {a['goVersion'] for a in report['artifacts']}
    provided_toolchains = set()
    for toolchain in config.get('toolchains', []):
        version, directory = toolchain['version'], Path(toolchain['directory'])
        if version not in versions or version in provided_toolchains or read_regular(directory / 'VERSION').decode().splitlines()[0] != version:
            raise ValueError('工具链声明与实际构建版本不一致')
        provided_toolchains.add(version)
        components.append({'ecosystem': 'toolchain', 'name': 'go', 'version': version, 'directory': directory,
                           'artifacts': [a['name'] for a in report['artifacts'] if a['goVersion'] == version]})
    for version in sorted(versions - provided_toolchains):
        report['issues'].append({'component': 'toolchain:go@' + version, 'reason': '缺少精确Go工具链根目录，尚未收集其许可文本'})
    if inventory.get('unknownGeneratedModules', inventory.get('generatedModules', 0)):
        report['issues'].append({'component': 'frontend-generated-modules', 'reason': '构建器生成模块需复核其工具来源/声明，不以包模块清单代替此项'})
    for package in inventory['packages']:
        key = package['lockPath']
        if not isinstance(key, str) or not key.startswith('node_modules/') or '\\' in key or '..' in Path(key).parts:
            raise ValueError('前端包路径无效')
        directory = frontend / key
        inside(frontend / 'node_modules', directory)
        metadata_raw = read_regular(directory / 'package.json')
        metadata = json.loads(metadata_raw)
        locked = lock['packages'].get(package['name'] + '@' + package['version'], {})
        integrity = locked.get('resolution', {}).get('integrity')
        if (metadata['name'] != package['name'] or metadata['version'] != package['version']
                or not integrity or integrity != package.get('integrity')):
            raise ValueError('实际前端元数据与构建清单/锁文件不同')
        components.append({'ecosystem': 'npm', 'name': package['name'], 'version': package['version'], 'integrity': package.get('integrity', ''),
                           'declaredLicense': metadata.get('license', ''), 'packageJSONSHA256': digest(metadata_raw), 'directory': directory})
    overrides = {}
    for entry in config.get('licenseSources', []):
        if entry['component'] in overrides or not re.fullmatch(r'[0-9a-f]{64}', entry['sha256']) or not entry.get('sourceURL') or not entry.get('commit'):
            raise ValueError('补充声明需要唯一组件、来源提交和SHA256')
        source = project / entry['file']
        inside(project, source)
        payload = read_regular(source)
        if digest(payload) != entry['sha256']:
            raise ValueError('补充声明文本与固定SHA256不同')
        overrides[entry['component']] = (entry, source)
    component_ids = {c['ecosystem'] + ':' + c['name'] + '@' + c['version'] for c in components}
    if set(overrides) - component_ids:
        raise ValueError('补充声明不属于实际组件，未加入其它版本文本')
    total = 0
    for component in sorted(components, key=lambda c: (c['ecosystem'], c['name'], c['version'])):
        directory = component.pop('directory')
        identity = component['ecosystem'] + ':' + component['name'] + '@' + component['version']
        component['id'] = identity
        component['texts'] = []
        key = digest(identity.encode())[:24]
        original_files = notice_files(directory)
        if not original_files and identity in overrides:
            provenance, source = overrides[identity]
            original_files = [source]
            component['supplementalSource'] = {k: provenance[k] for k in ['sourceURL', 'commit', 'sha256']}
        for path in original_files:
            data = read_regular(path)
            data.decode('utf-8')
            if b'\x00' in data:
                raise ValueError('声明文本含二进制内容')
            total += len(data)
            if total > MAX_TOTAL:
                raise ValueError('声明文本总量超过64MiB')
            target = output / 'texts' / component['ecosystem'] / key / path.name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(data)
            component['texts'].append({'path': target.relative_to(output).as_posix(), 'sourceName': path.name, 'sha256': digest(data), 'bytes': len(data)})
        if not component['texts']:
            report['issues'].append({'component': identity, 'reason': '精确版本包未提供顶层许可/声明文本；不得用元数据表达式代替原文'})
        report['components'].append(component)
    report['textBytes'] = total
    report['missingTextComponents'] = sum(not c['texts'] for c in report['components'])
    (output / 'inventory.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    lines = ['# 第三方声明证据', '', '此目录仅整理实际Go产物与前端构建包的原声明文本。缺项和边界见inventory.json，licenseApproval始终false。', '']
    for component in report['components']:
        lines += ['## ' + component['id'], '']
        for text in component['texts']:
            lines.append('- [' + text['sourceName'] + '](' + text['path'] + ') — SHA256 ' + text['sha256'])
        if not component['texts']:
            lines.append('缺少原声明文本，待补齐。')
        lines.append('')
    (output / 'NOTICES.md').write_text('\n'.join(lines), encoding='utf-8')
    return report


def bind(artifact, notices, output):
    artifact, notices, output = Path(artifact).resolve(), Path(notices).resolve(), Path(output).absolute()
    if output.resolve() == artifact or output.resolve() == notices or notices in output.resolve().parents:
        raise ValueError('绑定清单不能覆盖可执行文件或声明材料')
    inventory = read_json(notices / 'inventory.json')
    for component in inventory['components']:
        for text in component['texts']:
            relative = Path(text['path'])
            if relative.is_absolute() or '..' in relative.parts or '\\' in text['path']:
                raise ValueError('声明文本路径无效')
            path = notices / relative
            inside(notices, path)
            payload = read_regular(path)
            if digest(payload) != text['sha256'] or len(payload) != text['bytes']:
                raise ValueError('待绑定声明原文已变化')
    files, size, nodes, candidates = [], 0, 0, []
    for directory, directories, names in os.walk(notices, followlinks=False):
        for name in directories + names:
            nodes += 1
            path = Path(directory) / name
            if nodes > 8192 or path.is_symlink():
                raise ValueError('声明材料节点超界或含链接')
        for name in names:
            candidates.append(Path(directory) / name)
            if len(candidates) > 4096:
                raise ValueError('声明材料文件数量超界')
    for path in sorted(candidates):
        inside(notices, path)
        data = read_regular(path)
        size += len(data)
        if size > MAX_TOTAL:
            raise ValueError('声明材料总量超界')
        files.append({'path': path.relative_to(notices).as_posix(), 'sha256': digest(data), 'bytes': len(data)})
    data = {'schemaVersion': 1, 'artifactSHA256': digest(read_regular(artifact, 512 << 20)),
            'noticesInventorySHA256': digest(read_regular(notices / 'inventory.json')),
            'noticesTreeSHA256': digest(json.dumps(files, sort_keys=True).encode()), 'files': files, 'licenseApproval': False}
    with tempfile.NamedTemporaryFile(mode='w', encoding='utf-8', dir=output.parent, prefix='.notices-binding-', delete=False) as handle:
        json.dump(data, handle, ensure_ascii=False, indent=2)
        temporary = Path(handle.name)
    try:
        os.replace(temporary, output)
    finally:
        temporary.unlink(missing_ok=True)
    return data


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config')
    parser.add_argument('--artifact')
    parser.add_argument('--project-root')
    parser.add_argument('--bind-notices')
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    if args.bind_notices:
        if not args.artifact or args.config or args.project_root:
            parser.error('绑定模式需要artifact/bind-notices，不接受config/project-root')
        data = bind(args.artifact, args.bind_notices, args.output)
        print(json.dumps({'artifactSHA256': data['artifactSHA256'], 'noticesTreeSHA256': data['noticesTreeSHA256'], 'licenseApproval': False}))
        return
    if args.config:
        if args.artifact or args.project_root:
            parser.error('config模式不能同时指定artifact/project-root')
        config = read_json(args.config)
    else:
        if not args.artifact or not args.project_root:
            parser.error('需要config或明确的artifact/project-root')
        project = Path(args.project_root).resolve()
        config = {'projectRoot': str(project), 'frontendRoot': str(project / 'frontend'),
                  'frontendInventory': str(project / 'frontend/dist/dependency-inventory.json'),
                  'artifacts': [{'name': 'desktop', 'path': str(Path(args.artifact).resolve())}],
                  'licenseSources': read_json(project / 'build/dependency-license-sources.json'), 'toolchains': []}
        build = command_json(['go', 'version', '-m', '-json', config['artifacts'][0]['path']], project)
        environment = command_json(['go', 'env', '-json', 'GOMODCACHE', 'GOROOT'], project)
        candidates = [Path(environment['GOROOT'])]
        candidates += sorted(Path(environment['GOMODCACHE']).glob('golang.org/toolchain@v0.0.1-' + build['GoVersion'] + '.*'))[:64]
        for candidate in candidates:
            version_file = candidate / 'VERSION'
            if version_file.is_file() and read_regular(version_file).decode().splitlines()[0] == build['GoVersion']:
                config['toolchains'] = [{'version': build['GoVersion'], 'directory': str(candidate)}]
                break
    output = Path(args.output).absolute()
    # 新目录才可写；失败保留部分证据，未完成清单不能当成功产物。
    output.mkdir(mode=0o700)
    report = collect(config, output)
    print(json.dumps({'components': len(report['components']), 'missingTexts': report['missingTextComponents'], 'textBytes': report['textBytes'], 'licenseApproval': False}, ensure_ascii=False))


if __name__ == '__main__':
    main()
