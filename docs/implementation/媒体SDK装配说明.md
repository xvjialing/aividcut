# 媒体SDK装配说明

当前为显式开发SDK的私有装配入口，不批准许可，不自动下载、安装或发布。macOS依赖本机Qt官方macdeployqt与Apple工具；Windows需独立取得/验证对应SDK，不能使用Mac包代替。用户工程、模型凭据和缓存不进入媒体目录。

## macOS新目录装配

```sh
go build -o work/bin/media-runtime-manifest ./cmd/media-runtime-manifest
python3 scripts/package_macos_media.py --config /absolute/path/package.json
```

复制[示例配置](../../build/media-runtime.macos.example.json)到本地明确填写全部绝对路径。输出必须不存在、父目录已存在，工具不覆盖已有包；失败保留本次隔离stage供核对，不删除原SDK。配置只传固定参数，不执行其中shell代码。macdeployqt/manifest工具路径属于操作者明确选择的本地工具。

装配将worker、FFmpeg/FFprobe、明确的MLT模块及完整MLT运行数据放在media/AIVidCutMedia.app中。数据路径含loader.dict等文件，不能仅拷profiles。Qt官方工具复制framework/插件，然后逐条把全部非系统Mach-O引用转为包内loader相对路径，清理开发机绝对rpath；qt.conf的Prefix相对Contents设为`.`，显式包含worker所需离屏插件。不能解析到包内的依赖或缺目标架构直接失败。

所有改写完成后逐个签名Mach-O，再签嵌套app，最后生成完整runtime.json。MLT `.so`放在Resources中，不能仅依靠codesign --deep自动覆盖。当前只使用本地ad-hoc，不读取用户私钥；Developer ID、公证、Windows签名仍有独立验收。

## 通用清单生成与核验

新Go命令读取不含payload的Manifest规格（ID/版本/OS/架构/入口），流式收集全部文件/内部链接、创建清单，再调用同一个Load核验。v2增加data，旧v1保持兼容；设置文件v2与此SDK清单版本分别维护，Project仍v4。已有runtime.json拒绝覆盖；失败只移除本次创建且身份匹配的文件。可在对应平台生成，工具的Windows编译不代表Windows SDK可运行。

```sh
work/bin/media-runtime-manifest -root /absolute/path/media -spec /absolute/path/manifest-spec.json
work/bin/media-runtime-manifest -verify -root /absolute/path/media -target-os darwin -target-arch arm64
```

完整内容清单包括内部签名文件，后续改写/重签任何内部文件需重新装配生成；SDK目录不放日志、临时文件或用户配置。生成工具不证明DLL/framework架构、动态加载、许可或实际媒体质量；对应实际运行和分发材料仍需核对。

## 日常完整构建

将已装配的 `media` 目录内容放到工程内 `work/media-runtime/<GOOS>-<GOARCH>/`，例如 Apple Silicon Mac 使用 `work/media-runtime/darwin-arm64/runtime.json`。此目录不会提交到源码仓库，也不由 `make setup` 下载。首次取得/装配 SDK 后，日常命令为：

```sh
make build
make run   # macOS：构建成功后打开应用
make open  # macOS：只打开已有应用
```

完整构建优先使用显式 `AIVIDCUT_BUNDLE_MEDIA_RUNTIME`，否则使用固定目录；先按目标 OS/架构核验清单，再调用原 Wails 构建。缺失、损坏或不匹配会失败，不能静默生成少媒体库的完整包。当前本机固定目录由已核验的私有开发 SDK 复制，未重新下载或修改媒体文件。

当前 Mac SDK 为 arm64，部分 Mach-O 最低部署版本为 macOS 26，不能因主程序声明 macOS 13 推断整包兼容旧系统。此改动仅简化构建入口，不改变 SDK 内容、许可或实际兼容范围。详见[快捷命令说明与验收](2026-10-30-完整打包与启动快捷命令.md)。

## 显式覆盖与底层Wails构建

```sh
AIVIDCUT_BUNDLE_MEDIA_RUNTIME=/absolute/path/package/media ./scripts/build.sh
```

显式变量也可用于 `make build`，用于切换其它已装配的 SDK。底层 `./scripts/build.sh` / `make build-app` 只在明确提供变量时拷贝，保留框架/交叉编译检查用途；没有变量时不带媒体库。源与目标都用宿主工具按构建目标 OS/架构核验清单。Mac 先进入新 `.app/Contents/Resources/media`，再签外层；Windows 保留原独立 exe，同时生成新的 `build/bin/AIVidCut-windows.*` 候选目录，避免覆盖未知目录。

这个输入仍需已经完成对应平台的媒体装配。把Mac SDK给Windows目标会被拒绝，不靠交叉编译冒充DLL部署；Windows需使用其部署工具和真实DLL/插件再验。当前SDK仍为已观察的GPL/Qt组合，完整许可/对应源码/专利及商业分发安排保留待审。
