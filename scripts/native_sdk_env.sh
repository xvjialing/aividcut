#!/bin/sh
# 仅供本项目原生开发脚本读取；不加载SDK或用户提供的shell代码。

aividcut_native_fail() {
  printf '%s\n' "$*" >&2
  exit 1
}

aividcut_native_sdk_environment() {
  if [ -z "${AIVIDCUT_MLT_SDK:-}" ] && [ -z "${PKG_CONFIG_PATH:-}" ]; then
    aividcut_native_fail '请显式设置 AIVIDCUT_MLT_SDK 或 PKG_CONFIG_PATH，脚本不会自动寻找Homebrew或其它剪辑软件。'
  fi
  if [ -n "${AIVIDCUT_MLT_SDK:-}" ]; then
    [ -d "$AIVIDCUT_MLT_SDK/lib/pkgconfig" ] || aividcut_native_fail 'MLT SDK缺少lib/pkgconfig目录。'
    PKG_CONFIG_PATH="$AIVIDCUT_MLT_SDK/lib/pkgconfig${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
  fi
  if [ -n "${AIVIDCUT_FFMPEG_SDK:-}" ]; then
    [ -d "$AIVIDCUT_FFMPEG_SDK/lib/pkgconfig" ] || aividcut_native_fail 'FFmpeg SDK缺少lib/pkgconfig目录。'
    PKG_CONFIG_PATH="$AIVIDCUT_FFMPEG_SDK/lib/pkgconfig${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
  fi
  if [ -n "${AIVIDCUT_QT_SDK:-}" ]; then
    [ -d "$AIVIDCUT_QT_SDK" ] || aividcut_native_fail 'Qt SDK路径不存在。'
    CMAKE_PREFIX_PATH="$AIVIDCUT_QT_SDK${CMAKE_PREFIX_PATH:+:$CMAKE_PREFIX_PATH}"
    export CMAKE_PREFIX_PATH
  fi
  export PKG_CONFIG_PATH
  aividcut_pkg_config=$(command -v "${AIVIDCUT_PKG_CONFIG:-pkg-config}") || aividcut_native_fail '需要pkg-config；可通过AIVIDCUT_PKG_CONFIG指定工具。'
  "$aividcut_pkg_config" --exists 'mlt-framework-7 >= 7.30' || aividcut_native_fail '明确选择的pkg-config搜索路径中没有MLT 7.30或更新SDK。'
}
