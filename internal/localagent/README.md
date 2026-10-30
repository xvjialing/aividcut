# 本地 CLI 规划基础

Plan快照模式检测本机CLI，并将受限会话输出解析成 `editor.EditRequest`，实际应用由服务另行提交。本包不直接写工程文件、不运行工作流脚本或自动登录/复制认证文件；新的Execute模式通过App受控MCP逐批操作。

另有独立的第一方MCP执行模式 `InspectExecution`/`Execute`，说明与真实配置证据见 [EXECUTION.md](EXECUTION.md)。它不沿用Plan空MCP证据，不伪造EditRequest或CoreValidated，实际编辑交由App工具和核心守卫。

## 接口

- `Discover(ctx) []Installation`、`Inspect(ctx, provider, executable) Installation`：普通本机安装/版本/登录状态概况。
- `NewPlanner(Config) (*Planner,error)`：设置超时、总输出上限、独立状态与临时目录。
- `Planner.Inspect(ctx, provider) Installation`：对应独立 CLI 状态作用域的检测。
- `Planner.VerifyIsolation(ctx, provider) Isolation`：重新检查隔离条件，返回逐项 `Checks`，不启动推理。
- `Planner.LoginInstructions(ctx, provider) (LoginCommand,error)`：仅返回 CLI 正常登录的 executable/args/env 作用域，由用户决定执行；没有登录执行功能。
- `Planner.Plan(ctx, PlanInput, func(Event)) (PlanResult,error)`：仅当隔离已证明且对应作用域登录时启动，返回计划及副本核心校验标记。
- `ParseEditRequest(data,maximum)`、`ValidatePlan(project,request)`：严格 JSON 和无副作用的核心校验。
- `Workflows(project)`：内置中文工作流说明和前置数据状态。

Provider 为 codex/claude；PlanInput 含工程快照、明确要求、工作流和可选片段选区。Installation 将 Installed、AuthState、Isolation 分开，不把本机登录等同于受限规划可用。

## 当前真实状态

本机只读帮助核查：Codex 0.164.0、Claude Code 2.1.308。2026-10-25 使用临时独立 Codex 作用域实测：配置与特性检查通过、MCP 列表为空、启动 requirements 为 null；登录状态为 signed_out，整体状态为 `needs_login`。没有真实推理或账户消费验收，也没有接通 MCP 写入。

Codex 的可用性由真实检查结果计算，不再固定为 false。独立目录正常登录后，每次检测都会重新读取该账户的启动配置；全部条件通过才进入 `ready` / `Supported=true`。初版仅放行已核查的 Codex 0.164.0 与 Darwin/Linux 进程适配；升级版本需复核配置和事件协议。

Claude 当前生产状态仍为 `policy_unverified`：2.1.308执行协议适配已实现，safe-mode仍允许策略hooks，目前缺少已验证的启动前有效策略检查；真实推理入口未开放。Windows Job进程树代码已进入共享managedprocess，原生验收程序已交接，但实际API/管理配置尚未证明，仍为 `platform_unverified`。不提供 allowUnsafe 或无条件降级。

| 状态 | 含义与后续动作 |
| --- | --- |
| `profile_required` | 配置绝对 StateDir；用新的私有目录正常登录，不复制既有配置或凭据 |
| `needs_login` | 参数和当前作用域配置已检查；按 LoginInstructions 登录后重查账户策略 |
| `unsupported_version` | 缺参数或版本协议未验证，先核查该版本 |
| `policy_unverified` | 管理策略存在或无法确认，需实现策略兼容验证，不绕过策略 |
| `features_unverified` / `mcp_unverified` | 有效禁用未通过，不启动规划 |
| `working_directory_unverified` | 临时目录存在祖先项目配置，改用应用私有目录 |
| `platform_unverified` | 平台隔离未完成 |
| `ready` | 当前配置契约通过；可生成候选计划，提交仍走核心 |

`Checks` 的每项提供 ID、pass/fail/unknown、原因和后续动作。检测不读取认证文件内容，不持久化原始 CLI 配置。调查证据与后续路线见 `work/localagent-isolation-assessment.md`。

调用方应将 StateDir 放入应用私有数据目录，WorkDir 留空或指向无项目配置的私有临时目录。调用 LoginInstructions 后展示其 executable/args 和对应环境作用域，由用户在正常终端登录。不要自动执行该指令，不将密钥粘贴进应用配置，不复用或复制原 CODEX_HOME 的 auth.json。

## 安全与协议

- 子进程使用参数数组和 stdin，不拼接 shell。普通模式不读取或复制 auth.json，登录状态仅由 CLI 自身的 status 命令提供。
- Codex 参数包含 read-only、never approvals、ephemeral、ignore-user-config、独立 CWD、结构化输出及禁用 shell/apps/plugins/hooks 等特性；空 mcp_servers 表不被当作已清空的证据。
- Claude 构造 safe-mode/restricted、空内置工具、禁 MCP 工具、strict 空 MCP、无持久会话和结构化输出。官方说明 safe-mode 仍允许管理 hooks，因此不能仅凭参数宣称已完成隔离。
- 管理策略不能被应用绕过。检测缺失、版本不支持或策略未知均保留不可用原因。
- Codex 在无额外用户配置的独立目录内，使用同一组限制读取 features list、mcp list 和 app-server --print-startup-config。最后一条只打印配置，不启动服务。遇到任何 requirements 或未确认的本机管理配置，初版保守阻止；不推断策略一定有害。
- MCP 元数据列表不是 exec 的工具目录。契约要求独立目录无用户配置、临时 CWD 及祖先无项目配置；exec 额外使用 ignore-user-config/ignore-rules，移除用户及项目带来的差异。配置检查仍是快照，不能保证其他进程或管理员之后不改变策略。
- unified_exec 的用户禁用在本机版本会被忽略，因此适配不依赖此开关；shell_tool=false 才用于禁命令工具。文件修改由 read-only 与 never approvals 约束。
- 事件中的工具调用/文件修改、外部 MCP、权限与 hook 信号会使规划失败。此检查是第二道防线，不能撤销已经发生的启动副作用。
- Codex JSONL 事件和最终文件需一致；Claude 使用 structured_output，缺失时不降级解析自由文字。
- 计划绑定原 revision 与本次幂等键，限制命令数/输出量，禁止模型导入/重定位素材、改变时基或自行解除锁轨。选区和音画链接受限。
- 校验只在新建编辑核心副本中运行，不改变原工程；实际提交仍须再次检查当前 revision。
- 规划上下文剥离素材路径、URL、缩略图 URL 和指纹；不向调用方转发原始认证输出或模型思考文本。
- Unix 取消终止独立进程组；Windows 当前仅保证直接进程取消，子进程树的完整隔离尚未验证，因此真实规划继续禁用。

## 验证

`go test ./internal/localagent -race -v` 使用真正的本地可执行 fixture 子进程，不接触模型账户。覆盖两个协议、成功计划与副本校验、工具事件拒绝、错误版本/键、越权素材命令、输出上限、超时、前置数据缺失、环境变量白名单、敏感状态脱敏和原JSON完整性。

`AIVIDCUT_LOCALAGENT_METADATA_TEST=1 go test ./internal/localagent -run TestNativeCodexMetadataOnly -v -count=1` 可选执行本机 CLI 的只读元数据检查，不调用 Plan；默认测试会明确跳过此项。隔离状态矩阵通过独立可执行 fixture 测试 ready、待登录、工具未禁用、外部 MCP、管理策略和不完整诊断，未伪造本机账户登录。

fixture 的私有测试注入仅存在于测试代码，生产 API 不暴露跳过隔离的参数。测试通过不代表本机 CLI 推理、账户权限或 MCP 写入已接通。

官方依据：[Codex CLI](https://learn.chatgpt.com/docs/cli/reference)、[Codex 配置](https://learn.chatgpt.com/docs/config-file/config-reference)、[Claude CLI](https://code.claude.com/docs/en/cli-reference)、[Claude 程序化模式](https://code.claude.com/docs/en/headless)。
