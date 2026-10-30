# Windows 系统 OCR 与 MSIX 交接

2026-10-28，FW-R04/FW-R11/FW-R12/FW-R14，P2/P5。沿用用户“暂时没有 Windows 机器，先完成可交接构建链路”的安排；不冒充本机 Windows 执行或识别质量验收。

Windows 系统 Windows.Media.Ocr [官方要求包身份](https://learn.microsoft.com/en-us/uwp/api/windows.media.ocr?view=winrt-26100)。新增独立 C++/WinRT 辅助进程、MSVC/Windows SDK 构建脚本、包身份检查与 MSIX 准备入口；现有便携 ZIP 保持可用，未具备包身份、辅助进程或语言时如实不可用。不绕过身份、签名、安装或系统语言包权限，也不自动安装依赖。

辅助进程共用 JPEG stdin→有界 JSON、取消与源核对契约，不复制编辑器。Windows 返回系统实际采用的识别语言；API 未提供置信度或模型修订时明确未知，不能填零冒充。运行中只使用系统已装 OCR，不上传图片，不把结果直接写时间线。

接纳使用真实 windows-media-ocr 来源/方法；工程升级 v7，v1–v6 只在内存升级，旧版本夹带新来源拒绝，保存前原文件不改。Apple Vision 既有来源与结果保持，新增适配器可扩展注册和核对而不复制整个工作区。

构建链路必须核对目标 PE 架构、辅助进程与源代码/构建材料哈希，明确要求原生 Windows 和 VS Developer PowerShell；MSIX 的 Publisher/身份/证书由明确参数提供，默认不签名、不安装、不发布。准备出的资料不能等同于通过 MakeAppx、签名或真实 OCR。

验收：Go/前端两引擎契约、Windows 无身份/无语言/错误引擎/未知置信度、来源不可伪造、v7 保存与迁移、双端构建、MSIX 输入和路径安全；Mac 原有实际 OCR 仍通过。Windows 原生编译、MSIX 安装、系统语言和真实识别保留待验。参考：[Microsoft OcrEngine](https://learn.microsoft.com/en-us/uwp/api/windows.media.ocr.ocrengine?view=winrt-26100)、[包身份检测](https://learn.microsoft.com/en-us/windows/win32/api/appmodel/nf-appmodel-getcurrentpackagefullname)、[MSIX 组件](https://learn.microsoft.com/en-us/windows/msix/desktop/desktop-to-uwp-manual-conversion)。
