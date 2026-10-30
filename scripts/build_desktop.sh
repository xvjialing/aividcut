#!/bin/sh
# 由Wails v3的Taskfile调用，保留独立媒体SDK的构建边界。
set -eu
project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_root"
pnpm --dir frontend run build
mkdir -p build/bin
copy_media_runtime() {
  [ -n "${AIVIDCUT_BUNDLE_MEDIA_RUNTIME:-}" ] || return 0
  # 核验工具在宿主运行，预期平台明确取目标，避免跨编译后误执行Windows程序。
  aividcut_host_os=$(go env GOHOSTOS)
  aividcut_host_arch=$(go env GOHOSTARCH)
  aividcut_target_os=$(go env GOOS)
  aividcut_target_arch=$(go env GOARCH)
  env GOOS="$aividcut_host_os" GOARCH="$aividcut_host_arch" go run ./cmd/media-runtime-manifest -verify -root "$AIVIDCUT_BUNDLE_MEDIA_RUNTIME" -target-os "$aividcut_target_os" -target-arch "$aividcut_target_arch"
  mkdir -p "$1"
  cp -R "$AIVIDCUT_BUNDLE_MEDIA_RUNTIME/." "$1/"
  env GOOS="$aividcut_host_os" GOARCH="$aividcut_host_arch" go run ./cmd/media-runtime-manifest -verify -root "$1" -target-os "$aividcut_target_os" -target-arch "$aividcut_target_arch"
}
case "$(go env GOOS)" in
  darwin)
    export MACOSX_DEPLOYMENT_TARGET=13.0
    export CGO_CFLAGS="${CGO_CFLAGS:-} -mmacosx-version-min=13.0"
    export CGO_LDFLAGS="${CGO_LDFLAGS:-} -mmacosx-version-min=13.0"
    go build -tags production -trimpath -buildvcs=false -ldflags='-s -w' -o build/bin/AIVidCut .
    # 使用新bundle，避免运行中的旧开发窗口与新版本共享可写可执行文件。
    bundle_stage=$(mktemp -d "$project_root/build/bin/.AIVidCut-stage.XXXXXX")
    trap 'rm -rf "$bundle_stage"' EXIT INT TERM
    mkdir -p "$bundle_stage/AIVidCut.app/Contents/MacOS" "$bundle_stage/AIVidCut.app/Contents/Resources"
    cp build/bin/AIVidCut "$bundle_stage/AIVidCut.app/Contents/MacOS/AIVidCut"
    # Swift仅在构建机使用；随包为已编译的本机OCR进程。
    case "$(go env GOARCH)" in
      arm64) aividcut_ocr_arch=arm64 ;;
      amd64) aividcut_ocr_arch=x86_64 ;;
      *) printf '不支持的macOS OCR架构\n' >&2; exit 1 ;;
    esac
    mkdir -p work/swift-ocr-module-cache
    xcrun swiftc -O -module-cache-path "$project_root/work/swift-ocr-module-cache" -target "$aividcut_ocr_arch-apple-macosx13.0" native/ocr/main.swift -o "$bundle_stage/AIVidCut.app/Contents/MacOS/AIVidCutOCR"
    codesign --force --sign - "$bundle_stage/AIVidCut.app/Contents/MacOS/AIVidCutOCR"
    python3 scripts/record_ocr_build.py "$bundle_stage/AIVidCut.app/Contents/MacOS/AIVidCutOCR" "$project_root/native/ocr/main.swift" "$bundle_stage/AIVidCut.app/Contents/Resources/ocr-build-info.json" "$aividcut_ocr_arch-apple-macosx13.0"
    cp build/darwin/Info.plist "$bundle_stage/AIVidCut.app/Contents/Info.plist"
    if [ -n "${AIVIDCUT_BUNDLE_MEDIA_RUNTIME:-}" ]; then
      copy_media_runtime "$bundle_stage/AIVidCut.app/Contents/Resources/media"
    fi
    # 每次从项目源图标生成；首次构建及增量构建都不读取旧应用里的图标。
    work/bin/wails3 generate icons -input build/appicon.png -macfilename "$bundle_stage/AIVidCut.app/Contents/Resources/iconfile.icns" -windowsfilename ""
    python3 scripts/collect_dependency_notices.py --artifact "$bundle_stage/AIVidCut.app/Contents/MacOS/AIVidCut" --project-root "$project_root" --output "$bundle_stage/AIVidCut.app/Contents/Resources/THIRD_PARTY_APPLICATION_NOTICES"
    codesign --force --sign - "$bundle_stage/AIVidCut.app"
    codesign --verify --deep --strict "$bundle_stage/AIVidCut.app"
    # 旧bundle仅是可再生构建产物，不包含用户工程或资料目录。
    rm -rf build/bin/AIVidCut.app.previous
    if [ -d build/bin/AIVidCut.app ]; then mv build/bin/AIVidCut.app build/bin/AIVidCut.app.previous; fi
    mv "$bundle_stage/AIVidCut.app" build/bin/AIVidCut.app
    python3 scripts/collect_dependency_notices.py --artifact "$project_root/build/bin/AIVidCut.app/Contents/MacOS/AIVidCut" --bind-notices "$project_root/build/bin/AIVidCut.app/Contents/Resources/THIRD_PARTY_APPLICATION_NOTICES" --output "$project_root/build/bin/AIVidCut.app-notices-binding.json"
    printf '已构建：build/bin/AIVidCut.app（本地自签名）\n'
    ;;
  windows)
    if [ -n "${AIVIDCUT_WINDOWS_OCR_DIRECTORY:-}" ] && [ -z "${AIVIDCUT_BUNDLE_MEDIA_RUNTIME:-}" ]; then
      printf 'Windows OCR装配需要明确的随包媒体目录，未忽略辅助进程配置。\n' >&2
      exit 1
    fi
    go build -tags production -trimpath -buildvcs=false -ldflags='-s -w -H windowsgui' -o build/bin/AIVidCut.exe .
    if [ -n "${AIVIDCUT_BUNDLE_MEDIA_RUNTIME:-}" ]; then
      aividcut_windows_bundle=$(mktemp -d "$project_root/build/bin/AIVidCut-windows.XXXXXX")
      cp build/bin/AIVidCut.exe "$aividcut_windows_bundle/AIVidCut.exe"
      copy_media_runtime "$aividcut_windows_bundle/media"
      if [ -n "${AIVIDCUT_WINDOWS_OCR_DIRECTORY:-}" ]; then
        python3 scripts/attach_windows_ocr.py --source "$AIVIDCUT_WINDOWS_OCR_DIRECTORY" --destination "$aividcut_windows_bundle" --arch "$(go env GOARCH)"
      fi
      python3 scripts/collect_dependency_notices.py --artifact "$aividcut_windows_bundle/AIVidCut.exe" --project-root "$project_root" --output "$aividcut_windows_bundle/THIRD_PARTY_APPLICATION_NOTICES"
      python3 scripts/collect_dependency_notices.py --artifact "$aividcut_windows_bundle/AIVidCut.exe" --bind-notices "$aividcut_windows_bundle/THIRD_PARTY_APPLICATION_NOTICES" --output "$aividcut_windows_bundle/application-notices-binding.json"
      printf 'Windows随包候选：%s（尚需实机/签名/许可验收）\n' "$aividcut_windows_bundle"
    fi
    printf '已构建：build/bin/AIVidCut.exe；WebView2、安装器和签名仍需Windows实机验收。\n'
    ;;
  *)
    printf '当前交付目标为macOS和Windows。\n' >&2
    exit 1
    ;;
esac
