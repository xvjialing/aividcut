package localagent

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"time"
)

func localPolicyCheck(ctx context.Context, provider Provider) IsolationCheck {
	check := IsolationCheck{ID: "local_management", Status: "unknown", Reason: "本机管理配置无法确认", NextAction: "由管理员核查策略，应用不会跳过管理配置"}
	var paths []string
	if provider == Codex {
		paths = []string{"/etc/codex/config.toml", "/etc/codex/requirements.toml", "/etc/codex/managed_config.toml", "/etc/codex/hooks.json"}
	} else if runtime.GOOS == "darwin" {
		paths = []string{"/Library/Application Support/ClaudeCode/managed-settings.json", "/Library/Application Support/ClaudeCode/managed-settings.d", "/Library/Application Support/ClaudeCode/managed-mcp.json"}
	} else {
		paths = []string{"/etc/claude-code/managed-settings.json", "/etc/claude-code/managed-settings.d", "/etc/claude-code/managed-mcp.json"}
	}
	for _, path := range paths {
		if _, err := os.Lstat(path); err == nil {
			check.Reason = "本机存在管理配置；当前契约未验证该策略，不能绕过后启动"
			return check
		} else if !os.IsNotExist(err) {
			return check
		}
	}
	if runtime.GOOS == "darwin" {
		if !managedPreferencesAbsent(ctx, provider) {
			check.Reason = "macOS 管理配置存在或无法核查；不能将其视为已关闭"
			return check
		}
	} else if runtime.GOOS != "linux" {
		return check
	}
	return IsolationCheck{ID: check.ID, Status: "pass", Reason: "指定系统文件及本机管理配置键未观察到；账户策略由 CLI 启动诊断另查"}
}

// 固定脚本仅通过系统 CoreFoundation 读取指定键的存在性，不输出或保存配置内容。
// 直接执行系统程序，参数不进入 shell，也不使用用户提供的脚本。
func managedPreferencesAbsent(ctx context.Context, provider Provider) bool {
	domain := "com.openai.codex"
	keys := []string{"requirements_toml_base64", "config_toml_base64"}
	if provider == Claude {
		domain = "com.anthropic.claudecode"
		keys = []string{"hooks", "disableAllHooks", "policyHelper", "managedSourcesBehavior", "statusLine", "fileSuggestion", "allowedMcpServers", "deniedMcpServers", "allowManagedMcpServersOnly", "allowManagedHooksOnly"}
	}
	domainJSON, _ := json.Marshal(domain)
	keysJSON, _ := json.Marshal(keys)
	script := `ObjC.import("CoreFoundation"); var values = {}; ` + string(keysJSON) + `.forEach(function(key) { var ref = $.CFPreferencesCopyAppValue($(key), $(` + string(domainJSON) + `)); var object = ObjC.castRefToObject(ref); values[key] = {present: !object.isNil(), forced: Boolean($.CFPreferencesAppValueIsForced($(key), $(` + string(domainJSON) + `)))}; }); JSON.stringify(values);`
	limited, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	output, _, err := run(limited, commandSpec{executable: "/usr/bin/osascript", args: []string{"-l", "JavaScript", "-e", script}, environment: []string{"PATH=/usr/bin:/bin", "LANG=C"}}, 8<<10, nil)
	if err != nil {
		return false
	}
	var values map[string]struct {
		Present bool `json:"present"`
		Forced  bool `json:"forced"`
	}
	if json.Unmarshal(output, &values) != nil || len(values) != len(keys) {
		return false
	}
	for _, key := range keys {
		value, found := values[key]
		if !found || value.Present || value.Forced {
			return false
		}
	}
	return true
}
