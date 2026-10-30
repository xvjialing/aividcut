package localagent

import (
	"aividcut/internal/managedprocess"
	"context"
	"encoding/json"
	"errors"
	"time"
)

// 仅initialize/config-read，不创建thread/turn，不触发模型推理或MCP业务工具。
// 收到有效配置后取消此私有进程，避免把它变成常驻app-server。
func (p *Planner) readExecutionConfiguration(parent context.Context, executable, directory, profile string, config ExecutionMCP, overrides []string) ([]byte, error) {
	ctx, timeout := context.WithTimeout(parent, 8*time.Second)
	defer timeout()
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	args := append([]string{"--no-daemon", "app-server"}, overrides...)
	command := managedprocess.CommandContext(child, executable, args...)
	command.Dir = directory
	command.Env = executionEnvironment(p.environment(Codex, profile), config.Token)
	command.WaitDelay = 2 * time.Second
	input, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	defer input.Close()
	var result []byte
	data := &capture{maximum: 256 << 10, cancel: cancel}
	data.line = func(line []byte) error {
		var message struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(line, &message) != nil {
			return ErrProtocol
		}
		if len(message.Error) > 0 && string(message.Error) != "null" {
			return ErrProtocol
		}
		switch message.ID {
		case 1:
			if len(message.Result) == 0 {
				return ErrProtocol
			}
			_, err := input.Write([]byte("{\"method\":\"initialized\",\"params\":{}}\n{\"id\":2,\"method\":\"config/read\",\"params\":{\"includeLayers\":false}}\n"))
			return err
		case 2:
			if len(message.Result) == 0 {
				return ErrProtocol
			}
			result = append([]byte(nil), message.Result...)
			cancel()
		default:
			if message.Method == "" {
				return ErrProtocol
			}
		}
		return nil
	}
	command.Stdout = captureWriter{target: data}
	command.Stderr = captureWriter{target: data, stderr: true}
	if err = command.Start(); err != nil {
		return nil, err
	}
	_, writeErr := input.Write([]byte("{\"id\":1,\"method\":\"initialize\",\"params\":{\"clientInfo\":{\"name\":\"aividcut_configuration_check\",\"version\":\"0.1.0\"}}}\n"))
	if writeErr != nil {
		cancel()
	}
	waitErr := command.Wait()
	data.mu.Lock()
	defer data.mu.Unlock()
	if parent.Err() != nil {
		return nil, parent.Err()
	}
	if data.err != nil {
		return nil, data.err
	}
	if len(result) > 0 {
		return result, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, errors.Join(writeErr, waitErr, ErrProtocol)
}
