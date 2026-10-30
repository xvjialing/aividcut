#!/bin/sh
set -eu
aividcut_project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$aividcut_project_root"
# 固定读取工程自身辅助函数，不执行SDK元数据或用户配置中的命令。
. "$aividcut_project_root/scripts/native_sdk_env.sh"
if [ "${1:-}" = '--help' ]; then
  printf '%s\n' '由显式AIVIDCUT_MLT_SDK/PKG_CONFIG_PATH生成work/mlt-selected-repository与work/mlt-profiles开发symlink。'
  exit 0
fi
[ "$#" -eq 0 ] || aividcut_native_fail '本脚本不接受路径参数，请使用明确的SDK环境变量。'
aividcut_native_sdk_environment

aividcut_modules=$("$aividcut_pkg_config" --variable=moduledir mlt-framework-7)
aividcut_data=$("$aividcut_pkg_config" --variable=mltdatadir mlt-framework-7)
[ -d "$aividcut_modules" ] || aividcut_native_fail 'SDK的moduledir不存在。'
[ -d "$aividcut_data/profiles" ] || aividcut_native_fail 'SDK的mltdatadir/profiles不存在。'
aividcut_modules=$(CDPATH= cd -- "$aividcut_modules" && pwd -P)
aividcut_profiles_source=$(CDPATH= cd -- "$aividcut_data/profiles" && pwd -P)
mkdir -p "$aividcut_project_root/work/bin"
aividcut_repository="$aividcut_project_root/work/mlt-selected-repository"
aividcut_profiles="$aividcut_project_root/work/mlt-profiles"
aividcut_ready="$aividcut_project_root/work/bin/.aividcut-native-worker-built"
rm -f "$aividcut_ready"

# 已有实体文件或未知内容不能被开发脚本悄悄覆盖。
[ ! -L "$aividcut_repository" ] || aividcut_native_fail 'work/mlt-selected-repository已有目录symlink，未改写。'
if [ -e "$aividcut_repository" ]; then
  [ -d "$aividcut_repository" ] || aividcut_native_fail 'work/mlt-selected-repository不是目录。'
  for aividcut_entry in "$aividcut_repository"/* "$aividcut_repository"/.[!.]* "$aividcut_repository"/..?*; do
    [ -e "$aividcut_entry" ] || [ -L "$aividcut_entry" ] || continue
    aividcut_name=${aividcut_entry##*/}
    case "$aividcut_name" in
      libmltcore.*|libmltavformat.*|libmltqt6.*|libmltxml.*|libmltplus.*|libmltnormalize.*|libmltresample.*)
        [ -L "$aividcut_entry" ] || aividcut_native_fail '选定repository存在实体模块文件，未覆盖。' ;;
      *) aividcut_native_fail '选定repository存在未知内容，未覆盖；请保留后自行选择处理方式。' ;;
    esac
  done
fi
if [ -e "$aividcut_profiles" ] || [ -L "$aividcut_profiles" ]; then
  [ -L "$aividcut_profiles" ] || aividcut_native_fail 'work/mlt-profiles已有实体文件或目录，未覆盖。'
fi

aividcut_stage=$(mktemp -d "$aividcut_project_root/work/.mlt-repository-XXXXXX")
aividcut_previous="${aividcut_stage}.previous"
aividcut_profile_stage="${aividcut_stage}.profiles"
trap 'rm -rf "$aividcut_stage"; rm -f "$aividcut_profile_stage"; if [ -d "$aividcut_previous" ] && [ ! -e "$aividcut_repository" ]; then mv "$aividcut_previous" "$aividcut_repository"; fi' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
for aividcut_module in core avformat qt6 xml plus normalize resample; do
  aividcut_source=''
  for aividcut_suffix in so dylib dll; do
    aividcut_candidate="$aividcut_modules/libmlt$aividcut_module.$aividcut_suffix"
    if [ -f "$aividcut_candidate" ]; then
      aividcut_source=$aividcut_candidate
      break
    fi
  done
  [ -n "$aividcut_source" ] || aividcut_native_fail "SDK缺少本轮已验证的模块：${aividcut_module}；未改写原repository。"
  ln -s "$aividcut_source" "$aividcut_stage/${aividcut_source##*/}"
done
ln -s "$aividcut_profiles_source" "$aividcut_profile_stage"
if [ -d "$aividcut_repository" ]; then
  mv "$aividcut_repository" "$aividcut_previous"
fi
mv "$aividcut_stage" "$aividcut_repository"
# 先移除已确认的symlink，避免mv跟随它进入SDK目录。
if [ -L "$aividcut_profiles" ]; then
  rm "$aividcut_profiles"
fi
mv "$aividcut_profile_stage" "$aividcut_profiles"
if [ -d "$aividcut_previous" ]; then
  rm -rf "$aividcut_previous"
fi
printf '开发repository：%s\n开发profiles：%s\n' "$aividcut_repository" "$aividcut_profiles"
printf '%s\n' '仅引用明确SDK的模块/数据，没有复制第三方binary；不代表商业发行BOM或许可已通过。'
