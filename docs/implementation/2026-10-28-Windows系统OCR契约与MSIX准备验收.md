# Windows 系统 OCR 契约与 MSIX 准备验收

2026-10-28，FW-R04/FW-R11/FW-R12/FW-R14，P2/P5。对应[规格](../specs/2026-10-28-Windows系统OCR与MSIX交接.md)及[Windows 操作交接](Windows系统OCR构建与MSIX交接说明.md)。本轮完成适配代码、共享契约与准备工具；**Windows 原生 helper 尚未编译、MSIX 尚未由 MakeAppx 打包/签名安装、Windows 识别尚未执行**。不据此完成整项需求。

## 实现

新增独立 C++/WinRT helper：只接收有界源 JPEG，使用系统用户语言配置中的 OCR，返回实际语言；主程序和 helper 均检查包身份。微软[明确要求包身份](https://learn.microsoft.com/en-us/uwp/api/windows.media.ocr?view=winrt-26100)，因此便携方式如实不可用。没有调用系统语言包下载，也没有身份、签名或脚本策略绕过。

Go Worker 共用受控进程、JPEG/JSON、超时/取消、源文件和工程核对；Windows 隐藏控制台并保留进程 Job。结果必须与预期引擎一致，Windows 未公开的模型修订/置信度保持未知，不能用零或 Apple 来源代替。核对窗口展示实际引擎与语言；没有置信度时说明原因，不展示虚假百分比。

接纳继续调用统一命令、检索、回看和撤销。工程升级 v7，v1–v6 只在内存迁移，Windows 来源与方法绑定；旧版本夹带新来源拒绝。原 Apple Vision 的来源和数字置信度保持。

VS 构建入口记录 helper/源码哈希及工具链。可选装配验证目标架构、源码及产物匹配，不覆盖已有 helper，并重建候选校验清单。MSIX 准备工具接受明确的身份/Publisher、版本与图标，复制/核对完整候选，拒绝链接、路径、大小写冲突、不同架构 PE 和坏 PNG。准备后生成的 MakeAppx 入口先复核快照；文件、清单或工具改变时拒绝继续。准备本身不签名、安装或发布。

## 已有证据

- 64 个前端文件、453 项通过；新增 Windows 真实来源/语言/未知置信度展示及无 MSIX 不启动两项 DOM 回归。类型、格式和严格 UI 静态检查通过。
- 新增 5 项 Go 回归覆盖包身份与相邻发现、空置信度、禁止伪造修订/引擎、错误映射、Windows 来源接纳保存重开、v7 兼容。应用/编辑/OCR/存储/版本/恢复/收集/内容索引/MCP 相关竞态通过。Windows 目标主程序实际交叉编译通过，包含新系统 API 调用；不等于已在 Windows 运行。
- 5 项 Python 测试覆盖清单身份转义/权限、缺 helper 的显式不完整模式、架构与源哈希、链接/嵌套目录/PNG CRC、装配和禁止覆盖，以及快照修改/增加文件后的拒绝。这里的最小 PE 是测试夹具，不是编译成功的 OCR 程序。
- 对本轮真实 Windows 候选的 767 文件、290949710 字节执行准备和完整快照复核通过。为了验证未具备 helper 的目录流程，明确使用 `--allow-missing-ocr`、夹具身份及夹具图标；`ocrPayloadPresent=false`、`makeAppxExecuted=false`。该目录不能称为 MSIX 发行物或可识别候选。
- 已有官方 PowerShell 运行时仅解析原生构建、图标生成及实际生成的 MakeAppx 入口，语法通过；没有在 Mac 上假装执行 WinRT/MSVC/System.Drawing。
- Mac 的真实 Apple Vision 继续通过中英文、空白及逐秒视频的第 2 秒识别，读到 `SOURCE SECOND 2`；源位置、接纳、检索和存盘保持。原生最终 v7 构建实际读取上一轮 v6 工程、识别并放弃，修订仍为 5，原 v6 文件字节未改写，实例正常关闭。[原生截图](../../work/windows-ocr-check/native-v7-apple-review.png)。

## 交接产物

[Windows OCR 构建包](../../work/windows-ocr-check/Windows-OCR-Build-Kit.zip)包含自有 C++ 桥、构建/图标/装配/MSIX 准备工具、完整说明、现有工程图标和自有中英文/空白样本。没有 Windows SDK、证书或未经编译的伪 helper 二进制。

[最新 Windows 客户端候选](../../work/windows-ocr-check/AIVidCut-windows-amd64-ocr-contract.zip)保留固定媒体 SDK、应用依赖声明、诊断工具，另补支持 OCR 角色的 Windows `release-audit.exe` 和两份相对路径配置；767 文件，ZIP CRC 通过。helper 未附带，便携状态继续明确不可用。Mac 正式包仍在 `build/bin/AIVidCut.app`，本地签名及 OCR 源/产物绑定匹配。精确哈希和日志见[数字证据](2026-10-28-Windows系统OCR契约与MSIX准备证据.json)。

Wails [官方最新入口](https://github.com/wailsapp/wails/releases/latest)本轮仍指向 [v3.0.0](https://github.com/wailsapp/wails/releases/tag/v3.0.0)。SDK、既有多轨、字幕配音、LUT、生成模型、两种 CLI 与 Skills 目标均保持。静态发行审计仍 Mac 0 blocker/3 review、Windows 0/30，`releaseVerified=false`。

## 待完成

原生 Windows 编译和头文件/API 检查、安装包身份及其子进程继承、系统语言、真实识别/中文路径/取消、MSIX 重定向后的资料与媒体发现，仍按交接说明等待用户有机器后执行。包 Publisher、证书和正式分发方式仍需用户在实际发行前确认；本轮没有代填公司身份或证书。完整许可（含原 npm 文本缺项）、账户、Claude 策略、Windows 实机及目标设备性能不因代码/目录准备完成而解除。
