#!/bin/sh
set -eu
aividcut_project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$aividcut_project_root"
. "$aividcut_project_root/scripts/native_sdk_env.sh"
if [ "${1:-}" = '--help' ]; then
  printf '%s\n' '显式配置AIVIDCUT_MLT_SDK或PKG_CONFIG_PATH后，构建本项目worker到work/bin，并准备固定的开发repository/profiles symlink。'
  printf '%s\n' '可选：AIVIDCUT_FFMPEG_SDK、AIVIDCUT_QT_SDK、AIVIDCUT_CMAKE、AIVIDCUT_PKG_CONFIG。不会安装系统依赖或复制第三方binary。'
  exit 0
fi
[ "$#" -eq 0 ] || aividcut_native_fail '本脚本不接受额外参数，请使用明确SDK环境变量。'
rm -f "$aividcut_project_root/work/bin/.aividcut-native-worker-built"
aividcut_native_sdk_environment
aividcut_cmake=$(command -v "${AIVIDCUT_CMAKE:-cmake}") || aividcut_native_fail '需要CMake 3.24或更新；可通过AIVIDCUT_CMAKE指定工具。'
case "$("$aividcut_cmake" --help)" in
  *--fresh*) ;;
  *) aividcut_native_fail '开发脚本需要CMake 3.24+的--fresh，避免切换SDK后误用旧CMake缓存。' ;;
esac
"$aividcut_pkg_config" --exists libavformat libavcodec libavutil libavfilter libswresample libswscale || aividcut_native_fail '需要与MLT兼容的FFmpeg解码、滤镜、重采样与图像转换开发包。'
mkdir -p "$aividcut_project_root/work/bin"
"$aividcut_cmake" --fresh -S "$aividcut_project_root/native/mlt-worker" -B "$aividcut_project_root/work/mlt-worker-build" \
  -DCMAKE_BUILD_TYPE=Release \
  -DPKG_CONFIG_EXECUTABLE="$aividcut_pkg_config" \
  -DCMAKE_RUNTIME_OUTPUT_DIRECTORY="$aividcut_project_root/work/bin" \
  -DCMAKE_RUNTIME_OUTPUT_DIRECTORY_RELEASE="$aividcut_project_root/work/bin"
"$aividcut_cmake" --build "$aividcut_project_root/work/mlt-worker-build" --config Release --target aividcut-mlt-worker --parallel 2
aividcut_worker="$aividcut_project_root/work/bin/aividcut-mlt-worker"
if [ ! -x "$aividcut_worker" ] && [ -x "$aividcut_worker.exe" ]; then
  aividcut_worker="$aividcut_worker.exe"
fi
[ -x "$aividcut_worker" ] || aividcut_native_fail '构建没有生成预期的本项目worker。'
"$aividcut_project_root/scripts/prepare_native_repository.sh"
touch "$aividcut_project_root/work/bin/.aividcut-native-worker-built"
printf '本项目worker：%s\nMLT SDK版本：%s\n' "$aividcut_worker" "$("$aividcut_pkg_config" --modversion mlt-framework-7)"
printf '%s\n' '开发构建完成；真实插件、原生播放、Windows和发行许可仍以各自验收为准。'
