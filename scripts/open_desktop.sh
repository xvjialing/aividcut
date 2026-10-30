#!/bin/sh
# 启动已构建的本机客户端；不启动浏览器开发服务。
set -eu
project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if [ "$(uname -s)" != Darwin ]; then
  printf '当前快捷启动入口仅支持 macOS；Windows 请运行对应交付目录中的 AIVidCut.exe。\n' >&2
  exit 1
fi
if [ ! -x "$project_root/build/bin/AIVidCut.app/Contents/MacOS/AIVidCut" ]; then
  printf '尚未找到 macOS 应用，请先运行 make build，或用 make run 完整构建并启动。\n' >&2
  exit 1
fi
exec /usr/bin/open "$project_root/build/bin/AIVidCut.app"
