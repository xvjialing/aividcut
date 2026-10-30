// Package localagent 将本机 CLI 的检测、受限参数与只读规划分开。
// 规划结果必须经过编辑核心验证；本包不提交工程，也不自动登录或执行工作流脚本。
package localagent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"aividcut/internal/editor"
)

type Provider string

const (
	Codex  Provider = "codex"
	Claude Provider = "claude"
)

var (
	ErrUnsupported  = errors.New("当前 CLI 不能证明所需隔离")
	ErrOutputLimit  = errors.New("CLI 输出超过上限")
	ErrProtocol     = errors.New("CLI 输出协议无效")
	ErrPrerequisite = errors.New("缺少工作流前置数据")
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Cause   error  `json:"-"`
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Cause }

func failure(code, message string, cause error) error {
	return &Error{Code: code, Message: message, Cause: cause}
}

type Isolation struct {
	Supported    bool             `json:"supported"`
	Status       string           `json:"status"`
	Reason       string           `json:"reason"`
	MissingFlags []string         `json:"missingFlags"`
	Evidence     []string         `json:"evidence"`
	Limitations  []string         `json:"limitations"`
	Checks       []IsolationCheck `json:"checks"`
}

// IsolationCheck 仅报告检查结论，不包含原始配置、认证输出或服务器凭据。
type IsolationCheck struct {
	ID         string `json:"id"`
	Status     string `json:"status"` // pass、fail、unknown
	Reason     string `json:"reason"`
	NextAction string `json:"nextAction,omitempty"`
}

type Installation struct {
	Provider         Provider  `json:"provider"`
	Path             string    `json:"path"`
	Installed        bool      `json:"installed"`
	Version          string    `json:"version"`
	AuthState        string    `json:"authState"`
	AuthReason       string    `json:"authReason"`
	ProfileDirectory string    `json:"profileDirectory,omitempty"`
	Isolation        Isolation `json:"isolation"`
}

type Config struct {
	StateDir       string
	WorkDir        string
	Executables    map[Provider]string
	Timeout        time.Duration
	MaxOutputBytes int
	// 画面MCP结果可能出现在JSONL中；独立预算不放宽普通规划输出。
	MaxFrameStreamBytes int
}

type PlanInput struct {
	Provider    Provider       `json:"provider"`
	Project     editor.Project `json:"project"`
	Instruction string         `json:"instruction"`
	Workflow    string         `json:"workflow"`
	Selection   []string       `json:"selection"`
}

type PlanResult struct {
	Provider      Provider           `json:"provider"`
	Request       editor.EditRequest `json:"request"`
	CoreValidated bool               `json:"coreValidated"`
	Warnings      []string           `json:"warnings"`
}

type Event struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

type Workflow struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Available   bool   `json:"available"`
	Reason      string `json:"reason"`
}

type LoginCommand struct {
	Executable  string            `json:"executable"`
	Args        []string          `json:"args"`
	Environment map[string]string `json:"environment"`
	Notice      string            `json:"notice"`
}

type commandSpec struct {
	executable  string
	args        []string
	directory   string
	environment []string
	input       []byte
}

type Planner struct {
	config             Config
	inspect            func(context.Context, Provider, string, string) Installation
	environment        func(Provider, string) []string
	executionInspector func(context.Context, Provider, ExecutionMCP) Installation
}

func NewPlanner(config Config) (*Planner, error) {
	if config.StateDir != "" && !filepath.IsAbs(config.StateDir) {
		return nil, fmt.Errorf("CLI 独立状态目录必须是绝对路径")
	}
	if config.WorkDir != "" && !filepath.IsAbs(config.WorkDir) {
		return nil, fmt.Errorf("CLI 临时工作目录必须是绝对路径")
	}
	if config.Timeout == 0 {
		config.Timeout = 60 * time.Second
	}
	if config.Timeout < time.Second || config.Timeout > 5*time.Minute {
		return nil, fmt.Errorf("CLI 超时需在 1 秒到 5 分钟之间")
	}
	if config.MaxOutputBytes == 0 {
		config.MaxOutputBytes = 1 << 20
	}
	if config.MaxOutputBytes < 1024 || config.MaxOutputBytes > 4<<20 {
		return nil, fmt.Errorf("CLI 输出上限需在 1 KiB 到 4 MiB 之间")
	}
	if config.MaxFrameStreamBytes == 0 {
		config.MaxFrameStreamBytes = 16 << 20
	}
	if config.MaxFrameStreamBytes < 1024 || config.MaxFrameStreamBytes > 32<<20 {
		return nil, fmt.Errorf("画面执行流上限需在1KiB到32MiB之间")
	}
	return &Planner{config: config, inspect: inspectInstallation, environment: childEnvironment}, nil
}

func knownProvider(provider Provider) bool { return provider == Codex || provider == Claude }
