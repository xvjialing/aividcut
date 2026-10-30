# 只读发行输入检查

对应[规格](../../docs/specs/2026-10-27-发行产物与媒体依赖检查.md)。此模块不参与剪辑/生成，不扫描系统找SDK，不安装或修改任何媒体依赖。报告针对配置中的完整随包发行候选，开发机外置依赖可以被读取并记录，但会如实报告未随包。

```sh
make release-audit
make release-check
go run ./cmd/release-audit -config /absolute/path/audit.json
```

相对路径以配置文件位置为基准。示例[配置](../../build/release-audit.example.json)描述目标包布局，当前开发`.app`尚未拥有其中全部媒体文件，报告出现缺项是预期事实，不是安装/修复请求。使用`AUDIT_CONFIG=/absolute/path/audit.json make release-audit`选择其它输入。报告JSON写stdout，输出文件必须放在被检查输入之外，避免shell重定向先截断输入。

`release-check`先运行production嵌入资源测试，再以`-check`输出当前发行候选缺项；不自动构建/安装/发布。当前许可闭包仍需人工评审，该门槛不能仅靠补齐文件变绿。Windows交叉环境不能在Mac运行production测试，需在对应Windows验收环境执行；静态PE报告仍可跨平台生成。

默认只静态读文件；`-probe-ffmpeg`是明确执行配置文件中同平台/架构的FFmpeg及FFprobe固定`-version`，3秒/64KiB上限、无shell和自定义参数。探测前后的产物SHA256绑定结果，不匹配即拒绝；不会执行Windows二进制来代替Windows实机测试。未知构建/未探测不默认归类为LGPL。

单文件512MiB，每目录8192文件/32768节点/2GiB，配置64KiB、32产物/8运行时目录。前端映射流式读取；普通文件变化、目录变化、超界、特殊文件及损坏PE导入表均不作为完整检查。目录内symlink只记原链接目标，不递归跟随，指向包外单独报告；这部分哈希不是目标文件内容的证明。

Mach-O包含thin/fat头；PE直接解析普通导入目录，因为当前Go标准库`ImportedLibraries`仍为未实现的空返回。已知系统库分类仅代表普通导入名称/路径，不验证签名和实际加载目标。PE延迟导入、LoadLibrary/Qt/MLT插件及传递依赖不在完整覆盖范围。

Go构建信息记录实际版本、允许列出的构建选项及嵌入模块；调试段和trimpath只作保护措施证据，不能保证前端或算法不可逆向。前端目录扫描不等于已提取应用二进制；另用production构建的嵌入资源测试核对真实embed.FS，二者分开保留。

`-check`遇blocker或review退出1，正常生成报告退出0，配置/检查无法完成退出2。`releaseVerified`固定false，许可闭包始终需要人工复核，不能把静态检查全绿当成发行许可。签名/公证/对应源码/许可证与重链接安排/专利/双端实机仍有独立门槛。本轮不自动切换编码器，不把进程隔离视为GPL义务豁免，也不把Wails的许可代替媒体SDK的许可。
