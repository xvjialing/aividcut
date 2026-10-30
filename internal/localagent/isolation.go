package localagent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// unified_exec 在已核查版本中不能关闭；禁用 shell_tool 才移除命令工具。
var restrictedCodexFeatures = []string{
	"shell_tool", "shell_snapshot", "shell_snapshot_v2", "apps", "plugins", "remote_plugin", "hooks",
	"multi_agent", "multi_agent_v2", "agent_message_board", "goals", "memories", "external_agent_memory_import",
	"browser_use", "browser_use_external", "computer_use", "in_app_local_automation",
	"code_mode", "code_mode_host", "code_mode_prewarm", "image_generation", "view_image", "multi_env_copy_file",
	"skill_mcp_dependency_install", "skill_search", "sleep_tool", "tool_suggest", "workspace_dependencies",
	"request_permissions_tool", "standalone_web_search", "daemon_auto_start",
}

type metadataProbe func(...string) ([]byte, []byte, error)

func restrictionArguments() []string {
	var args []string
	for _, feature := range restrictedCodexFeatures {
		args = append(args, "--disable", feature)
	}
	for _, config := range []string{`web_search="disabled"`, `sandbox_mode="read-only"`, `approval_policy="never"`, `mcp_servers={}`, `plugins={}`} {
		args = append(args, "--config", config)
	}
	return args
}

func addIsolationCheck(isolation *Isolation, check IsolationCheck) bool {
	isolation.Checks = append(isolation.Checks, check)
	return check.Status == "pass"
}

// 验证限定于已核查 CLI 的配置契约；不把任意版本的相同参数视为相同行为。
// 正常登录后重新检查会走到 ready，失败或未知条件均保持关闭。
func verifyInstallationIsolation(ctx context.Context, info *Installation, profile string, probe metadataProbe, policies func(context.Context, Provider) IsolationCheck) {
	isolation := &info.Isolation
	isolation.Supported = false
	isolation.Checks = []IsolationCheck{}
	if !addIsolationCheck(isolation, profileCheck(profile)) {
		setIsolationState(info, "profile_required")
		return
	}
	if len(isolation.MissingFlags) > 0 {
		addIsolationCheck(isolation, IsolationCheck{ID: "arguments", Status: "fail", Reason: "CLI 缺少受限模式所需参数", NextAction: "使用官方支持的版本后重新检测"})
		setIsolationState(info, "unsupported_version")
		return
	}
	addIsolationCheck(isolation, IsolationCheck{ID: "arguments", Status: "pass", Reason: "本机帮助列出了规划和隔离参数"})
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		addIsolationCheck(isolation, IsolationCheck{ID: "process_boundary", Status: "unknown", Reason: "当前平台尚未验证完整子进程树取消和管理策略读取", NextAction: "完成该平台的进程树与策略适配后重查"})
		setIsolationState(info, "platform_unverified")
		return
	}
	addIsolationCheck(isolation, IsolationCheck{ID: "process_boundary", Status: "pass", Reason: "使用独立 Unix 进程组，取消时终止整组"})
	if info.Provider == Claude {
		addIsolationCheck(isolation, policies(ctx, info.Provider))
		addIsolationCheck(isolation, IsolationCheck{ID: "effective_policy_hooks", Status: "unknown", Reason: "safe-mode 仍允许策略 hooks；该版本尚无已验证的启动前有效策略读取协议", NextAction: "补齐官方策略诊断或独立操作系统隔离；不会降级启动"})
		setIsolationState(info, "policy_unverified")
		return
	}
	if info.Version != "0.164.0" {
		addIsolationCheck(isolation, IsolationCheck{ID: "protocol_version", Status: "unknown", Reason: "该 Codex 版本尚未核查启动配置诊断协议", NextAction: "核查该版本官方帮助、有效配置和事件格式后加入适配"})
		setIsolationState(info, "unsupported_version")
		return
	}
	addIsolationCheck(isolation, IsolationCheck{ID: "protocol_version", Status: "pass", Reason: "Codex 0.164.0 的配置诊断和事件协议已核查"})
	if !addIsolationCheck(isolation, policies(ctx, info.Provider)) {
		setIsolationState(info, "policy_unverified")
		return
	}
	// 这些命令只读 CLI 的配置，不创建聊天、不启动 MCP、不调用模型。
	args := restrictionArguments()
	features, _, err := probe(append([]string{"features", "list"}, args...)...)
	if err != nil || !restrictedFeaturesDisabled(features) {
		addIsolationCheck(isolation, IsolationCheck{ID: "effective_features", Status: "unknown", Reason: "不能确认 shell、hooks、插件、应用及辅助工具均被有效禁用", NextAction: "检查 CLI 诊断或管理策略；不绕过要求"})
		setIsolationState(info, "features_unverified")
		return
	}
	addIsolationCheck(isolation, IsolationCheck{ID: "effective_features", Status: "pass", Reason: "CLI 有效特性列表确认受限特性均为 false"})
	servers, _, err := probe(append([]string{"mcp", "list", "--json"}, args...)...)
	if err != nil || !noMCPServers(servers) {
		addIsolationCheck(isolation, IsolationCheck{ID: "effective_mcp", Status: "unknown", Reason: "独立 CLI 作用域仍有 MCP 配置，或列表无法确认", NextAction: "使用仅通过正常登录创建的独立状态目录；不复制现有配置"})
		setIsolationState(info, "mcp_unverified")
		return
	}
	addIsolationCheck(isolation, IsolationCheck{ID: "effective_mcp", Status: "pass", Reason: "独立作用域的 CLI MCP 配置列表为空"})
	startup, _, err := probe(append([]string{"app-server", "--print-startup-config"}, args...)...)
	if err != nil || !unmanagedStartupConfiguration(startup, info.Version) {
		addIsolationCheck(isolation, IsolationCheck{ID: "effective_requirements", Status: "unknown", Reason: "CLI 报告存在管理要求，或启动配置诊断无法确认", NextAction: "由管理员确认策略与受限规划兼容，再实现该策略的验证；不会覆盖策略"})
		setIsolationState(info, "policy_unverified")
		return
	}
	addIsolationCheck(isolation, IsolationCheck{ID: "effective_requirements", Status: "pass", Reason: "CLI 仅打印启动配置，当前作用域的有效 requirements 为 null；未启动 app-server"})
	if info.AuthState != "signed_in" {
		addIsolationCheck(isolation, IsolationCheck{ID: "scope_authentication", Status: "unknown", Reason: "独立作用域尚未确认登录；登录后的账户策略需重新核查", NextAction: "使用 LoginInstructions 返回的作用域正常登录，再次 Inspect 或 VerifyIsolation"})
		setIsolationState(info, "needs_login")
		return
	}
	addIsolationCheck(isolation, IsolationCheck{ID: "scope_authentication", Status: "pass", Reason: "CLI 自身报告对应独立作用域已登录"})
	isolation.Supported, isolation.Status = true, "ready"
	isolation.Reason = "独立作用域的有效配置检查通过，可启动受限计划生成；实际编辑仍由 Go 核心另行校验提交"
	isolation.Evidence = append(isolation.Evidence, "启动配置、特性及MCP均由同一CLI和独立作用域重新读取；每次Plan都会重查")
	isolation.Limitations = append(isolation.Limitations, "MCP列表不是exec工具目录；此契约要求独立目录无用户配置，exec额外跳过用户配置并使用同一组更严格参数。", "这是当前配置快照的验证，无法保证之后管理员或其他进程不改变策略；不支持的事件仍会中止规划。")
}

func setIsolationState(info *Installation, status string) {
	info.Isolation.Status = status
	for _, check := range info.Isolation.Checks {
		if check.Status != "pass" {
			info.Isolation.Reason = check.Reason
			return
		}
	}
}

func profileCheck(profile string) IsolationCheck {
	check := IsolationCheck{ID: "dedicated_profile", Status: "fail", Reason: "需要 AIVidCut 独立的私有 CLI 状态目录", NextAction: "配置绝对 StateDir，并使用 CLI 在该作用域正常登录"}
	if profile == "" || !filepath.IsAbs(profile) {
		return check
	}
	stat, err := os.Lstat(profile)
	if err != nil || !stat.IsDir() || stat.Mode()&os.ModeSymlink != 0 || runtime.GOOS != "windows" && stat.Mode().Perm()&0077 != 0 {
		return check
	}
	for _, name := range []string{"config.toml", "hooks.json", "requirements.toml", "managed_config.toml", "settings.json", "settings.local.json", ".mcp.json"} {
		if _, err := os.Lstat(filepath.Join(profile, name)); err == nil || !os.IsNotExist(err) {
			check.Reason = "独立 CLI 状态目录存在额外配置或 hooks，当前契约无法确认其来源"
			check.NextAction = "使用新的独立目录正常登录；应用不会删除或改写现有配置"
			return check
		}
	}
	return IsolationCheck{ID: check.ID, Status: "pass", Reason: "独立私有目录未观察到用户配置或启动 hooks；未读取认证文件"}
}

func isolatedWorkingDirectory(directory string) IsolationCheck {
	check := IsolationCheck{ID: "working_directory", Status: "unknown", Reason: "临时工作目录或其祖先存在 Codex 项目配置，不能证明会话不会加载它", NextAction: "使用无项目配置的应用私有临时目录"}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return check
	}
	for current := resolved; ; current = filepath.Dir(current) {
		for _, name := range []string{"config.toml", "hooks.json"} {
			_, err := os.Lstat(filepath.Join(current, ".codex", name))
			if err == nil || !os.IsNotExist(err) {
				return check
			}
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return IsolationCheck{ID: check.ID, Status: "pass", Reason: "临时工作目录及祖先未观察到项目 Codex 配置或 hooks"}
}

func restrictedFeaturesDisabled(output []byte) bool {
	values := map[string]string{}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			values[fields[0]] = fields[len(fields)-1]
		}
	}
	for _, feature := range restrictedCodexFeatures {
		if values[feature] != "false" {
			return false
		}
	}
	return true
}

func noMCPServers(data []byte) bool {
	if len(bytes.TrimSpace(data)) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return false
	}
	var servers []json.RawMessage
	return json.Unmarshal(data, &servers) == nil && len(servers) == 0
}

func unmanagedStartupConfiguration(data []byte, version string) bool {
	var startup struct {
		Version       string `json:"appServerVersion"`
		Configuration struct {
			Requirements json.RawMessage `json:"requirements"`
		} `json:"configRequirements"`
	}
	return json.Unmarshal(data, &startup) == nil && startup.Version == version && bytes.Equal(bytes.TrimSpace(startup.Configuration.Requirements), []byte("null"))
}
