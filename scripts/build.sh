#!/bin/sh
set -eu
project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_root"
for argument in "$@"; do
  case "$argument" in
    ARCH=*) printf 'Wails v3 build请使用GOARCH=amd64或GOARCH=arm64；ARCH参数会被CLI的宿主默认值覆盖。\n' >&2; exit 1 ;;
  esac
done
if [ ! -x work/bin/wails3 ]; then
  printf '请先运行 make setup，安装固定版本的工程本地构建工具。\n' >&2
  exit 1
fi
wails_version=$(go list -m -f '{{.Version}}' github.com/wailsapp/wails/v3)
if [ "$(work/bin/wails3 version 2>&1)" != "$wails_version" ]; then
  printf '工程Wails模块与CLI版本不一致，请运行make setup。\n' >&2
  exit 1
fi
exec work/bin/wails3 build "$@"
