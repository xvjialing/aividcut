#!/usr/bin/env python3
"""记录自有OCR辅助进程、源代码和Apple构建工具，不能替代发行许可核查。"""
import hashlib
import json
from pathlib import Path
import subprocess
import sys

artifact, source, output, target = map(str, sys.argv[1:])
def command(args):
    result = subprocess.run(args, check=True, capture_output=True, timeout=30)
    if len(result.stdout) > 1 << 20:
        raise ValueError('构建工具输出超过预算')
    return result.stdout.decode().strip()
report = {
    'schemaVersion': 1,
    'component': 'AIVidCutOCR',
    'source': 'native/ocr/main.swift',
    'sourceSHA256': hashlib.sha256(Path(source).read_bytes()).hexdigest(),
    'artifactSHA256': hashlib.sha256(Path(artifact).read_bytes()).hexdigest(),
    'target': target,
    'swiftCompiler': command(['xcrun', 'swiftc', '--version']),
    'appleSDKVersion': command(['xcrun', '--show-sdk-version']),
    'imports': command(['otool', '-L', artifact]).splitlines()[1:],
    'recognition': {'engine': 'apple-vision', 'revision': 3, 'languages': ['zh-Hans', 'zh-Hant', 'en-US']},
    'limitations': ['最低系统为构建目标，实际兼容仍需对应设备验证', '不随包分发Swift编译器或Apple系统框架', '此记录不构成整个产品的商业发行许可批准'],
}
Path(output).write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
