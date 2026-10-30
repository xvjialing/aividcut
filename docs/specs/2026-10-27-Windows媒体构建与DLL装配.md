# Windows媒体构建与DLL装配

2026-10-27，FW-R11/FW-R14/FW-R16，推进P0双端媒体底座。保持已有Mac SDK/多轨/字幕配音/LUT/生成/CLI/Skills目标，真实模型账户后置。当前宿主无Windows C++编译器或运行环境，本轮实现可交接的原生构建和静态装配，不把Mac分析或PE复制夹具当Windows播放通过。

以明确的MSYS2 UCRT64 SDK作为首个Windows原生构建输入，保持GCC/libstdc++/UCRT同一工具链，避免混用MSVC或CLANG64运行库。SDK安装/包来源和版本需明确，脚本不自动安装、执行包中脚本或公开发布。核对MSYS2官方包的SHA256，仅作为本次下载内容一致证据，PGP签名/源码及完整许可仍单独验证。

新增PowerShell原生构建入口：仅在Windows运行、明确SDK与新构建/输出目录，固定CMake/Ninja/g++/pkg-config路径，保持原SDK不写入。构建本项目worker后使用Go装配器，不执行JSON中的shell；路径以数组传参，模式失败保留本次stage/日志，不覆盖已有候选。

Go工具默认只读规划，根包括worker、FFmpeg/FFprobe、明确MLT/Qt插件，DLL只在明确目录中按Windows名称大小写规则解析，不搜索PATH/其它软件。普通与延迟PE导入均读取，RVA/VA、长度和数量有界；无依赖不能由Go未实现的ImportedLibraries空返回推导。错架构、同名不同内容、非法路径、缺少非系统DLL均保留具体原因。

API-set记录为系统契约，固定OS DLL清单是操作者声明的Windows基线，不能等同当前注册表或设备验证；动态LoadLibrary名称、驱动、系统组件与字体仍有实机门槛。延迟导入不默认跳过，不能靠把未知DLL归系统来获得通过。

仅明确-assemble且真实worker存在、DLL闭包无缺项时，创建新media目录：可执行与依赖放bin，MLT模块放mlt，Qt平台/图片插件保留plugins分区，完整MLT数据含loader.dict/profiles，写相对qt.conf及SDK清单v2并复用Load核验。默认只规划不会产生发行包，缺worker/缺DLL拒绝提交；清单与静态闭包不证明媒体协议、兼容性、许可、签名或商业可发布。

Windows worker环境修复为程序目录/系统目录PATH、SystemRoot/WINDIR和临时目录白名单，再附现有受控MLT/Qt变量；不沿Unix PATH，也不继承CLI账户和任意全局环境。非Windows保持原行为。

验收须覆盖真实官方Windows DLL的静态扫描、延迟导入与错架构/同名冲突、缺项不输出、真实PE复制机制及Windows交叉编译。原生编译、播放音频、Qt字体/插件、中文路径、保存导出、Job Object/资源和Windows安装运行仍需对应机器证明。所有复制夹具明确标注非媒体验收，releaseVerified保持false。
