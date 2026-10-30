package localagent

// ExecutionMCP 只能由App的内部执行队列提供，不接受模型/普通项目文件指定连接。
type ExecutionMCP struct {
	URL   string   `json:"-"`
	Token string   `json:"-"`
	Tools []string `json:"tools"`
}

type ExecutionInput struct {
	Provider          Provider     `json:"provider"`
	ProjectID         string       `json:"projectId"`
	ProjectRevision   int64        `json:"projectRevision"`
	Instruction       string       `json:"instruction"`
	WorkflowID        string       `json:"workflowId,omitempty"`
	SkillInstructions string       `json:"skillInstructions,omitempty"`
	Selection         []string     `json:"selection"`
	MCP               ExecutionMCP `json:"-"`
}

type ExecutionResult struct {
	Provider Provider `json:"provider"`
	Summary  string   `json:"summary"`
	Warnings []string `json:"warnings"`
}
