# 受控子进程生命周期

本模块用于本应用明确启动的CLI/媒体子进程，不接受任意PID管理，不是权限沙箱。统一使用CommandContext后调用本包装的Start/Run/Wait/Output/CombinedOutput；禁止通过嵌入的Cmd绕过生命周期。字段和管道配置沿exec，输出限额由原调用方提供。

Windows先创建不继承/kill-on-close Job、挂起创建子进程，再分配/确认唯一线程/恢复；失败终止，不退化执行。取消终止本Job，Wait关闭Job收尾普通派生进程。线程/Job API只操作由当前Cmd持有身份的进程，不查找同名用户进程。Job无法替代CLI工具白名单、managed策略或OS权限隔离，也不代表任意远程/服务启动都被包含。

Unix沿原Setpgid+组SIGKILL，未新增权限设置。默认WaitDelay2秒防遗留管道无界等待。调用者仍需为整个会话及单次操作提供ctx，长会话不能用立即结束的请求ctx；所有成功Start都必须Wait。

```sh
go test -race ./internal/managedprocess -count=1 -v
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c ./internal/managedprocess -trimpath -ldflags='-s -w' -o /new/managedprocess-windows.test.exe
```

在Windows机器执行原生测试：

```powershell
& .\managedprocess-windows.test.exe -test.v -test.run '^TestNativeWindowsJobOwnsGrandchildOnCancelAndRootExit$'
```

它只创建自己的临时helper/派生进程，分别验证ctx取消与父进程正常退出后叶进程结束，不需要Go安装、管理员权限或模型账户。测试文件单独交接，不与客户端发行混用。无机器时只有源码/交叉编译证据；Windows/Claude执行隔离状态保持未验。

完整[规格](../../docs/specs/2026-10-27-共享进程生命周期与WindowsJob.md)保留实现范围与官方依据。
