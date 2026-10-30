package localagent

import (
	"encoding/json"
	"strings"
)

const claudeExecutionVersion = "2.1.308"

func buildClaudeExecutionCommand(executable, directory, profile string, config ExecutionMCP, prompt []byte) commandSpec {
	tools := make([]string, len(config.Tools))
	for i, name := range config.Tools {
		tools[i] = "mcp__" + executionServerName + "__" + name
	}
	mcpConfig, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{executionServerName: map[string]any{"type": "http", "url": config.URL, "headers": map[string]string{"Authorization": "Bearer ${" + executionTokenVariable + "}"}}}})
	args := []string{"--print", "--verbose", "--output-format", "stream-json", "--input-format", "text", "--restricted", "--tools", "", "--setting-sources", "", "--strict-mcp-config", "--mcp-config", string(mcpConfig), "--disable-slash-commands", "--no-session-persistence", "--permission-mode", "dontAsk", "--permission-prompts", "none", "--allowedTools"}
	args = append(args, tools...)
	return commandSpec{executable: executable, args: args, directory: directory, environment: claudeExecutionEnvironment(childEnvironment(Claude, profile), config.Token), input: prompt}
}
func claudeExecutionEnvironment(base []string, token string) []string {
	filtered := make([]string, 0, len(base))
	for _, v := range base {
		key, _, _ := strings.Cut(v, "=")
		if strings.EqualFold(key, "ENABLE_TOOL_SEARCH") {
			continue
		}
		filtered = append(filtered, v)
	}
	return append(executionEnvironment(filtered, token), "ENABLE_TOOL_SEARCH=false")
}
