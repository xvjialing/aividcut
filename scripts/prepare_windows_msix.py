#!/usr/bin/env python3
"""准备可审核MSIX目录和构建入口；不运行产物、签名、安装或发布。"""
import argparse
import binascii
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import struct
import xml.etree.ElementTree as ET

MAX_FILE = 512 << 20
MAX_TOTAL = 1 << 30
NAMESPACE = 'http://schemas.microsoft.com/appx/manifest/foundation/windows10'
UAP = 'http://schemas.microsoft.com/appx/manifest/uap/windows10'
UAP10 = 'http://schemas.microsoft.com/appx/manifest/uap/windows10/10'
RESCAP = 'http://schemas.microsoft.com/appx/manifest/foundation/windows10/restrictedcapabilities'

def read(path, limit=MAX_FILE):
    path = Path(path)
    if path.is_symlink() or not path.is_file():
        raise ValueError('输入必须为非链接普通文件')
    before = path.stat()
    if before.st_size > limit:
        raise ValueError('文件超过预算')
    with path.open('rb') as stream:
        data = stream.read(limit + 1)
        after = os.fstat(stream.fileno())
    if len(data) > limit or (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns) != (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns):
        raise ValueError('读取期间文件变化')
    return data

def sha(data):
    return hashlib.sha256(data).hexdigest()

def validate_pe(data, arch):
    if len(data) < 64 or data[:2] != b'MZ':
        raise ValueError('需要真实PE文件头')
    offset = struct.unpack_from('<I', data, 60)[0]
    expected = {'x64': 0x8664, 'arm64': 0xAA64}[arch]
    if offset > len(data) - 24 or data[offset:offset+4] != b'PE\0\0' or struct.unpack_from('<H', data, offset+4)[0] != expected:
        raise ValueError('PE目标架构或头无效')

def valid_windows_path(relative):
    for part in relative.parts:
        if part.rstrip(' .') != part or re.search(r'[<>:"\\|?*\x00-\x1f]', part) or part.split('.')[0].upper() in {'CON', 'PRN', 'AUX', 'NUL', *(f'COM{i}' for i in range(1,10)), *(f'LPT{i}' for i in range(1,10))}:
            raise ValueError('文件名不符合Windows路径约定')

def png(path, size):
    data = read(path, 2 << 20)
    if data[:8] != b'\x89PNG\r\n\x1a\n':
        raise ValueError('图标必须为PNG')
    offset, tags = 8, []
    while offset < len(data):
        if offset + 12 > len(data):
            raise ValueError('PNG被截断')
        length = struct.unpack_from('>I', data, offset)[0]
        tag = data[offset+4:offset+8]
        if length > len(data)-offset-12:
            raise ValueError('PNG块超过文件边界')
        payload = data[offset+8:offset+8+length]
        if binascii.crc32(tag+payload) & 0xffffffff != struct.unpack_from('>I', data, offset+8+length)[0]:
            raise ValueError('PNG块CRC不符')
        tags.append(tag)
        if len(tags) == 1:
            if tag != b'IHDR' or length != 13 or struct.unpack_from('>II', payload) != (size, size):
                raise ValueError('PNG尺寸不符')
        offset += length + 12
        if tag == b'IEND':
            if length != 0 or offset != len(data):
                raise ValueError('PNG结束数据无效')
            break
    if not tags or tags[-1] != b'IEND' or b'IDAT' not in tags:
        raise ValueError('PNG缺少图像数据或结束块')
    return data

def ocr_binding(candidate, project, arch):
    binary = candidate / 'AIVidCutOCR.exe'
    record_path = candidate / 'ocr-build-info.json'
    record = json.loads(read(record_path, 64 << 10))
    payload = read(binary)
    validate_pe(payload, arch)
    if record.get('schemaVersion') != 1 or record.get('component') != 'AIVidCutOCR' or record.get('engine') != 'windows-media-ocr' or record.get('arch') != arch or record.get('source') != 'native/ocr-windows/main.cpp' or record.get('nativeCompileCompleted') is not True or record.get('packageIdentityRequired') is not True:
        raise ValueError('OCR构建记录身份或架构不符')
    if record.get('artifactSHA256') != sha(payload) or record.get('sourceSHA256') != sha(read(project/'native/ocr-windows/main.cpp', 1 << 20)):
        raise ValueError('OCR产物/源代码与构建记录不符')
    return record

def prepare(candidate, output, *, identity, publisher, publisher_name, version, arch, logos, allow_missing_ocr=False, project=None):
    source_arg = Path(candidate)
    if source_arg.is_symlink():
        raise ValueError('候选目录不能为链接')
    candidate, output = source_arg.resolve(), Path(output).absolute()
    project = Path(project or Path(__file__).resolve().parent.parent)
    if output.exists() or candidate == output.resolve() or candidate in output.resolve().parents or not candidate.is_dir():
        raise ValueError('需要现有候选和位于其外部的新输出目录')
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9.-]{2,49}', identity) or not publisher.startswith('CN=') or len(publisher) > 1024 or any(ord(c)<32 for c in publisher+publisher_name) or not 1 <= len(publisher_name) <= 256:
        raise ValueError('包身份或Publisher参数无效')
    parts = version.split('.')
    if len(parts) != 4 or any(not re.fullmatch(r'0|[1-9][0-9]{0,4}', p) or int(p)>65535 for p in parts) or arch not in ('x64', 'arm64'):
        raise ValueError('MSIX版本或架构无效')
    if identity.split('.')[0].upper() in {'CON','PRN','AUX','NUL'}:
        raise ValueError('包身份使用保留名')
    images = {size: png(logos[size], size) for size in (44, 50, 150)}
    ocr = None
    if (candidate/'AIVidCutOCR.exe').exists() or (candidate/'ocr-build-info.json').exists():
        ocr = ocr_binding(candidate, project, arch)
    elif not allow_missing_ocr:
        raise ValueError('缺少原生OCR辅助进程和构建记录；未准备可识别候选')
    files, names, total = [], set(), 0
    for directory, directories, filenames in os.walk(candidate, followlinks=False):
        for name in directories+filenames:
            path = Path(directory)/name
            relative = path.relative_to(candidate)
            if path.is_symlink():
                raise ValueError('候选不能包含链接')
            valid_windows_path(relative)
            key = relative.as_posix().casefold()
            if key in names or len(names) >= 10000:
                raise ValueError('候选有大小写冲突或节点超过预算')
            names.add(key)
            if relative.parts[0].casefold() in ('assets', 'appxmanifest.xml'):
                raise ValueError('候选已有MSIX文件，未覆盖')
        for name in filenames:
            path = Path(directory)/name
            data = read(path)
            total += len(data)
            if total > MAX_TOTAL or len(files) >= 8192:
                raise ValueError('候选文件或总量超过预算')
            if path.suffix.casefold() in ('.exe', '.dll', '.pyd'):
                validate_pe(data, arch)
            files.append((path, path.relative_to(candidate), sha(data), len(data)))
    if 'aividcut.exe' not in names:
        raise ValueError('候选缺少AIVidCut.exe')
    output.mkdir(parents=True)
    payload_dir = output/'Payload'
    payload_dir.mkdir()
    # 每个复制结果重新核对，源目录不作修改。
    records = []
    for source, relative, digest, length in files:
        dest = payload_dir/relative
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, dest)
        if sha(read(dest)) != digest:
            raise ValueError('复制期间候选内容变化，保留未完成目录')
        records.append({'path':relative.as_posix(), 'sha256':digest, 'bytes':length})
    assets = payload_dir/'Assets'
    assets.mkdir()
    for size, data in images.items():
        (assets/f'Logo{size}.png').write_bytes(data)
    for prefix, uri in [('',NAMESPACE),('uap',UAP),('uap10',UAP10),('rescap',RESCAP)]:
        ET.register_namespace(prefix,uri)
    package = ET.Element(f'{{{NAMESPACE}}}Package', {'IgnorableNamespaces':'uap uap10 rescap'})
    ET.SubElement(package, 'Identity', {'Name':identity, 'Publisher':publisher, 'Version':version, 'ProcessorArchitecture':arch})
    properties = ET.SubElement(package, 'Properties')
    for key, value in [('DisplayName','AIVidCut'),('PublisherDisplayName',publisher_name),('Description','AI视频剪辑工作台'),('Logo','Assets/Logo50.png')]:
        ET.SubElement(properties,key).text=value
    ET.SubElement(ET.SubElement(package,'Resources'),'Resource',{'Language':'zh-CN'})
    ET.SubElement(ET.SubElement(package,'Dependencies'),'TargetDeviceFamily',{'Name':'Windows.Desktop','MinVersion':'10.0.19041.0','MaxVersionTested':'10.0.19041.0'})
    ET.SubElement(ET.SubElement(package,'Capabilities'),f'{{{RESCAP}}}Capability',{'Name':'runFullTrust'})
    application = ET.SubElement(ET.SubElement(package,'Applications'),'Application',{'Id':'AIVidCut','Executable':'AIVidCut.exe',f'{{{UAP10}}}RuntimeBehavior':'packagedClassicApp',f'{{{UAP10}}}TrustLevel':'mediumIL'})
    ET.SubElement(application,f'{{{UAP}}}VisualElements',{'DisplayName':'AIVidCut','Description':'AI视频剪辑工作台','Square150x150Logo':'Assets/Logo150.png','Square44x44Logo':'Assets/Logo44.png','BackgroundColor':'transparent'})
    manifest = ET.tostring(package,encoding='utf-8',xml_declaration=True)
    (payload_dir/'AppxManifest.xml').write_bytes(manifest)
    (output/'Build-MSIX.ps1').write_text("""# 核对准备快照后调用SDK打包，不签名、不安装或发布。
param([Parameter(Mandatory=$true)][string]$MakeAppx,[Parameter(Mandatory=$true)][string]$OutputFile,[string]$Python='python')
$ErrorActionPreference='Stop'
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw '需要Windows SDK MakeAppx' }
if (Test-Path -LiteralPath $OutputFile) { throw '输出文件已存在，未覆盖' }
& $Python (Join-Path $PSScriptRoot 'Tools/prepare_windows_msix.py') --verify-prepared $PSScriptRoot
if ($LASTEXITCODE -ne 0) { throw '准备目录已变化，未执行MakeAppx' }
& $MakeAppx pack /d (Join-Path $PSScriptRoot 'Payload') /p ([IO.Path]::GetFullPath($OutputFile))
if ($LASTEXITCODE -ne 0) { throw 'MakeAppx未通过，尚无有效MSIX' }
Write-Output 'MSIX已打包，签名、安装、身份和OCR语言仍待验收。'
""",encoding='utf-8-sig')
    generated = [{'path':'AppxManifest.xml','sha256':sha(manifest),'bytes':len(manifest)}]
    generated += [{'path':f'Assets/Logo{size}.png','sha256':sha(data),'bytes':len(data)} for size,data in images.items()]
    tools = output/'Tools'
    tools.mkdir()
    checker = read(Path(__file__),1<<20)
    (tools/'prepare_windows_msix.py').write_bytes(checker)
    report = {'schemaVersion':1,'identity':identity,'publisher':publisher,'version':version,'arch':arch,'manifestSHA256':sha(manifest),'files':records,'generatedFiles':generated,'builderSHA256':sha(read(output/'Build-MSIX.ps1',1<<20)),'checkerSHA256':sha(checker),'sourceBytes':total,'ocr':ocr,'ocrPayloadPresent':ocr is not None,'makeAppxExecuted':False,'signed':False,'installed':False,'runtimeVerified':False,'releaseVerified':False,'limits':['Windows原生SDK编译/MakeAppx/签名/安装未由准备动作验证','MSIX专用最低系统声明19041不是整个产品的最低平台确认','PNG检查签名、尺寸和CRC，不替代视觉验收','本机校验和不替代签名或恶意篡改防护']}
    (output/'preparation.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
    return report

def verify_prepared(directory):
    directory=Path(directory)
    report=json.loads(read(directory/'preparation.json',4<<20))
    if report.get('schemaVersion')!=1:
        raise ValueError('准备清单版本无效')
    expected=report['files']+report['generatedFiles']
    if not expected or len(expected)>8192:
        raise ValueError('准备清单数量无效')
    names=set()
    payload=directory/'Payload'
    if payload.is_symlink():
        raise ValueError('准备目录不能为链接')
    for entry in expected:
        relative=Path(entry['path'])
        if relative.is_absolute() or '..' in relative.parts or '\\' in entry['path'] or entry['path'] in names:
            raise ValueError('准备清单路径无效或重复')
        valid_windows_path(relative)
        path=payload/relative
        for parent in path.parents:
            if parent==payload:break
            if parent.is_symlink():raise ValueError('准备目录父路径为链接')
        data=read(path)
        if sha(data)!=entry['sha256'] or len(data)!=entry['bytes']:
            raise ValueError('待打包文件已变化')
        names.add(entry['path'])
    actual=set()
    nodes=0
    for folder,dirs,files in os.walk(payload,followlinks=False):
        for name in dirs+files:
            nodes+=1
            if nodes>10000:raise ValueError('准备树节点超过预算')
            path=Path(folder)/name
            if path.is_symlink():raise ValueError('准备目录出现链接')
        for name in files:actual.add((Path(folder)/name).relative_to(payload).as_posix())
    if actual!=names or sha(read(directory/'Build-MSIX.ps1',1<<20))!=report['builderSHA256'] or sha(read(directory/'Tools/prepare_windows_msix.py',1<<20))!=report['checkerSHA256']:
        raise ValueError('准备树或打包工具已变化')
    return report

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--verify-prepared')
    for name in ('candidate','output','identity','publisher','publisher-name','version','arch','logo44','logo50','logo150'):
        parser.add_argument('--'+name)
    parser.add_argument('--allow-missing-ocr',action='store_true')
    args = parser.parse_args()
    if args.verify_prepared:
        verify_prepared(args.verify_prepared)
        print('准备快照核对通过，尚未执行MakeAppx')
        return
    if any(getattr(args,name) is None for name in ('candidate','output','identity','publisher','publisher_name','version','arch','logo44','logo50','logo150')):
        parser.error('准备模式需要完整候选、身份、版本、架构和三种图标')
    report = prepare(args.candidate,args.output,identity=args.identity,publisher=args.publisher,publisher_name=args.publisher_name,version=args.version,arch=args.arch,logos={44:args.logo44,50:args.logo50,150:args.logo150},allow_missing_ocr=args.allow_missing_ocr)
    print(json.dumps({key:report[key] for key in ('sourceBytes','ocrPayloadPresent','makeAppxExecuted','runtimeVerified','releaseVerified')}))
if __name__ == '__main__':
    main()
