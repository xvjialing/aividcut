#!/bin/sh
# 日常完整构建：优先显式覆盖，否则读取工程内对应平台的固定媒体目录。
set -eu
project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_root"
if [ "$#" -ne 0 ]; then
  printf '完整构建请通过 GOOS/GOARCH 环境变量选择目标平台。\n' >&2
  exit 1
fi
aividcut_target_os=$(go env GOOS)
aividcut_target_arch=$(go env GOARCH)
case "$aividcut_target_os" in
  darwin|windows) ;;
  *) printf '当前完整构建目标为 macOS 和 Windows。\n' >&2; exit 1 ;;
esac
if [ -z "${AIVIDCUT_BUNDLE_MEDIA_RUNTIME:-}" ]; then
  AIVIDCUT_BUNDLE_MEDIA_RUNTIME="$project_root/work/media-runtime/$aividcut_target_os-$aividcut_target_arch"
fi
if [ ! -f "$AIVIDCUT_BUNDLE_MEDIA_RUNTIME/runtime.json" ]; then
  printf '未找到已装配的媒体运行库：%s\n' "$AIVIDCUT_BUNDLE_MEDIA_RUNTIME" >&2
  printf '请将包含 runtime.json 的 media 目录放到该位置，或设置 AIVIDCUT_BUNDLE_MEDIA_RUNTIME。\n' >&2
  printf '装配说明：docs/implementation/媒体SDK装配说明.md；仅检查应用编译可用 make build-app。\n' >&2
  exit 1
fi
export AIVIDCUT_BUNDLE_MEDIA_RUNTIME
# 前端编译前核验，避免 SDK 缺失、损坏或目标不匹配时生成不完整应用。
aividcut_host_os=$(go env GOHOSTOS)
aividcut_host_arch=$(go env GOHOSTARCH)
env GOOS="$aividcut_host_os" GOARCH="$aividcut_host_arch" go run ./cmd/media-runtime-manifest -verify -root "$AIVIDCUT_BUNDLE_MEDIA_RUNTIME" -target-os "$aividcut_target_os" -target-arch "$aividcut_target_arch"
printf '完整构建使用媒体运行库：%s\n' "$AIVIDCUT_BUNDLE_MEDIA_RUNTIME"
exec ./scripts/build.sh
