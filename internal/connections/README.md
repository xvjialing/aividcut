# 模型连接与系统凭据

本包为生成能力提供连接资料、系统凭据及使用期保护，不负责验证模型账号、模型 ID、额度、权限或区域可用性。`Configured` 仅表示此连接已保存凭据。测试全部使用内存 `SecretStore`；本轮未读取、解锁或写入真实 Keychain/WinCred，也未调用收费模型。

## 稳定接口

```go
NewNativeStore() (*NativeStore, error)
New(metadataPath string, store SecretStore) (*Manager, error)
(*Manager).List() []Profile
(*Manager).Save(ctx context.Context, input SaveInput) (Profile, error)
(*Manager).Delete(ctx context.Context, id string) error
(*Manager).Acquire(ctx context.Context, id string) (*Lease, error)
(*Manager).Pin(id string) (*Pin, error)
(*Manager).PendingCleanupCount() int
(*Manager).Cleanup(ctx context.Context) error
IsCommitted(err error) bool
```

- `Profile` 只有 `ID/ProviderID/Name/ModelID/Configured/CreatedAt/UpdatedAt`，不包含密钥及系统凭据句柄；JSON 使用 camelCase。
- `SaveInput` 额外接受 `APIKey`。空 `ID` 新建连接；已有 `ID` 更新资料；空 `APIKey` 保留原凭据。新建时允许不配置密钥形成草稿。已有连接的服务归属不可改变，需为其他服务新建连接。
- `SaveInput` 的 JSON 解码接受 `apiKey`；重新 JSON 编码及普通 Go 格式化会隐藏它。应用不得将输入对象、反射字段或密钥字符串写入自定义日志，应及时清空界面中的输入值。
- `Lease.Profile` 为取得租约时的资料快照，`Secret(ctx, providerID)` 在原服务下提供本次凭据，结构上满足 `generation.SecretSource`。必须在整个请求结束后 `Close()`；关闭会清零本包持有的字节副本并释放使用计数。Go 字符串及系统库内部副本不能承诺立即清零。
- 租约建立前即占用连接，包括等待系统读取期间；租约或 Pin 存在时禁止删除及替换凭据，允许修改名称和模型资料。后续资料变化不会改变既有租约的快照或凭据。修改公开 `Lease.Profile` 也不会改变密钥绑定服务。
- `Pin` 不读取凭据，供未完成远程任务等待恢复时保护连接；恢复方应先恢复任务对应的 Pin，再开放资料编辑。Pin/Lease 只在当前进程内有效，不写入元数据。

## 存储与失败语义

正式 `NativeStore` 只白名单选择 macOS Keychain 或 Windows WinCred，使用 `github.com/99designs/keyring v1.2.2` 的系统 API 后端；不存在明文文件、外部 CLI 参数或其他后端降级。未支持系统、缺少 CGO 的 macOS 构建或不可用后端会明确报错。构造 `NativeStore`、`Manager` 及执行 `List/Pin` 都不打开或查询系统凭据库。只有显式保存、取得租约、删除或清理可能触发系统授权提示；系统调用本身是同步调用，context 在调用边界检查，不能承诺关闭原生授权对话框。

元数据是版本化 JSON，仅含 Profile、随机凭据引用及待清理引用。采用同目录临时文件、文件同步与原子重命名，不将 API Key 写入元数据、工程或临时文件。临时文件为 `0600`，新建目录为 `0700`；既有目录权限由安装和应用数据目录策略负责。每个元数据路径只支持一个写入 Manager，不提供跨进程锁；不是工程快照的一部分。

替换凭据使用新的随机引用，并按以下顺序提交：

1. 先落盘候选引用的清理意图，当前 Profile 仍指向原凭据；在途引用受保护，不会被 Cleanup 删除。
2. 将新密钥存入系统凭据库，再原子发布新 Profile；同时记录旧引用的回收意图。
3. 清理旧凭据。只有系统删除和清理记录落盘都完成后才移除清理意图。

提交前的失败或取消保留原连接，并尝试删除候选凭据；如回滚也失败，清理意图跨重启保留。重启和 List 不自动清理，调用方可提示并显式调用 `Cleanup`。对已不存在的待清理凭据可以安全重试。应用不得将普通 Save 失败视作已保存新密钥。

提交后清理失败，`Save` 返回**已提交的 Profile**及 `*Error{Committed:true, Code:"cleanup_pending"}`，消息明确“连接已保存，旧凭据清理待处理”。应用应接受该 Profile、清空输入中的 API Key、展示可重试清理的提示，不应重新提交密钥。`Delete` 同理：逻辑删除成功、凭据待清理时返回 `Committed:true`。提交后的取消不会伪报整个保存/删除失败。

`Cleanup` 只操作非活动引用，不会更换账号或重新提交生成任务。系统错误被转换为不含密钥的固定公开错误。资料文件损坏、版本不支持、未知字段、无效或重复引用会直接拒绝加载，不会猜测或访问系统凭据库修复。文件原子替换防止半文件，尚不承诺所有文件系统在突然断电时的目录项持久性。

## 验证范围

内存 fixture 覆盖资料重启、公开返回脱敏、租约与 Pin、等待读取期间的保护、独立连接并发、提交顺序、各阶段失败回滚、取消、清理失败跨重启与幂等重试。原生后端只验证白名单和惰性构造，不据此宣称 macOS/Windows 系统授权和凭据访问已通过实机验收。
