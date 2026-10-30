# Windows 系统 OCR 构建与 MSIX 交接

2026-10-28。本次提供源码、共享协议和可执行的准备入口；当前没有 Windows 机器，**尚未完成原生 C++/WinRT 编译、MakeAppx、签名安装或真实 Windows OCR**。不要将 Go 交叉构建或协议夹具当作这些验收。现有便携客户端继续支持其它已实现功能，系统 OCR 仅在包身份与原生组件齐备后可用。

## 原因与边界

微软 [Windows.Media.Ocr](https://learn.microsoft.com/en-us/uwp/api/windows.media.ocr?view=winrt-26100)要求桌面应用具有包身份。原生辅助进程和 Go 客户端均核对身份；不通过临时改环境变量或 PowerShell 执行策略绕过。Windows 识别器按系统用户语言配置选取已安装语言，结果显示实际语言；模型修订和逐行置信度未公开时保持未知。

工程当前为 v7。v1–v6 只在内存迁移，明确保存后旧客户端不能读取新格式；新 Windows 来源不会写成 Apple Vision。两个引擎复用源 JPEG、核对、取消、接纳、撤销、检索和保存。Windows Native helper 无需 Qt、Python 或 .NET 运行时；Python、VS/SDK 只用于构建机。进程树控制和包身份不是对辅助进程的完整权限沙箱。

## 构建辅助进程

需要原生 Windows、VS 2022 的 Desktop development with C++、Windows SDK（建议 10.0.19041 或更新）和明确的 x64 Developer PowerShell。本次客户端/媒体交接产物为 amd64；arm64 参数仍需匹配架构的整套客户端与 SDK 验证。

```powershell
./scripts/build_windows_ocr.ps1 -OutputDirectory ./work/ocr-native
```

输出 `AIVidCutOCR.exe`、`ocr-build-info.json` 和日志。编译脚本不运行辅助进程、不签名、不安装。`/MT`只适用于这个独立辅助进程，MLT/Qt 媒体链路继续使用既有 UCRT64 构建。

源目录/构建目录路径可含空格。输出必须为新目录；失败保留日志。构建记录绑定 C++ 源码和最终 PE 哈希，目标架构、VS/SDK 元信息会一并保存；这是本机证据，不是防恶意篡改的签名。

## 将组件加入候选

先将提供的 Windows 候选完整复制到一个新的工作目录（例如 `work/candidate-with-ocr`），再执行：

```powershell
python ./scripts/attach_windows_ocr.py --source ./work/ocr-native --destination ./work/candidate-with-ocr --arch amd64
```

入口检查客户端/辅助进程 PE 架构、来源和哈希，不覆盖已有 OCR 文件，并重建 `PACKAGE-CONTENTS.sha256`。从完整仓库构建时，也可设置 `AIVIDCUT_WINDOWS_OCR_DIRECTORY` 指向该目录，再运行已有 Windows 随包构建；仍须提供 `AIVIDCUT_BUNDLE_MEDIA_RUNTIME`。

该目录尚未获得包身份，直接运行仍应显示“需要 MSIX 包身份”，不是识别失败。不要为此取消身份检查。

## 准备 MSIX

用现有工程图标生成 MSIX 尺寸，不修改原图：

```powershell
./scripts/make_windows_msix_logos.ps1 -SourcePath ./build/appicon.png -OutputDirectory ./work/msix-logos
```

准备脚本需要 Python 3.9+。以下 Name、Publisher 和 PublisherDisplayName 是需要替换的参数示例，Publisher 必须匹配后续证书主体；工具不替用户注册身份、创建/导入证书或调整系统信任。

```powershell
python ./scripts/prepare_windows_msix.py --candidate ./work/candidate-with-ocr --output ./work/msix-ready --identity Your.Company.AIVidCut --publisher "CN=Your Certificate Subject" --publisher-name "Your Company" --version 0.1.0.0 --arch x64 --logo44 ./work/msix-logos/Logo44.png --logo50 ./work/msix-logos/Logo50.png --logo150 ./work/msix-logos/Logo150.png
```

生成 `Payload/AppxManifest.xml`、完整复制文件、构建入口及准备快照。权限仅声明桌面应用 `runFullTrust`，没有新增相机、麦克风、管理员或启动任务。此 MSIX 入口声明 Windows 10 2004/19041+，不代表整个产品的最低系统或该系统已实测。

默认缺少原生 OCR 会拒绝准备。`--allow-missing-ocr`仅用于明确的清单/便携主体检查，不产生可识别候选。测试生成的单色图标属于验证夹具，不能冒充产品视觉验收。

准备后可在任一构建机核对快照：

```powershell
python ./scripts/prepare_windows_msix.py --verify-prepared ./work/msix-ready
```

在 Windows SDK 构建机打包：

```powershell
./work/msix-ready/Build-MSIX.ps1 -MakeAppx "<Windows SDK>/x64/makeappx.exe" -OutputFile ./work/AIVidCut-unsigned.msix
```

入口在打包前核对完整准备树、清单、图标和工具哈希，改变后需要重新准备。工具不覆盖已有输出，不签名、安装或发布。遵循组织现有的脚本执行策略；不使用执行策略绕过参数。

## 后续实机验收

由用户选择匹配身份的证书及安装/分发方式，核对签名后安装。从已安装包入口启动，确认主程序及子进程身份继承；直接从便携 ZIP 或资源管理器启动另一副本不能代替验证。首次测试用自有中英文/空白图片：显示实际识别语言、无文字空态、原始置信度未知、人工修正接纳、Undo、v7 保存重开和内容检索。缺语言时由用户通过 Windows 语言设置安装所需语言，不自动下载系统语言包。

还需验证原生 helper 编译、MTA/WinRT 初始化、目标设备系统版本、子进程 Job/取消/退出、中文路径、MSIX 重定向后的资料目录和媒体 SDK 发现；然后复验既有视频/音轨/字幕/LUT/模型/Agent/导出。Helper `--probe` 也要求身份，不能把在未安装目录中返回身份缺失记作 WinRT 已坏。

独立发行检查可在用户 Windows 机器运行交接包内 `tools/release-audit.exe`，给配置增加 `ocr` 角色和实际 helper 路径；静态检查仍不替代 MSIX、许可与真实媒体验收。当前 Qt/MLT/FFmpeg、签名、源码/重链接义务及真实账户门槛继续保留。

官方依据：[包身份检测](https://learn.microsoft.com/en-us/windows/win32/api/appmodel/nf-appmodel-getcurrentpackagefullname)、[OcrEngine](https://learn.microsoft.com/en-us/uwp/api/windows.media.ocr.ocrengine?view=winrt-26100)、[手工生成 MSIX 组件](https://learn.microsoft.com/en-us/windows/msix/desktop/desktop-to-uwp-manual-conversion)。
