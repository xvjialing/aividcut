# 受限本地 MCP 传输

2026-10-25，关联 FW-R07/FW-R08/FW-R11/FW-R16。固定使用官方 `github.com/modelcontextprotocol/go-sdk v1.7.0`，仅实现工具注册与 loopback Streamable HTTP，不导入 application，不修改工程、读取凭据库、调用 CLI 或执行收费任务。

## 接口

```go
server, err := mcpserver.Start(mcpserver.Options{
    ProjectID: originalProjectID,
    Tools: []mcpserver.Tool{{
        Name: "project.summary",
        Description: "读取当前工程摘要",
        InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
        ReadOnly: true,
        Handler: func(ctx context.Context, input json.RawMessage) (any, error) {
            // 应用在自己的锁/事务中再次核对工程身份和范围，仅返回有界摘要。
            return map[string]any{"projectId": originalProjectID}, nil
        },
    }},
    ValidateProject: checkOriginalProject,
})
```

- `Info() Info`：Address/ProjectID/Running/ToolCount，普通状态不含 secret。
- `ConnectionConfig() (ConnectionConfig,error)`：仅在明确的用户复制动作中调用，返回 URL/Transport/HTTPHeaders.Authorization。每次返回独立map，停止后拒绝返回。不要将此结果写入普通日志、项目、任务metadata或自动发布；本包不持久化它。
- `Stop(ctx) error`：停止接收、撤销当前凭据、取消请求/回调并等待。调用方ctx先结束可返回超时，但不能视作全部回调已退出。
- `Close() error`：默认最多等待2秒，可通过StopTimeout调整。
- `Done() <-chan struct{}`：监听结束及所有已接受请求、实际业务回调退出后关闭，可用于应用资源/锁的最终释放。

Tool 提供 Name/Description/InputSchema/Handler 和 ReadOnly/DestructiveHint/IdempotentHint/OpenWorldHint。注解只是向客户端描述行为，不能当权限边界；允许什么工具由应用选择。改变工具集或工程范围时停止旧服务并创建新服务，凭据随之更换。

`ValidateProject` 是可选入口检查，不能替代业务提交时原子核查 projectID/revision、选区、幂等键及用户授权。应用回调仍负责拒绝工程切换或过期提交。工具参数里的 `consent:true` 不构成用户发起的收费授权；注册工具不应直接开放任意路径写入、Key读取或无UI授权的付费提交。

## 网络与协议边界

- 只绑定 `tcp4 127.0.0.1:0` 随机端口，唯一入口 `/mcp`。peer必须为IPv4 loopback，Host须精确匹配实际127.0.0.1端口；不信任代理头。
- 每次Start用crypto/rand生成32字节ephemeral bearer，只有Authorization header有效；拒绝缺失/重复/错误凭据、URL查询参数携带凭据。停止后旧凭据无效，新Start不复用。
- 默认服务面向本机CLI，没有Origin的请求才可进入；带Origin（含null/同地址Origin）或cross-site Fetch头的浏览器请求拒绝。不设置允许跨源的CORS头。
- SDK采用 Stateless=true/JSONResponse=true，每POST独立临时协议会话。拒绝持久Mcp-Session-Id，GET/DELETE返回405，不提供独立SSE、后台通知/恢复或stateful会话。没有把2026新协议的无状态方向描述成持久旧会话已完成。
- 每个请求独立构建SDK服务并捕获HTTP生命周期，使无状态旧协议请求也绑定实际HTTP断开/截止时间。服务Stop还会统一取消所有回调。旧客户端如何发送逻辑取消通知由其SDK决定；这里不以RPC id跨客户端维护持久取消会话，也不承诺旧协议通知恢复功能。
- 只接受单个JSON-RPC对象，不接受batch；严格检查UTF-8、重复键、深度及词元。SDK日志和HTTP错误日志均丢弃，不记录Authorization/原请求体。

固定SDK的raw `Server.AddTool` 不自动验证输入，因此本包先以SDK同依赖 `jsonschema-go v0.4.3` 编译Schema，根须type=object，Loader=nil禁止远程ref，注册时也预检SDK声明；调用时验证后才进入业务。Schema不自动套用default。未知内部error/回调panic只给通用错误；应用明确允许公开的诊断可返回 `*ToolError{Code,Message}`。

## 限额与生命周期

默认请求30秒、输入1MiB、完整HTTP输出2MiB、HTTP并发8、业务回调并发2、TCP连接最多HTTP并发+2。可配置范围见normalizeOptions：请求100ms–2分钟、输入1KiB–8MiB、输出4KiB–16MiB、请求并发1–32、回调并发1–8。静态工具最多64，单Schema64KiB、描述8192字节，目录须在输出预算内。

工具返回值转换为JSON对象；原本是数组/标量时包在 result 字段，兼容旧协议structuredContent。兼容text content同时返回相同JSON。为预留这些编码开销，单工具原始JSON预算为HTTP输出上限的四分之一；大工程应分页或返回摘要，不能通过单次结果传视频帧。最终HTTP响应还会完整缓冲并核查总字节后发布。

输入、输出及执行并发均有边界，但这不是运行任意恶意Go回调的沙箱。业务须限制自己的分配/序列化并遵守ctx。超时会结束请求等待，忽略ctx的回调仍占原并发名额，避免超时后无限叠加新goroutine；Done不会在它实际退出前关闭。取消不代表已提交业务自动回滚，写操作必须由应用核心保证事务/幂等语义。

## 验证与来源

`go test ./internal/mcpserver -race -v` 使用真实loopback HTTP和官方Go SDK客户端，没有真实账户、CLI推理或费用。

覆盖当前2026-07-28协商，以及2025-11-25 initialize/list/call。固定SDK未公开强制旧版本的选项，legacy测试仅在fixture中拒绝discovery以触发官方客户端自身回退分支，后续旧版请求真实到达本服务并断言HTTP协议头；不冒称真实Codex/Claude客户端已验收。

另测Bearer/Origin/Host/peer、session/query限制、chunked输入、Schema/远程ref拒绝、输出预算、错误脱敏、HTTP及工具并发、工程guard、两协议Stop取消、忽略取消时Done保持未完成、停止后配置不可读和重启凭据轮换。未实现业务工具、MCP写入验收、CLI配置安装或付费动作的用户授权流程。

固定版本依据直接读取已校验Go模块的官方源码，而网页打开在本轮网络环境失败：

- [SDK v1.7.0服务文档](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/docs/server.md)
- [Streamable HTTP及Stateless/取消选项](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/mcp/streamable.go)
- [工具注册责任与服务选项](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/mcp/server.go)
- [协议类型与工具注解](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/mcp/protocol.go)

依赖由主会话固定管理，本包未执行get/tidy或改根模块文件。实际客户端兼容性、Windows/macOS原生应用关闭和业务写入/收费门槛仍需应用层单独验收。

2026-10-26：ImageResult为显式图像结果通道，SDK image content与结构化元数据分开，不允许根指针/嵌套对象回落成普通JSON图片。每次1–3张PNG/JPEG、单图256KiB、解码尺寸至多1024×1024；最终图像结果另有保守响应预算，HTTP总响应限额继续生效。默认应用源帧至多960×540，超预算明确减少帧或用overview。业务回调审计不构成网络交付或模型理解的证明。

## 成功生成候选（2026-10-26）

应用工具目录新增当前工程成功候选分页、添加/替换预览及两项应用。列表和预览只读，无路径/素材URL/像素；应用只在已有allowEdits范围注册，预览绑定当前scope字符串并在准备/事务前再次检查。回执newlyApplied/alreadyApplied/clipExists/matchesPreview区分真正编辑与重复/撤销；价格相关请求仍另经客户端人确认。实际传输、Schema/投影/停止/Runner证据见[验收](../../docs/implementation/2026-10-26-生成候选MCP与Wails3正式版验收.md)。
