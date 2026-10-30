# 本地画面文字识别适配器

接口只接受应用已核对的源 JPEG，返回识别文字及原始置信度，不接收工程写入权限。首个实现是独立的 macOS 13+ Apple Vision 进程，原文识别关闭语言校正，使用修订 3 和系统实际支持的中英文语言；结果仍可能有错。

`native/ocr/main.swift` 只在构建机由 Xcode 命令行工具编译；客户端调用随包 `Contents/MacOS/AIVidCutOCR`，无需运行时 Swift 编译器。开发联调可显式指定 `AIVIDCUT_OCR_WORKER` 的绝对路径，正常桌面包自动发现相邻进程，不搜索 PATH。Windows新增系统OCR适配代码，缺包身份或helper时返回不可用；原生编译与执行仍待验。新增适配器应复用 Recognizer 数据契约，同时登记能力发现、结果校验、真实来源及必要的格式迁移；不能只替换进程或伪装 Apple Vision。

输入上限 4MiB，输出 256KiB、错误流 16KiB；辅助进程最多 50 条、每条 1000 字。调用经共享 managedprocess 生命周期，45 秒上限或父上下文取消会终止进程树。应用层另核对项目、修订、源实例和指纹，最多运行一份识别，候选保留 10 分钟，待核对最多 8 份，含已接纳回执的总缓存最多 32 份。退出和源实例切换取消运行、清理候选；不会自动重试或调用收费模型。

识别结果只有通过明确接纳才作为带源帧证据的 VisualAnalysis 保存到当前 v7 工程。原始置信度仅供核对，人工修正后不把旧置信度冒充修正文案的置信度。回看、检索、移除、撤销和素材收集沿用既有核心；这是选定帧的 OCR，不是整段字幕或连续事件分析。

实际构建在 Resources/ocr-build-info.json 记录辅助进程和源代码 SHA256、Swift/Apple SDK、目标和系统动态依赖。release-audit 支持独立 ocr 角色，正常构建分别签名辅助进程与应用。系统兼容、媒体许可和正式发行仍按各自门槛核验。

参考：[Apple Vision 请求](https://developer.apple.com/documentation/vision/vnrecognizetextrequest)、[语言校正开关](https://developer.apple.com/documentation/vision/vnrecognizetextrequest/useslanguagecorrection)。

实际样本由native/ocr/create_test_fixtures.swift在明确目录生成，包括中英文、空白及0–3秒计数。显式设置AIVIDCUT_OCR_WORKER、AIVIDCUT_OCR_FIXTURE_IMAGE和本机FFmpeg/ffprobe后运行TestSourceOCRActualVisionImageBlankAndVideoPosition；不依赖FFmpeg的drawtext滤镜。

Windows使用同一协议的windows-media-ocr引擎与方法，API未提供的置信度必须为null，修订0只表示未知。Go和helper均核对包身份，子进程隐藏控制台并保留Job控制；实际用户语言来自系统配置。构建/装配与MSIX入口见[Windows交接](../../docs/implementation/Windows系统OCR构建与MSIX交接说明.md)，原生编译/安装/识别尚未验证。

MCP的aividcut_read_source_ocr与人工核对共用识别函数，但不注册预览URL或保留人工候选。读取必须有源画面许可，沿用20/128证据及请求超时/取消；返回原始文字仍是未校对素材数据，模型解释与原始识别须分开。
