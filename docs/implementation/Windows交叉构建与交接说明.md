# Windows交叉构建与交接说明

适用于macOS宿主上的明确MinGW UCRT工具链。无需在Mac执行Windows程序；现有[Windows原生构建入口](Windows媒体构建与验收说明.md)继续保留。

## 准备输入

需要Python 3.9+、宿主pkg-config/zstd、能够实际运行的x86_64-w64-mingw32 UCRT C++编译器及其sysroot。可通过[Homebrew官方mingw-w64配方](https://formulae.brew.sh/formula/mingw-w64)准备宿主工具；编译器不包含在客户端包内。本轮使用14.0.0_3/GCC16.2.0。不要直接解压未重定位的Homebrew bottle就视为已安装：本轮试验曾遇到Mach-O占位依赖、归档硬链接和COFF对象中的占位字符串，均只在work副本中处理并通过真实UCRT链接探针；系统工具链未改。常规交接优先使用已正确安装/重定位的工具链。

运行库按[固定包锁说明](Windows运行库包锁说明.md)重放，取得runtime/ucrt64。与它使用同一份build/windows-runtime.packages.lock.json提取最小开发前缀：

```sh
python3 scripts/resolve_msys2_runtime.py --zstd /absolute/path/to/zstd apply \
  --profile development --lock build/windows-runtime.packages.lock.json \
  --cache /absolute/path/to/package-cache --output /absolute/path/to/new-dev-sdk
```

输出new-dev-sdk/ucrt64，含头文件和Windows导入库，不是可在Windows运行CMake的完整SDK。本轮四包3366文件、42866488字节。下载缓存仍按锁大小与SHA核对；官方包PGP尚未验证。默认不加profile仍只取得原运行文件。

## 完整worker编译和SDK装配

先创建候选输出的父目录，build目录与package-output自身必须不存在。以下均替换为明确绝对路径：

```sh
python3 scripts/build_windows_media_cross.py \
  --compiler /absolute/path/to/x86_64-w64-mingw32-g++ \
  --pkg-config /absolute/path/to/pkg-config \
  --sysroot /absolute/path/to/mingw-toolchain \
  --sdk-root /absolute/path/to/new-dev-sdk/ucrt64 \
  --runtime-sdk-root /absolute/path/to/runtime/ucrt64 \
  --build-directory /absolute/path/to/new-worker-build \
  --package-output /absolute/path/to/new-windows-package

go run ./cmd/windows-media-package \
  -config /absolute/path/to/new-worker-build/windows-runtime-config.json
go run ./cmd/windows-media-package \
  -config /absolute/path/to/new-worker-build/windows-runtime-config.json -assemble
```

编译脚本不安装工具、不执行目标PE，需审阅输入工具来源。成功保留cross-build-report.json和commands.json；失败保留分步日志。原生头文件/导入库必须同UCRT64且兼容所选GCC/Qt ABI。静态名称闭包不能证明全部导出符号、计算名称LoadLibrary或驱动可用。

注意：装配器返回new-windows-package候选根；真正SDK在new-windows-package/media。交叉脚本stdout明确给出packageOutput和mediaRoot，避免多套一层media后传错路径。

## Wails随包构建

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  AIVIDCUT_BUNDLE_MEDIA_RUNTIME=/absolute/path/to/new-windows-package/media \
  ./scripts/build.sh
```

入口先在宿主核验Windows目标清单，再编译Windows程序并复制到新的build/bin/AIVidCut-windows.*目录，再核对一次清单。此目录含AIVidCut.exe与相邻media。不会替换macOS app。Wails模块/CLI/npm runtime均为项目固定3.0.0。

如需独立Windows清单工具：

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build \
  -trimpath -buildvcs=false -ldflags='-s -w' \
  -o /absolute/path/to/media-runtime-manifest.exe ./cmd/media-runtime-manifest
```

## 后续Windows接手

本轮交接zip与目录记录在[验收报告](2026-10-27-Windows交叉构建与交接包验收.md)，包含核验工具、151份实际许可文本及固定包锁。包中没有编译器、登录密钥、用户素材、Node开发依赖或SDK安装脚本。

将zip完整解压到可写的新目录。先在PowerShell运行：

```powershell
.\tools\media-runtime-manifest.exe -verify -root "$PWD\media" -target-os windows -target-arch amd64
```

这是内容核验，不能代替下面的功能实测。双击AIVidCut.exe，确认WebView2和启动、媒体工具来源为随包；导入一段本机有声视频及图片，试播放/暂停/Seek、多轨层级、文字/字幕、音量/淡化和.cube。保存重开再导出，核对画面、字幕、LUT与音尾；更换含中文/空格的目录再次验证。记录Windows版本、硬件、WebView2、包SHA和失败日志。再测取消、长素材、峰值资源和声画同步。现阶段没有机器，以上全部继续标待验，不作已通过记录。

当前zip是内部开发候选。许可证文本与包锁有助于追溯，但完整源码提供义务、Qt/FFmpeg/GPL商业分发路径、Windows代码签名/安装更新与系统授权尚未完成审查。
