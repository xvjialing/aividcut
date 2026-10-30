# 随包媒体清单

应用启动只查自身安装目录中的`media/runtime.json`；macOS `.app`使用`Contents/Resources/media/runtime.json`。模块不扫描其它应用，不安装/下载工具，不执行清单内容，不修改SDK。清单验证后返回本次安装位置下的绝对路径，设置只保存自动模式。

格式见`Manifest/Paths/Payload`类型。可读schemaVersion=1/2：v1声明五类入口，v2还显式声明MLT数据目录data；v1不能夹带data语义。两版均含ID/版本、darwin或windows、arm64或amd64及全部文件。普通文件使用`kind=file`、bytes和小写SHA256；内部链接使用`kind=symlink`与相对target，不附SHA/bytes。清单自身不列入payload。物理目录中的文件及链接必须逐项登记，目录项自动遍历；框架`Versions/Current→A`等内部相对链接允许，越根、绝对链接和未登记内容拒绝。

`os.Root`约束文件读取，SHA按64KiB流式计算，并核对全部文件/目录身份与元数据及原清单路径。2MiB清单、8192条payload、32768节点、单文件512MiB、总计2GiB；取消与变更都返回错误。SDK目录只存受清单管理的内容，日志/缓存写用户数据目录。

哈希说明内容一致性，不能替代发布者身份、代码签名、许可、动态库闭包或实际能力。正式装配顺序需要先完成库重定位和内部二进制签名，再生成清单，最后外层签名；后续修改内部文件需重新生成/核对清单。`CreateManifest`对无已有清单的新目录生成并调用同一Load校验，已有内容不覆盖。macOS显式装配入口和Wails随包参数见[装配说明](../../docs/implementation/媒体SDK装配说明.md)，双端正式签名与许可仍需继续。

应用层在自动模式使用此模块，旧手动空路径仍表示PATH/原生未配置。自动模式找到坏清单时停止自动PATH回落，显式环境/手动配置可独立使用；没有清单则保留原PATH发现。原生worker/repository/profiles在无相关环境覆盖时成组使用，避免混配。候选检测忽略环境覆盖，保存重新校验随包内容和检测指纹，重启生效。

[规格](../../docs/specs/2026-10-27-随包媒体运行时发现与校验.md)及验收记录维护实际覆盖；不要把开发机移动后仍能加载Homebrew库当作干净机器运行证明。
