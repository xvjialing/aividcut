// Package generationjobs 管理付费生成的持久状态；恢复只处理已有请求，不自动重交。
package generationjobs

import (
	"context"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/generation"
)

const (
	Queued     = "queued"
	Submitting = "submitting"
	Running    = "running"
	Waiting    = "waiting"
	Importing  = "importing"
	Succeeded  = "succeeded"
	Failed     = "failed"
	Unknown    = "unknown"
	Cancelled  = "cancelled"
)

type Job struct {
	ID               string                     `json:"id"`
	ConnectionID     string                     `json:"connectionId"`
	ProjectID        string                     `json:"projectId"`
	ProjectRevision  int64                      `json:"projectRevision"`
	Request          generation.Request         `json:"request"`
	Status           string                     `json:"status"`
	Message          string                     `json:"message"`
	RemoteID         string                     `json:"remoteId,omitempty"`
	CreatedAt        time.Time                  `json:"createdAt"`
	UpdatedAt        time.Time                  `json:"updatedAt"`
	CanResume        bool                       `json:"canResume"`
	Acknowledged     bool                       `json:"acknowledged,omitempty"`
	Estimate         *CostEstimate              `json:"estimate,omitempty"`
	Assets           []generation.ImportedAsset `json:"assets"`
	Imported         bool                       `json:"imported"`
	ImportedAssetIDs []string                   `json:"importedAssetIds,omitempty"`
}

type SubmitInput struct {
	ConnectionID string             `json:"connectionId"`
	Request      generation.Request `json:"request"`
	Consent      bool               `json:"consent"`
	Estimate     *EstimateInput     `json:"estimate,omitempty"`
	Context      *ProjectContext    `json:"context,omitempty"`
}

// 可选的人工核对上下文；应用层在取工程快照时校验，旧MCP另有scope校验。
type ProjectContext struct {
	ProjectID   string  `json:"projectId"`
	Revision    int64   `json:"revision"`
	SourceEpoch *uint64 `json:"sourceEpoch,omitempty"`
}

// Binding 中的密钥仅由连接层惰性读取，不进入任何持久记录。
type Binding struct {
	ProviderID string
	ModelID    string
	Secrets    generation.SecretSource
	Release    func()
}

type Resolver func(context.Context, string) (Binding, error)
type Factory func(string, string, generation.SecretSource, generation.ReferenceSource) (generation.Provider, error)

type Options struct {
	Directory    string
	Resolve      Resolver
	Factory      Factory
	Importer     *generation.ResultImporter
	PollInterval time.Duration
	TaskTimeout  time.Duration
}

type record struct {
	Version   int             `json:"version"`
	Job       Job             `json:"job"`
	Signature string          `json:"signature"`
	Task      generation.Task `json:"task"`
}

type projectRequest struct {
	Project editor.Project
	Input   SubmitInput
}
