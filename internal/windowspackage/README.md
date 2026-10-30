# Windows媒体静态规划与装配

从明确的worker/FFmpeg/FFprobe、MLT模块、Qt插件和DLL目录建立普通/延迟PE依赖图。仅静态读取，不在Mac执行Windows程序，不扫描PATH、注册表或其它软件。文件架构、SHA/字节绑定，DLL按大小写不敏感名称查找；同名不同内容、错架构、非法路径、缺项均拒绝装配。

默认只规划：

```sh
go run ./cmd/windows-media-package -config /absolute/path/config.json
```

`-assemble`才写新目录，必须真实worker存在且静态缺项为空。SDK代码/依赖放bin，MLT插件放mlt，Qt插件保留plugins分区，MLT数据含core/loader.dict和profiles，写相对qt.conf及清单v2。输出存在或位于数据输入树内时拒绝，失败保留stage，已有文件不覆盖。上限1024 PE/单文件512MiB/合计2GiB，数据32768节点；清单另受mediaruntime边界限制。

systemDlls是操作者声明的Windows基线，API-set是系统契约，二者不是SDK普通文件；声明不能替代目标系统验证。具体DLL不在明确SDK目录时保留missing，不能把未知DLL自动归系统。运行时计算的LoadLibrary、驱动、Qt行为与签名仍需实机。

复制机制测试使用真正交叉编译PE夹具，ID明确标注非媒体质量验收，不证明worker协议或Windows播放。实际SDK来源和构建步骤见[Windows说明](../../docs/implementation/Windows媒体构建与验收说明.md)，商业许可及源码分发安排仍待审，releaseVerified保持false。
