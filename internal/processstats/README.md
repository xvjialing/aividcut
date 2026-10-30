# 应用进程资源读取

只以当前进程为根枚举并确认后代，不接受界面指定任意PID。macOS用公开libproc身份与rusage_info_v4，累计CPU通过mach_timebase_info换算；Windows用Toolhelp、GetProcessTimes和GetProcessMemoryInfo。输出程序名、出生身份、CPU、RSS与各平台私有指标，不输出参数、环境或程序路径。

PID复用拒绝，曾确认且身份不变的孤儿可继续观测；短进程、权限变化和128明细/65536枚举上界如实记录。RSS共享页面可能重复，Mac物理footprint与Windows私有提交语义不同。独立WebKit XPC、GPU/显存和外部独立CLI没有可靠归属时不猜测、不补零，不使用私有API。

[阶段验收](../../docs/implementation/2026-10-27-进程资源诊断验收.md)保留实际CPU单位校验与采样数据。Linux等未实现平台返回未支持。2026-10-27 preview-benchmark已复用本模块并移除独立ps解析，Windows诊断工具与客户端同口径；Mac随包AIVidCutMedia明确归为media，详见[补充验收](../../docs/implementation/2026-10-27-跨平台拉帧资源诊断验收.md)。
