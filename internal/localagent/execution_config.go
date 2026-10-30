package localagent

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const executionServerName = "aividcut"
const executionTokenVariable = "AIVIDCUT_MCP_BEARER"

var firstPartyToolName = regexp.MustCompile(`^aividcut_[A-Za-z0-9_]{1,110}$`)
var executionTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,512}$`)

func checkedExecutionMCP(config ExecutionMCP) (ExecutionMCP, error) {
	endpoint, err := url.Parse(config.URL)
	if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" || endpoint.User != nil || endpoint.Path != "/mcp" || endpoint.RawPath != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return ExecutionMCP{}, failure("execution_endpoint", "执行仅允许App提供的127.0.0.1 MCP连接", ErrUnsupported)
	}
	port, err := strconv.Atoi(endpoint.Port())
	if err != nil || port < 1 || port > 65535 {
		return ExecutionMCP{}, failure("execution_endpoint", "MCP端口无效", ErrUnsupported)
	}
	if !executionTokenPattern.MatchString(config.Token) {
		return ExecutionMCP{}, failure("execution_credentials", "第一方MCP会话凭据无效", ErrUnsupported)
	}
	if len(config.Tools) < 1 || len(config.Tools) > 64 {
		return ExecutionMCP{}, failure("execution_tools", "需要1–64个App提供的第一方工具名", ErrUnsupported)
	}
	config.Tools = append([]string(nil), config.Tools...)
	sort.Strings(config.Tools)
	for index, name := range config.Tools {
		if !firstPartyToolName.MatchString(name) || strings.Contains(name, config.Token) || index > 0 && config.Tools[index-1] == name {
			return ExecutionMCP{}, failure("execution_tools", "执行工具必须是唯一的aividcut命名工具", ErrUnsupported)
		}
	}
	return config, nil
}

func executionOverrides(config ExecutionMCP) []string {
	quoted := make([]string, len(config.Tools))
	for index, name := range config.Tools {
		quoted[index] = strconv.Quote(name)
	}
	value := `mcp_servers.aividcut={url=` + strconv.Quote(config.URL) + `,bearer_token_env_var="` + executionTokenVariable + `",enabled=true,required=true,enabled_tools=[` + strings.Join(quoted, ",") + `],default_tools_approval_mode="approve",startup_timeout_sec=10,tool_timeout_sec=60}`
	return []string{"--config", value}
}

func executionEnvironment(base []string, token string) []string {
	var result []string
	noProxy := "127.0.0.1,localhost"
	for _, entry := range base {
		key, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, executionTokenVariable) {
			continue
		}
		if strings.EqualFold(key, "NO_PROXY") {
			if value != "" {
				noProxy += "," + value
			}
			continue
		}
		result = append(result, entry)
	}
	return append(result, executionTokenVariable+"="+token, "NO_PROXY="+noProxy)
}

func (p *Planner) InspectExecution(ctx context.Context, provider Provider) Installation {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Installation{Provider: provider, Isolation: Isolation{Status: "unverified", Reason: "无法创建隔离诊断的临时凭据"}}
	}
	config := ExecutionMCP{URL: "http://127.0.0.1:9/mcp", Token: base64.RawURLEncoding.EncodeToString(random[:]), Tools: []string{"aividcut_get_project"}}
	return p.inspectExecution(ctx, provider, config)
}

func (p *Planner) inspectExecution(ctx context.Context, provider Provider, config ExecutionMCP) Installation {
	if p.executionInspector != nil {
		return p.executionInspector(ctx, provider, config)
	}
	info := p.Inspect(ctx, provider)
	// 快照模式空MCP证据不作为执行契约证据向前端显示。
	checks := []IsolationCheck{}
	for _, check := range info.Isolation.Checks {
		if check.ID != "effective_mcp" {
			checks = append(checks, check)
		}
	}
	info.Isolation.Checks = checks
	info.Isolation.Supported = false
	info.Isolation.Evidence = []string{"执行模式将重新验证单一第一方MCP及工具白名单；不是快照规划的空MCP模式"}
	if provider == Claude {
		if info.Version == claudeExecutionVersion && info.Installed {
			addIsolationCheck(&info.Isolation, IsolationCheck{ID: "execution_protocol_adapter", Status: "pass", Reason: "Claude 2.1.308结构化执行适配已实现；协议回归不等于真实模型或启动策略验收"})
		}
		addIsolationCheck(&info.Isolation, IsolationCheck{ID: "execution_mcp", Status: "unknown", Reason: "Claude执行协议已接入；safe-mode不加载MCP，restricted仍不能代替启动前managed策略核查", NextAction: "补齐启动前有效策略验证与真实账户验收，不绕过managed策略"})
		info.Isolation.Status = "policy_unverified"
		info.Isolation.Reason = "Claude第一方MCP执行隔离尚未证明，不启动执行"
		return info
	}
	if info.Isolation.Status != "ready" && info.Isolation.Status != "needs_login" {
		return info
	}
	config, err := checkedExecutionMCP(config)
	if err != nil {
		info.Isolation.Status = "execution_config_invalid"
		info.Isolation.Reason = "第一方MCP执行配置无效"
		return info
	}
	directory, err := os.MkdirTemp(p.config.WorkDir, "aividcut-execution-inspect-")
	if err != nil {
		info.Isolation.Status = "working_directory_unverified"
		info.Isolation.Reason = "不能创建独立执行诊断目录"
		return info
	}
	defer os.RemoveAll(directory)
	if check := isolatedWorkingDirectory(directory); check.Status != "pass" {
		addIsolationCheck(&info.Isolation, check)
		setIsolationState(&info, "working_directory_unverified")
		return info
	}
	probe := func(args ...string) ([]byte, []byte, error) {
		limited, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		return run(limited, commandSpec{executable: info.Path, args: args, directory: directory, environment: executionEnvironment(p.environment(provider, p.profile(provider)), config.Token)}, 128<<10, nil)
	}
	args := append(restrictionArguments(), executionOverrides(config)...)
	features, _, err := probe(append([]string{"features", "list"}, args...)...)
	if err != nil || !restrictedFeaturesDisabled(features) {
		addIsolationCheck(&info.Isolation, IsolationCheck{ID: "execution_features", Status: "unknown", Reason: "执行配置下的shell/插件/hooks等禁用未确认"})
		setIsolationState(&info, "features_unverified")
		return info
	}
	list, _, err := probe(append([]string{"mcp", "list", "--json"}, args...)...)
	get, _, getErr := probe(append([]string{"mcp", "get", executionServerName, "--json"}, args...)...)
	if err != nil || getErr != nil || !singleExecutionMCP(list, get, config) {
		addIsolationCheck(&info.Isolation, IsolationCheck{ID: "execution_mcp", Status: "unknown", Reason: "实际CLI未确认唯一第一方MCP、env凭据及工具白名单"})
		setIsolationState(&info, "mcp_unverified")
		return info
	}
	configuration, err := p.readExecutionConfiguration(ctx, info.Path, directory, p.profile(provider), config, args)
	if err != nil || !executionConfigurationMatches(configuration, config) {
		addIsolationCheck(&info.Isolation, IsolationCheck{ID: "execution_effective_config", Status: "unknown", Reason: "CLI有效配置未确认approve模式、只读sandbox或单一第一方MCP约束"})
		setIsolationState(&info, "execution_config_unverified")
		return info
	}
	startup, _, err := probe(append([]string{"app-server", "--print-startup-config"}, args...)...)
	if err != nil || !unmanagedStartupConfiguration(startup, info.Version) {
		addIsolationCheck(&info.Isolation, IsolationCheck{ID: "execution_requirements", Status: "unknown", Reason: "执行配置存在管理要求或无法核查，未绕过"})
		setIsolationState(&info, "policy_unverified")
		return info
	}
	addIsolationCheck(&info.Isolation, IsolationCheck{ID: "execution_mcp", Status: "pass", Reason: "实际CLI确认仅aividcut loopback MCP，凭据为子进程env引用，工具名单精确匹配"})
	addIsolationCheck(&info.Isolation, IsolationCheck{ID: "execution_effective_config", Status: "pass", Reason: "config/read实测只读sandbox/never审批，全部受限特性关闭，第一方MCP默认approve且required"})
	info.Isolation.Limitations = []string{"配置诊断不等于真实模型执行验收；仅允许已配置MCP事件，未知内置工具事件立即失败。", "MCP修改由App核心逐批校验/撤销；停止不回滚已成功提交，收费请求仍需客户端用户确认。"}
	if info.AuthState != "signed_in" {
		info.Isolation.Status = "needs_login"
		info.Isolation.Reason = "执行契约配置已核查，独立CLI作用域仍需用户正常登录后重查账户策略"
		return info
	}
	info.Isolation.Supported = true
	info.Isolation.Status = "execution_ready"
	info.Isolation.Reason = "单一第一方MCP执行配置已验证；实际操作由App核心守卫"
	return info
}

type executionMCPMetadata struct {
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	Tools     []string `json:"enabled_tools"`
	Disabled  []string `json:"disabled_tools"`
	Transport struct {
		Type       string            `json:"type"`
		URL        string            `json:"url"`
		Bearer     string            `json:"bearer_token_env_var"`
		Headers    map[string]string `json:"http_headers"`
		EnvHeaders map[string]string `json:"env_http_headers"`
		Helper     json.RawMessage   `json:"http_headers_helper"`
	} `json:"transport"`
}

func singleExecutionMCP(list, get []byte, config ExecutionMCP) bool {
	var entries []executionMCPMetadata
	var entry executionMCPMetadata
	if json.Unmarshal(list, &entries) != nil || len(entries) != 1 || entries[0].Name != executionServerName || !entries[0].Enabled || json.Unmarshal(get, &entry) != nil {
		return false
	}
	return entry.Name == executionServerName && entry.Enabled && entry.Transport.Type == "streamable_http" && entry.Transport.URL == config.URL && entry.Transport.Bearer == executionTokenVariable && len(entry.Transport.Headers) == 0 && len(entry.Transport.EnvHeaders) == 0 && (len(entry.Transport.Helper) == 0 || string(entry.Transport.Helper) == "null") && len(entry.Disabled) == 0 && equalExecutionTools(entry.Tools, config.Tools)
}
func equalExecutionTools(left, right []string) bool {
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	return strings.Join(left, "\x00") == strings.Join(right, "\x00")
}

func executionConfigurationMatches(data []byte, config ExecutionMCP) bool {
	var result struct {
		Config struct {
			Sandbox  string          `json:"sandbox_mode"`
			Approval string          `json:"approval_policy"`
			Web      string          `json:"web_search"`
			Features map[string]bool `json:"features"`
			MCP      map[string]struct {
				URL         string            `json:"url"`
				Bearer      string            `json:"bearer_token_env_var"`
				Enabled     bool              `json:"enabled"`
				Required    bool              `json:"required"`
				Tools       []string          `json:"enabled_tools"`
				Approval    string            `json:"default_tools_approval_mode"`
				Environment string            `json:"environment_id"`
				Command     string            `json:"command"`
				Headers     map[string]string `json:"http_headers"`
				EnvHeaders  map[string]string `json:"env_http_headers"`
				Helper      json.RawMessage   `json:"http_headers_helper"`
			} `json:"mcp_servers"`
		} `json:"config"`
	}
	if json.Unmarshal(data, &result) != nil || result.Config.Sandbox != "read-only" || result.Config.Approval != "never" || result.Config.Web != "disabled" || len(result.Config.MCP) != 1 {
		return false
	}
	for _, feature := range restrictedCodexFeatures {
		value, found := result.Config.Features[feature]
		if !found || value {
			return false
		}
	}
	entry, found := result.Config.MCP[executionServerName]
	return found && entry.URL == config.URL && entry.Bearer == executionTokenVariable && entry.Enabled && entry.Required && entry.Approval == "approve" && entry.Environment == "local" && entry.Command == "" && len(entry.Headers) == 0 && len(entry.EnvHeaders) == 0 && (len(entry.Helper) == 0 || string(entry.Helper) == "null") && equalExecutionTools(entry.Tools, config.Tools)
}
