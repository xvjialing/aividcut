package localagent

import (
	"aividcut/internal/managedprocess"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type capture struct {
	mu        sync.Mutex
	maximum   int
	lineLimit int
	total     int
	stdout    []byte
	stderr    []byte
	pending   []byte
	err       error
	line      func([]byte) error
	cancel    context.CancelFunc
}

type captureWriter struct {
	target *capture
	stderr bool
}

func (w captureWriter) Write(data []byte) (int, error) {
	c := w.target
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return 0, c.err
	}
	if len(data) > c.maximum-c.total {
		c.err = ErrOutputLimit
		c.cancel()
		return 0, c.err
	}
	c.total += len(data)
	if w.stderr {
		c.stderr = append(c.stderr, data...)
		if len(c.stderr) > 16<<10 {
			c.stderr = append([]byte(nil), c.stderr[len(c.stderr)-(16<<10):]...)
		}
		return len(data), nil
	}
	if c.line == nil {
		c.stdout = append(c.stdout, data...)
		return len(data), nil
	}
	c.pending = append(c.pending, data...)
	for {
		index := bytes.IndexByte(c.pending, '\n')
		if index < 0 {
			if c.lineLimit > 0 && len(c.pending) > c.lineLimit {
				c.err = ErrOutputLimit
				c.cancel()
				return 0, c.err
			}
			break
		}
		if c.lineLimit > 0 && index > c.lineLimit {
			c.err = ErrOutputLimit
			c.cancel()
			return 0, c.err
		}
		line := bytes.TrimSpace(c.pending[:index])
		if len(line) > 0 {
			if err := c.line(line); err != nil {
				c.err = err
				c.cancel()
				return 0, err
			}
		}
		c.pending = c.pending[index+1:]
		if len(c.pending) == 0 {
			c.pending = nil
		}
	}
	return len(data), nil
}

func run(ctx context.Context, spec commandSpec, maximum int, line func([]byte) error) ([]byte, []byte, error) {
	return runWithLineLimit(ctx, spec, maximum, maximum, line)
}
func runWithLineLimit(ctx context.Context, spec commandSpec, maximum, lineLimit int, line func([]byte) error) ([]byte, []byte, error) {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	command := managedprocess.CommandContext(child, spec.executable, spec.args...)
	command.Dir, command.Env = spec.directory, spec.environment
	command.Stdin = bytes.NewReader(spec.input)
	command.WaitDelay = 2 * time.Second
	data := &capture{maximum: maximum, lineLimit: lineLimit, line: line, cancel: cancel}
	command.Stdout = captureWriter{target: data}
	command.Stderr = captureWriter{target: data, stderr: true}
	err := command.Run()
	data.mu.Lock()
	defer data.mu.Unlock()
	if data.err == nil && line != nil && len(bytes.TrimSpace(data.pending)) > 0 {
		data.err = line(bytes.TrimSpace(data.pending))
	}
	if data.err != nil {
		return data.stdout, data.stderr, data.err
	}
	if ctx.Err() != nil {
		return data.stdout, data.stderr, ctx.Err()
	}
	return data.stdout, data.stderr, err
}

// 只传 CLI 正常认证、路径、区域和网络所需环境；不透传注入代码或任意MCP/IDE配置。
func childEnvironment(provider Provider, profile string) []string {
	allowed := map[string]bool{}
	for _, key := range []string{"PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "LC_CTYPE", "TMPDIR", "TMP", "TEMP", "SYSTEMROOT", "WINDIR", "PATHEXT", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS", "OPENAI_API_KEY", "CODEX_API_KEY", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "OPENAI_BASE_URL", "ANTHROPIC_BASE_URL"} {
		allowed[key] = true
	}
	var environment []string
	if provider == Claude {
		allowed["CLAUDE_CODE_SAFE_MODE"] = true
	}
	for _, value := range os.Environ() {
		key, _, ok := strings.Cut(value, "=")
		if profile == "" && ((provider == Codex && key == "CODEX_HOME") || (provider == Claude && key == "CLAUDE_CONFIG_DIR")) {
			environment = append(environment, value)
			continue
		}
		if ok && allowed[strings.ToUpper(key)] {
			environment = append(environment, value)
		}
	}
	if profile != "" {
		if provider == Codex {
			environment = append(environment, "CODEX_HOME="+profile)
		} else {
			environment = append(environment, "CLAUDE_CONFIG_DIR="+profile)
		}
	}
	return environment
}

func metadata(ctx context.Context, provider Provider, executable, directory, profile string, args ...string) ([]byte, []byte, error) {
	limited, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return run(limited, commandSpec{executable: executable, args: args, directory: directory, environment: childEnvironment(provider, profile)}, 128<<10, nil)
}

func exitFailed(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit)
}

func noExtraJSON(decoder interface{ Decode(any) error }) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrProtocol
	}
	return nil
}
