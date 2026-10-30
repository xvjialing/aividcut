# 共享进程生命周期与Windows Job验收

2026-10-27，FW-R07/FW-R11/FW-R14/FW-R15/FW-R16，P0/P4/P5。统一本地Agent/媒体worker/FFmpeg生命周期，补齐Windows派生进程控制代码及独立验收程序，实际Windows机器和账户继续按用户指令后置。

## 代码缺口与实现

原Windows configureProcess/configureSpeechProcess为空或仅依赖CommandContext结束直接进程；macOS各模块重复设置进程组。新[managedprocess](../../internal/managedprocess/command.go)统一Start/Run/Wait/Output/CombinedOutput和默认2秒WaitDelay，18个生产构造入口迁移，移除各包重复平台配置。保留原环境白名单、MCP/策略/输出上限、工程作用域和取消已提交编辑的语义。

Windows创建不继承的匿名Job并设置kill-on-close，以CREATE_SUSPENDED启动；在主线程恢复前分配到Job，确认唯一所属线程和owner PID，ResumeThread前挂起计数须为1。无法创建、配置、附加、确认或恢复时结束本次进程，不退化为普通启动。取消终止该Job，正常Wait关闭Job也收尾普通派生进程，不按进程名查找/终止其它应用。

Job控制生命周期，不是OS权限沙箱；不设置breakaway、管理员权限或允许工具绕过。其它服务/远程/WMI等非普通CreateProcess方式不据此宣称被完整覆盖。Windows平台、CLI有效managed策略与账户门槛保留，不能仅凭交叉编译解除platform_unverified或Claude policy_unverified。

macOS/Linux保留原Setpgid/组SIGKILL行为。包装没有新增用户工具、MCP资源或模型网络调用；Go服务仍负责输入/工程/快照验证。共享模块的Output等生命周期均显式包装，避免这些方法绕过Start。

## 本机验证

生命周期测试覆盖准备失败不启动、附加失败回收/不可重启、Run/Output/CombinedOutput顺序、退出和清理错误同时保留、实际取消与已取消ctx不启动。另实际启动自己的分支/叶进程，让叶持续写心跳，取消后心跳停止；只操作本次测试PID和临时文件。当前Mac测试中含helper共8个顶层测试，不能将helper本身当作独立业务验收。

显式真实随包SDK/FFmpeg下，managedprocess/localagent/playback/media/exporter/application六包竞态回归通过，相关vet通过；预览、音频、导出、应用和受控Agent fixture的既有行为保持。原生媒体部分不是无环境skip计为通过。Windows调用链已交叉构建，x64/arm64原生测试二进制均编译，但未在Mac执行PE替代Windows。

## Windows实机交接

Windows专用TestNativeWindowsJobOwnsGrandchildOnCancelAndRootExit分别验证ctx取消和父进程正常退出后，本次Job叶进程的句柄在5秒内变为终止状态。测试不接收任意目标PID，只创建自己的helper；与客户端隔离交接，不需要Go安装、管理员权限或模型账户。

[独立Windows验收包](../../work/managed-process-check/Windows-Process-Lifecycle-Checks.zip)为2910042字节，SHA256为65f53ea01a202e3005e66b85f40ecc92d805db9e342d778b47d5ffca4db7c49a，含x64/arm64二进制、说明、校验与实际Go/x/sys许可文本。此包仅开发验证，不混入客户端商业发行包。Windows尚未执行，API/线程/Job嵌套/管道等需该实机结果，当前不能记为通过。

PowerShell命令如下，arm64机器选择对应文件：

```powershell
& .\managedprocess-windows.test.exe -test.v -test.run '^TestNativeWindowsJobOwnsGrandchildOnCancelAndRootExit$'
```

保留全部PASS/FAIL输出与系统/文件SHA；不能只凭文件启动或部分用例通过解除客户端策略门槛。

## 双端构建与资料

Wails latest本轮仍v3.0.0，Go/CLI/runtime对齐。双端随包构建、生产嵌入清单/source map检查和Mac deep/strict本地签名通过；Mac/Win媒体480/528 payload与原指纹保持，C++和SDK未改。应用原声明/签后绑定照常生成，缺失组件文本及完整法律审查不变。

[本轮Windows客户端候选](../../work/managed-process-check/AIVidCut-windows-amd64-process-jobs.zip)含764文件、112124434字节，CRC通过；SHA256为b411d5872a48baf5b638c8ca27ec97a9ea1c4313c00df143c1cd597e0998a2a1。客户端SHA为68bdaf9cdbef2ce5cfb071c14d96f37a222e13211ce8fd1e73c9c6afc59d654f；两个媒体诊断工具随共享模块重新编译，并重新收集实际声明和最终绑定。旧zip保持。

本轮没有前端视觉/交互改动及新GUI验证，390项前端沿上阶段记录；已有macOS最低SDK链接warning保持。媒体发行静态检查仍Mac0 blocker/3 review、Windows0/30，releaseVerified固定false。

## 本轮审查的其它未完成项

react-remove-scroll-bar当前上游LICENSE已找到且标注2025，但未证明对应已打包2.3.8的registry gitHead；继续保留原声明缺项，没有把新分支文本直接当作已绑定原文。[当前上游原文](https://github.com/theKashey/react-remove-scroll-bar/blob/master/LICENSE)与早期公开接口404分别记录，不将后者误写成“该项目没有许可证”。

仍需继续：Windows Job实际测试/管理配置与账户、Claude启动前managed策略、实际生成/语音/CLI账户、签名安装/许可对应源码、完整设备/GPU及原FW-R01–17/P0–P6目标。此实现和Mac进程组证据不替代Windows原生成功。

[规格](../specs/2026-10-27-共享进程生命周期与WindowsJob.md)、[数字证据](2026-10-27-共享进程生命周期与WindowsJob-evidence.json)、[共享模块说明](../../internal/managedprocess/README.md)。无子智能体、worktree、提交、PR或收费操作。
