package localagent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

func Discover(ctx context.Context) []Installation {
	return []Installation{Inspect(ctx, Codex, ""), Inspect(ctx, Claude, "")}
}

func Inspect(ctx context.Context, provider Provider, executable string) Installation {
	return inspectInstallation(ctx, provider, executable, "")
}

func inspectInstallation(ctx context.Context, provider Provider, executable, profile string) Installation {
	info := Installation{Provider: provider, AuthState: "unknown", AuthReason: "尚未检查登录状态", ProfileDirectory: profile,
		Isolation: Isolation{Status: "unverified", Reason: "尚未证明此会话的有效工具与管理策略隔离，不能启动推理", MissingFlags: []string{}, Evidence: []string{}, Checks: []IsolationCheck{}, Limitations: []string{"CLI 参数不是独立操作系统安全边界；未知策略不可视为已禁用。", "未进行真实账户推理或接通MCP；fixture验证不等于提供商推理验收。"}}}
	if !knownProvider(provider) {
		info.Isolation.Reason = "未知 CLI 类型"
		return info
	}
	if executable == "" {
		executable = string(provider)
	}
	path, err := exec.LookPath(executable)
	if err != nil {
		info.AuthReason = "本机未找到该 CLI，请使用其官方安装方式"
		return info
	}
	path, err = filepath.Abs(path)
	if err != nil {
		info.AuthReason = "CLI 路径无法解析"
		return info
	}
	info.Path, info.Installed = path, true
	if runtime.GOOS == "windows" && strings.ToLower(filepath.Ext(path)) != ".exe" {
		info.Isolation.Reason = "当前受限适配只支持原生 .exe，不通过 cmd/PowerShell 执行安装脚本 shim"
		return info
	}
	if profile != "" {
		if !filepath.IsAbs(profile) {
			info.Isolation.Reason = "独立 CLI 状态目录必须是绝对路径"
			return info
		}
		if err := os.MkdirAll(profile, 0700); err != nil {
			info.Isolation.Reason = "独立 CLI 状态目录不可用"
			return info
		}
		if stat, err := os.Lstat(profile); err != nil || !stat.IsDir() || stat.Mode()&os.ModeSymlink != 0 || runtime.GOOS != "windows" && stat.Mode().Perm()&0077 != 0 {
			info.Isolation.Reason = "独立 CLI 状态目录需为私有普通目录，不能使用符号链接或开放权限"
			return info
		}
	}
	directory, err := os.MkdirTemp("", "aividcut-cli-inspect-")
	if err != nil {
		info.AuthReason = "不能创建只读检测工作目录"
		return info
	}
	defer os.RemoveAll(directory)
	if check := isolatedWorkingDirectory(directory); check.Status != "pass" {
		info.Isolation.Reason, info.Isolation.Status = check.Reason, "working_directory_unverified"
		info.Isolation.Checks = append(info.Isolation.Checks, check)
		return info
	}
	version, _, err := metadata(ctx, provider, path, directory, profile, "--version")
	if err == nil {
		info.Version = versionNumber(version)
	}
	if info.Version == "" {
		info.AuthReason = "CLI 版本无法识别；请在命令行检查安装"
		return info
	}
	var help []byte
	if provider == Codex {
		main, _, _ := metadata(ctx, provider, path, directory, profile, "--help")
		execHelp, _, _ := metadata(ctx, provider, path, directory, profile, "exec", "--help")
		help = append(main, execHelp...)
	} else {
		help, _, _ = metadata(ctx, provider, path, directory, profile, "--help")
	}
	for _, flag := range requiredFlags(provider) {
		if !hasFlag(help, flag) {
			info.Isolation.MissingFlags = append(info.Isolation.MissingFlags, flag)
		}
	}
	if len(info.Isolation.MissingFlags) > 0 {
		info.Isolation.Reason = "当前版本缺少受限规划所需选项：" + strings.Join(info.Isolation.MissingFlags, ", ")
	} else {
		info.Isolation.Status = "arguments_ready"
		info.Isolation.Evidence = append(info.Isolation.Evidence, "本机帮助列出了受限模式和结构化输出所需选项")
		if provider == Codex {
			info.Isolation.Reason = "跳过用户配置及禁用特性仍需核查有效MCP和管理hooks；本轮未验证前不启动真实规划"
		} else {
			info.Isolation.Reason = "safe-mode 保留策略hooks，需核查管理策略与有效工具面；本轮未验证前不启动真实规划"
		}
	}
	var out, stderr []byte
	if provider == Codex {
		out, stderr, err = metadata(ctx, provider, path, directory, profile, "login", "status")
	} else {
		out, stderr, err = metadata(ctx, provider, path, directory, profile, "auth", "status", "--json")
	}
	info.AuthState, info.AuthReason = parseAuth(provider, out, stderr, err)
	probe := func(args ...string) ([]byte, []byte, error) {
		return metadata(ctx, provider, path, directory, profile, args...)
	}
	verifyInstallationIsolation(ctx, &info, profile, probe, localPolicyCheck)
	return info
}

func requiredFlags(provider Provider) []string {
	if provider == Codex {
		return []string{"--no-daemon", "--ask-for-approval", "--ignore-user-config", "--ignore-rules", "--skip-git-repo-check", "--sandbox", "--ephemeral", "--json", "--output-schema", "--output-last-message"}
	}
	return []string{"--safe-mode", "--restricted", "--tools", "--disallowedTools", "--strict-mcp-config", "--mcp-config", "--settings", "--setting-sources", "--disable-slash-commands", "--no-chrome", "--permission-mode", "--permission-prompts", "--no-session-persistence", "--output-format", "--verbose", "--include-partial-messages", "--json-schema", "--print"}
}

func hasFlag(help []byte, flag string) bool {
	pattern := regexp.MustCompile(`(^|[\s,])` + regexp.QuoteMeta(flag) + `([\s,=]|$)`)
	return pattern.Match(help)
}

var versionPattern = regexp.MustCompile(`\b[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9._-]+)?\b`)

func versionNumber(output []byte) string { return versionPattern.FindString(string(output)) }

func parseAuth(provider Provider, stdout, stderr []byte, err error) (string, string) {
	if provider == Claude {
		var status struct {
			LoggedIn *bool  `json:"loggedIn"`
			Method   string `json:"authMethod"`
		}
		if json.Unmarshal(stdout, &status) == nil {
			if status.LoggedIn != nil && *status.LoggedIn && err == nil {
				return "signed_in", "CLI 报告已登录；认证信息由 CLI 自身管理"
			}
			if status.LoggedIn != nil && !*status.LoggedIn || status.Method == "none" {
				return "signed_out", "CLI 报告未登录，请在对应状态目录正常完成 claude auth login"
			}
		}
	} else {
		combined := bytes.ToLower(append(append([]byte(nil), stdout...), stderr...))
		if bytes.Contains(combined, []byte("not logged in")) {
			return "signed_out", "CLI 报告未登录，请在对应状态目录正常完成 codex login"
		}
		if err == nil && bytes.Contains(combined, []byte("logged in")) {
			return "signed_in", "CLI 报告已登录；认证信息由 CLI 自身管理"
		}
	}
	return "unknown", "登录状态无法确认；未读取或复制认证文件，请在 CLI 正常界面检查"
}
