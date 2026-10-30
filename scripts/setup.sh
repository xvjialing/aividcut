#!/bin/sh
set -eu
project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_root"
if ! command -v pnpm >/dev/null 2>&1; then
  printf '请先准备 package.json 固定的 pnpm 12.15.0（可使用 Corepack），参见 frontend/README.md。\n' >&2
  exit 1
fi
mkdir -p work/bin
go mod download
wails_version=$(go list -m -f '{{.Version}}' github.com/wailsapp/wails/v3)
project_go_toolchain=$(go env GOVERSION)
GOTOOLCHAIN="$project_go_toolchain" GOBIN="$project_root/work/bin" go install "github.com/wailsapp/wails/v3/cmd/wails3@$wails_version"
pnpm --dir frontend install --frozen-lockfile
printf '\n开发依赖已就绪。完整打包的媒体 SDK 准备方式参见 README。\n'
