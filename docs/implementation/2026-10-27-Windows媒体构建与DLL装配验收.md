# Windows媒体构建与DLL装配验收

2026-10-27，FW-R11/FW-R14/FW-R16，P0双端媒体底座。上一轮Mac随包SDK已通过所测禁止Homebrew链路；本轮推进Windows原生构建入口、PE普通/延迟导入和明确DLL目录装配。当前宿主没有Windows C++编译器/运行环境，真实worker编译、播放与安装继续未完成，模型账户按用户要求后置，付费/CLI推理0次。

## 实际官方输入

从MSYS2官方UCRT64来源下载MLT7.42.0-1、FFmpeg9.0.2-1和Qt6-base6.12.0-1，分别按官方页面SHA256逐字节核对。约45MB压缩包只解出受限的普通文件到work，未运行其中Windows程序或包脚本，未安装系统依赖。PGP签名未验证，不把网页摘要比对提升为完整发布者认证。[MLT](https://packages.msys2.org/packages/mingw-w64-ucrt-x86_64-mlt)、[FFmpeg](https://packages.msys2.org/packages/mingw-w64-ucrt-x86_64-ffmpeg)、[Qt](https://packages.msys2.org/packages/mingw-w64-ucrt-x86_64-qt6-base)。

实际文件具有FFmpeg/FFprobe、Qt6Widgets/Gui/Core、MLT core/avformat/qt6/xml/plus/normalize/resample DLL、离屏/Windows/图片插件和loader.dict等数据。静态扫描23份真实amd64 PE、460条依赖引用；在当前仅三个主包的集合中仍有114条缺失引用，worker缺失为true，普通/延迟分类明确。本批实际文件延迟引用为0，因此延迟正例由独立构造的合法PE描述符测试，不虚报来自本批SDK。

缺项包括Qt6Svg及许多FFmpeg/字体/音频运行库，属于当前输入不完整的事实，不是把静态报告成功视为Windows通过。系统基线明确补充AUTHZ/AVICAP32/D3D12后仍保留其它未知DLL；没有用全局PATH或其它剪辑软件补库。实际软件包的许可、来源和完整ABI仍需继续审查。

## 已实现代码

新增windowspackage模块与cmd/windows-media-package，默认只规划，不执行PE。根为明确程序、MLT/Qt插件，依赖只从明确DLL目录按Windows大小写规则解析。PE读取含架构/字节/摘要，并在同一文件身份中绑定元数据与SHA；同名不同内容拒绝选择，根/库架构不匹配、名称夹带路径或超边界均拒绝。

新增普通/延迟PE解析，兼容RVA以及能安全转换的旧VA模式，描述符、名称和RVA有界；损坏/不支持时返回失败，不当成无依赖。当前Go的ImportedLibraries未实现，继续直接读描述符；发行审计也纳入延迟标记。API-set和操作者声明的系统DLL分别记录，不把它们当SDK磁盘文件，也不声称已在目标注册表核对。

仅明确-assemble、worker存在、静态缺项为空时创建新目录。bin放程序及依赖、mlt放选中模块、plugins保留Qt分区、data含完整MLT数据/profiles，相对qt.conf与清单v2复用同一校验。已有输出或把输出放在输入数据树中均拒绝，失败保留新stage，不覆盖源SDK。每PE512MiB、映像1024/合计2GiB、数据32768节点，完整清单继续有自己的边界。

复制机制正例使用真正交叉编译的Windows PE，但这些程序是明确的复制夹具，ID标注非媒体验收，不能代替本项目C++ worker或FFmpeg运行。该正例只证明封闭文件映射、平台声明、v2清单与已有输出保护。缺DLL/未知系统假设、同名冲突、错架构、缺worker和输入树自复制负例都拒绝提交。

## 原生构建入口与环境修复

新增build_windows_media.ps1，Windows限定、明确UCRT64 SDK与新目录，固定SDK内CMake/Ninja/g++/pkg-config，核对x64 MinGW triple，关闭CMake外部包注册表并隔离pkg-config/PATH，finally恢复环境。不自动安装/下载或调用SDK shell脚本，路径经参数数组传递。构建worker后生成Go装配配置，DLL不完整时保留错误，不自动扩大搜索范围。

PowerShell脚本新增无BOM JSON输出，避免Windows PowerShell5.1的UTF8默认BOM导致Go严格JSON失败；脚本本身BOM保留中文。用微软官方发布API和资产SHA核对的便携PowerShell7.6.9在Mac进行静态ParseFile，17个语句、0语法错误；关闭遥测/更新检查，没有执行原生Windows构建。[微软官方发布](https://github.com/PowerShell/PowerShell/releases)。语法证明不代替Windows 5.1/7运行、CMake/ABI与编译成功。

发现原worker进程所有平台都用Unix PATH，且Windows没有SystemRoot/WINDIR/TEMP。现按平台保留最小环境：Windows取系统API返回的Windows目录和显式worker目录/临时目录构造PATH及必要变量，再附原受控MLT/Qt变量，不继承CLI账户和任意父进程PATH；非Windows保持旧行为。纯函数覆盖来源边界，Windows客户端实际交叉编译包含系统API调用，但没有把这个编译视为实机环境验证。

## 检查与限制

Go完整35个带测试包竞态/vet通过，最终输入树/总量保护与环境隔离调整后相关windowspackage/releaseaudit/playback再验证。延迟导入包含合法延迟DLL、越界RVA与不可转换VA负例；复制夹具明确不提供媒体协议。

Windows装配工具及客户端amd64交叉构建通过；Mac继续显式携带上轮SDK构建并production资源检查，未用默认无SDK构建覆盖可用测试包。界面无改动，372项前端为既有基线，本轮构建typecheck/Vite通过，不新增GUI验收。Wails官方latest仍3.0.0，Go/CLI/runtime一致、Go1.27.3。

仍需继续：真正Windows机器上的本项目worker编译、完整UCRT64依赖、Qt/DLL动态加载和字体、真实多轨声音/LUT/中文路径与导出、Job Object和设备资源、签名与安装更新。当前SDK扫描缺项和worker缺失不作为已完成；模型账户与CLI推理、所有原FW-R01–17/P0–P6范围保持。没有把SDK复制正例或Mac解析结果当成用户所需最终Windows客户端，也没有自动公开分发GPL/Qt组合。

[规格](../specs/2026-10-27-Windows媒体构建与DLL装配.md)、[原生步骤](Windows媒体构建与验收说明.md)、[模块](../../internal/windowspackage/README.md)、[数字证据](2026-10-27-Windows媒体构建与DLL装配-evidence.json)。来源、包摘要、静态图、解析结果与日志位于work/windows-runtime-check。
