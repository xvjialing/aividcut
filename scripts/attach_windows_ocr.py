#!/usr/bin/env python3
"""将明确提供、匹配本次源码的Windows OCR产物加入新候选，不执行产物。"""
import argparse
import json
from pathlib import Path
from prepare_windows_msix import ocr_binding, read, sha, validate_pe, MAX_TOTAL

def attach(source, destination, arch):
    source, destination = Path(source), Path(destination)
    if source.is_symlink() or destination.is_symlink() or not destination.is_dir():
        raise ValueError('需要明确的构建目录和新候选目录')
    project = Path(__file__).resolve().parent.parent
    target={'amd64':'x64', 'arm64':'arm64'}[arch]
    validate_pe(read(destination/'AIVidCut.exe'),target)
    record = ocr_binding(source, project, target)
    for name in ('AIVidCutOCR.exe','ocr-build-info.json'):
        if (destination/name).exists():
            raise ValueError('OCR目标已存在，未覆盖')
    binary = read(source/'AIVidCutOCR.exe')
    if sha(binary) != record['artifactSHA256']:
        raise ValueError('复制前OCR产物变化')
    with (destination/'AIVidCutOCR.exe').open('xb') as stream:
        stream.write(binary)
    with (destination/'ocr-build-info.json').open('xb') as stream:
        stream.write(read(source/'ocr-build-info.json',64<<10))
    if ocr_binding(destination,project,record['arch']) != record:
        raise ValueError('复制后OCR记录变化')
    # 原校验清单随新增文件失效，重建本候选的完整文件摘要。
    entries=[]
    total=0
    for path in sorted(destination.rglob('*')):
        if path.is_symlink():raise ValueError('候选不能包含链接')
        if path.is_file() and path.name!='PACKAGE-CONTENTS.sha256':
            data=read(path);total+=len(data)
            if total>MAX_TOTAL or len(entries)>=8192:raise ValueError('候选超过预算')
            entries.append(f'{sha(data)}  {path.relative_to(destination).as_posix()}')
    (destination/'PACKAGE-CONTENTS.sha256').write_text('\n'.join(entries)+'\n',encoding='utf-8')
    return record
if __name__ == '__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    for name in ('source','destination','arch'):
        parser.add_argument('--'+name,required=True)
    args=parser.parse_args()
    record=attach(args.source,args.destination,args.arch)
    print(json.dumps({'engine':record['engine'],'arch':record['arch'],'runtimeVerified':False}))
