# 共享进程生命周期与Windows Job

2026-10-27，FW-R07/FW-R11/FW-R14/FW-R15/FW-R16，P0/P4/P5。补齐Windows取消只能结束直接进程的代码缺口，统一Agent/媒体worker/FFmpeg启动和收尾；Windows实际运行与账户仍按用户安排后置。

managedprocess.CommandContext包装exec的配置与管道，Start/Run/Wait/Output/CombinedOutput必须经过同一保护生命周期。调用方不能绕过嵌入Cmd直接调用其Start/Run。默认WaitDelay2秒限制遗留管道等待；显式输出/行预算仍由原调用方实施。失败保留退出/清理原因，不把附加失败降级为普通执行。

Windows在Start前创建不继承的Job，设置JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE；以CREATE_SUSPENDED创建直接子进程，在主线程恢复前将其分配到Job。Go关闭初始线程句柄后，通过有界线程枚举确认唯一所属线程，打开后再次核对owner PID，仅当ResumeThread返回前挂起计数1才继续。Job配置/分配/身份/恢复失败，结束并回收本次进程。取消终止本次Job，正常Wait后关闭Job也收尾普通CreateProcess派生进程，不设置breakaway或申请管理员权限。

CREATE_SUSPENDED到Job分配之间，context取消可杀本次挂起子进程，不能先执行用户/CLI代码后再补保护。线程枚举最多65536条，异常/多线程或无法确认状态拒绝，不能依赖名称猜目标。Job不是权限沙箱，也不覆盖远程服务或非普通CreateProcess的外部启动方式。

macOS/Linux保留现有独立进程组和SIGKILL取消行为，未默默改变到另一种权限策略。其它未实现平台明确拒绝。CLI仍沿安装/版本/独立状态/managed策略/账户检查，Windows platform_unverified和Claude policy_unverified不会因代码编译通过而自动解除。

验收覆盖准备失败不启动、附加失败回收、Run/Output生命周期、退出/清理错误、取消/已取消上下文、本机真实派生进程心跳停止，及Windows专用取消/正常父进程退出后派生进程终止测试。Windows x64/arm64验收二进制单独交接，未实际执行不得记为通过或混入客户端商业发行。

相关实际SDK回归包含Agent、MLT、FFmpeg、预览/导出和应用；双端生产构建、声明/签名绑定保持。没有新内置工具/MCP权限、账户调用或用户配置改写。

依据：[Microsoft Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)、[CREATE_SUSPENDED](https://learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags)、[AssignProcessToJobObject](https://learn.microsoft.com/en-us/windows/win32/api/jobapi2/nf-jobapi2-assignprocesstojobobject)、[ResumeThread](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-resumethread)，核查日期2026-10-27。
