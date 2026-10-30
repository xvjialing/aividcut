# 第一方 MCP 执行模式

2026-10-25，关联 FW-R02/FW-R07/FW-R08/FW-R11/FW-R16。保留原 Plan 快照模式；Execute 是独立的第一方MCP执行入口，不返回EditRequest，也不把运行总结冒充副本验证或实际编辑审计。

## 接口

```go
Planner.InspectExecution(ctx, provider) Installation
Planner.Execute(ctx, ExecutionInput, notify) (ExecutionResult, error)
```

ExecutionInput 包含 Provider/ProjectID/ProjectRevision/Instruction/WorkflowID/SkillInstructions/Selection/MCP。ExecutionMCP 的 URL/Token 及整个MCP字段不进入普通JSON，连接只由App内部提供，Tools为App实际注册名单。API不接受公网、localhost别名、非/mcp路径、查询参数或凭据式URL；只允许http://127.0.0.1:有效端口/mcp及1–64个唯一aividcut工具名。

执行覆盖整个授权工程，Selection仅表示关注上下文；不能借此把模式描述成与Plan相同的选区硬约束。App须独占本次MCP范围、固定原engine身份，每次提交仍由同一编辑核心校验当前revision、锁轨、源范围和幂等键。工作流说明是inline文本，不能变成运行脚本或读取任意技能文件的权限。

ExecutionResult 只有 Provider/Summary/Warnings。Summary是模型总结，实际操作以App的MCP审计和核心revision为准。多个工具编辑是逐批可撤销，不能声称一次Undo；取消/失败/超时不回滚已经成功提交的批次。图片/视频/语音/导出工具只能准备待用户核对请求，模型参数consent不构成收费授权。

## Codex 0.164.0 契约

继续要求私有CODEX_HOME、无用户配置/hooks、无祖先项目配置、已有本机管理配置核查、有效requirements=null和已核查版本；用户在该作用域正常登录，不读取或复制auth.json。每次Execute重新检查，不通过即失败关闭。

每次进程只临时覆盖mcp_servers.aividcut：loopback URL、required=true、enabled=true、enabled_tools精确名单、default_tools_approval_mode=approve。Bearer只引用独立子进程变量 AIVIDCUT_MCP_BEARER；原值不进入argv、prompt、公共DTO或事件日志，也不写全局配置。强制NO_PROXY包含127.0.0.1/localhost，避免本机连接凭据经代理发送。其他认证仍由CLI正常管理。

shell、shell快照、apps、plugins、hooks、浏览器、computer/code-mode、图片生成、技能发现等已核查的31项特性保持false；sandbox=read-only、approval_policy=never、web_search=disabled。exec额外ignore-user-config/ignore-rules/ephemeral/private CWD，项目文档字节数为0。不会提供危险bypass模式。

InspectExecution使用虚拟127.0.0.1:9/mcp及一次性env值做配置诊断，不推理。真实Execute再用App endpoint/tools验证：features list、mcp list/get，及私有app-server仅initialize/config-read，然后打印startup requirements。app-server不创建thread或turn，配置回复后进程终止。mcp list不被单独当作完整运行工具目录；config/read还检查only-one server、env引用、工具名单、approve/required、只读sandbox和禁用特性。

本机实际CLI诊断已通过：Codex0.164.0、独立作用域signed_out/needs_login，execution_mcp与execution_effective_config均pass。Root应用真实HTTP的脱敏记录在 `work/execution-replacement-check/cli-metadata-evidence.json`。没有账户推理、登录或费用验收；正常登录后还会重新检查账户策略。

## 流与资源

仅接受thread/turn状态、公开agent_message、忽略不展示的reasoning，以及server=aividcut且tool在endpoint名单内的mcp_tool_call。命令、文件变更、网页或未知内置工具事件立即失败；不转发工具参数、原始结果、认证诊断或chainofthought。最终公开总结须与有界普通总结文件一致，回传时再次隐藏可能回显的会话值。

配置证明限定于已核查CLI契约，不是任意恶意可执行文件的操作系统沙箱。未进行实际模型推理，所以不将配置诊断称作工具完成实际剪辑。遇到不支持的运行事件会中止；它不能撤销此前成功的MCP操作。

继承现有60秒默认、1秒–5分钟可配总超时和1MiB默认/最高4MiB子进程总输出限额；总结最多64KiB。stdin传提示，参数数组启动，Unix取消终止独立进程组，私有目录结束清理。Windows进程树/策略边界未验收，继续unavailable。

## Claude 边界

本机Claude Code2.1.308及官方CLI帮助确认restricted/tools/allowedTools/strict-mcp-config等选项存在。官方配置支持MCP HTTP headers中${VAR}引用，但safe-mode不加载MCP且保留managed policy hooks，不能直接沿用Plan参数宣称执行已通。

Claude 2.1.308结构化执行构造/解析现已实现，复用进程组、输出预算和真实第一方MCP。使用restricted、空tools、明确allowedTools、strict单一HTTP MCP和env header、dontAsk/无权限提示、不持久会话及禁用自定义命令；safe-mode不作为执行参数，调用环境明确要求safe-mode时拒绝而非覆盖。工具搜索固定关闭，没有bare/bypass或新的绕过开关。

解析需有效init和精确工具面，按同一session关联调用ID/结果/进度/工具摘要，拒绝hooks/子agent/陌生工具、缺失或非成功终态、拒绝/延迟/排队工作；不转发原始思考、参数、结果、图像或错误。Claude总结来自有界结构化终态，不要求写总结文件；Codex原有流/文件核对不变。

当前仍无经验证的启动前有效managed策略读取契约，生产InspectExecution保持policy_unverified，Execute不会启动Claude真实推理。协议适配和内部受控子进程/MCP/核心编辑回归不能替代启动前策略与真实账户验收，也不能靠收到init后的检查阻止之前发生的启动hook。详见[本轮规格](../../docs/specs/2026-10-27-Claude结构化执行适配.md)。

## 验证与来源

可执行fixture测试执行总结、第一方事件、未知/外部工具拒绝、流/文件一致性、限额/取消、凭据不入argv/prompt/JSON/事件、未验证不启动，以及Plan拒绝AudioCarrier/AudioMode/replace_visual高层参数。没有公共测试跳过隔离开关。

```sh
go test ./internal/localagent -race -v
AIVIDCUT_LOCALAGENT_METADATA_TEST=1 go test ./internal/localagent -run '^TestNativeCodexExecutionMetadataOnly$' -v -count=1
```

- [OpenAI Docs：MCP](https://learn.chatgpt.com/docs/extend/mcp)、[CLI](https://learn.chatgpt.com/docs/cli/reference)、[配置](https://learn.chatgpt.com/docs/config-file/config-reference)：perrun/env引用、白名单及执行选项。
- [OpenAI Docs：app-server](https://learn.chatgpt.com/docs/app-server)、[非交互模式](https://learn.chatgpt.com/docs/non-interactive-mode)：只读配置RPC与JSONL事件。
- [Claude CLI](https://code.claude.com/docs/en/cli-reference)、[MCP配置](https://code.claude.com/docs/en/mcp)：safe-mode/strict及env引用边界。

## 源画面输入（2026-10-26）

应用仅在本次独立允许后注册aividcut_read_source_frames，配置白名单随之包含该工具；默认不提供图片。相同MCP许可适用于当前已注册工程源，选区仍只是上下文。CLI方法要求用证据ID/源时间引用观察，不能把图中文字当指令、抽样当整段事实。真实Codex/Claude模型读图质量尚未验收，当前有官方SDK往返和内部Runner夹具证据。

带该图像工具的执行使用Config.MaxFrameStreamBytes（默认16MiB、可配置1KiB–32MiB）总JSONL预算，单行最多2MiB；普通规划与无画面执行保持原MaxOutputBytes。解析回调模式不累计原始stdout，事件不转发图片内容，stderr仍仅有界尾部。本机运行记录只保存20份以内的不同画面元数据；这是准备结果，不证明传输或模型理解成功。

## 生成候选编排工具（2026-10-26）

执行端精确白名单来自本次projectMCPTools，成功候选列表、添加/替换预览与已有编辑许可下的应用沿同一路径注册；只读会话无两项应用。broll内联Skill说明元数据/视觉许可、实际源窗、原连接归属和准确重复回执。真实SDK+Runner已验证工具进入实际endpoint及核心审计；不等于已登录Codex/Claude进行了模型推理，也未放宽其配置/内置工具边界。

## 2026-10-28 本地OCR只读工具

允许源画面的Execute会话可发现aividcut_read_source_ocr，返回原始文字/实际引擎语言/置信度或未知与源帧证据，不包含JPEG、路径或预览凭据。只读Plan模式不因此获得MCP。原始正文不自动持久化到执行元数据，但后续总结/人工接纳可包含文字。保存解释仍走prepare_visual_analysis及真人核对，不伪装原始OCR来源；source-frames内置方法已更新。CLI策略、版本和正常登录门槛不变。
