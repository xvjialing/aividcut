// Package workflows 把可复用剪辑参数编译为统一编辑命令；不调用生成模型或修改工程文件。
package workflows

import (
	"time"

	"aividcut/internal/editor"
)

type Option struct {
	Value string `json:"value"`
	Name  string `json:"name"`
}

type Parameter struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Required    bool     `json:"required"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Step        *float64 `json:"step,omitempty"`
	Options     []Option `json:"options,omitempty"`
	Default     any      `json:"default,omitempty"`
}

type Definition struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Version     string         `json:"version"`
	Builtin     bool           `json:"builtin"`
	Available   bool           `json:"available"`
	Reason      string         `json:"reason,omitempty"`
	Parameters  []Parameter    `json:"parameters"`
	Defaults    map[string]any `json:"defaults"`
}

type Input struct {
	Scope      string         `json:"scope,omitempty"`
	WorkflowID string         `json:"workflowId"`
	Selection  []string       `json:"selection"`
	Parameters map[string]any `json:"parameters"`
}

type Effect struct {
	Kind        string `json:"kind"`
	ClipID      string `json:"clipId,omitempty"`
	TrackID     string `json:"trackId,omitempty"`
	StartFrame  int64  `json:"startFrame"`
	EndFrame    int64  `json:"endFrame"`
	Description string `json:"description"`
}

type Evidence struct {
	Kind              string   `json:"kind"`
	ClipID            string   `json:"clipId,omitempty"`
	AssetID           string   `json:"assetId,omitempty"`
	TranscriptID      string   `json:"transcriptId,omitempty"`
	SourceFingerprint string   `json:"sourceFingerprint,omitempty"`
	WordIDs           []string `json:"wordIds,omitempty"`
	SourceStartUs     int64    `json:"sourceStartUs"`
	SourceEndUs       int64    `json:"sourceEndUs"`
	StartFrame        int64    `json:"startFrame"`
	EndFrame          int64    `json:"endFrame"`
	Description       string   `json:"description"`
}

type Plan struct {
	AudioFadeSummary    *AudioFadeSummary    `json:"audioFadeSummary,omitempty"`
	SpeechRippleSummary *SpeechRippleSummary `json:"speechRippleSummary,omitempty"`
	RangeSummary        *RippleSummary       `json:"rangeSummary,omitempty"`
	ID                  string               `json:"id"`
	WorkflowID          string               `json:"workflowId"`
	WorkflowVersion     string               `json:"workflowVersion"`
	ProjectID           string               `json:"projectId"`
	Request             editor.EditRequest   `json:"request"`
	Summary             string               `json:"summary"`
	Effects             []Effect             `json:"effects"`
	Evidence            []Evidence           `json:"evidence"`
	Warnings            []string             `json:"warnings"`
	AffectedClipIDs     []string             `json:"affectedClipIds"`
	SavedFrames         int64                `json:"savedFrames"`
}

type Preset struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	WorkflowID      string         `json:"workflowId"`
	WorkflowVersion string         `json:"workflowVersion"`
	Version         int64          `json:"version"`
	Parameters      map[string]any `json:"parameters"`
	CreatedAt       time.Time      `json:"createdAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
}

type PresetInput struct {
	ID          string         `json:"id,omitempty"`
	Name        string         `json:"name"`
	WorkflowID  string         `json:"workflowId"`
	Parameters  map[string]any `json:"parameters"`
	BaseVersion int64          `json:"baseVersion,omitempty"`
}
