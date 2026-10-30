package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/mcpserver"
	"context"
	"encoding/json"
)

func clipboardMCPResult(v ClipboardResult) any {
	return map[string]any{"projectId": v.Bootstrap.Project.ID, "revision": v.Bootstrap.Project.Revision, "clipboard": v.Clipboard, "selectedClipIds": v.SelectedClipIDs}
}
func (s *Service) clipboardMCPTools(scope *mcpProjectScope) []mcpserver.Tool {
	tools := []mcpserver.Tool{{Name: "aividcut_get_clipboard", Description: "只读当前授权工程的剪贴板摘要，不返回文本、路径或其它工程内容。", InputSchema: mcpSchema(map[string]any{}), ReadOnly: true, IdempotentHint: true, Handler: func(ctx context.Context, data json.RawMessage) (any, error) {
		if _, err := decodeMCP[struct{}](data); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := scope.checkLocked(s); err != nil {
			return nil, err
		}
		id, _ := s.engine.Identity()
		return s.clipboardStatusLocked(id), nil
	}}}
	if !scope.allowEdits {
		return tools
	}
	selection := map[string]any{"baseRevision": map[string]any{"type": "integer", "minimum": 0}, "clipIds": map[string]any{"type": "array", "items": mcpString(), "minItems": 1, "maxItems": 1000, "uniqueItems": true}, "includeLinked": map[string]any{"type": "boolean"}}
	for _, cut := range []bool{false, true} {
		name, description := "aividcut_copy_clips", "将当前选择复制为工程内快照；不修改工程，包含关联字幕及手改。单独字幕粘贴为独立字幕。"
		props := map[string]any{}
		for k, v := range selection {
			props[k] = v
		}
		required := []string{"baseRevision", "clipIds"}
		if cut {
			name, description = "aividcut_cut_clips", "保存工程内快照并原子删除选中片段；失败保留原剪贴板，一次撤销。"
			props["idempotencyKey"] = mcpString()
			required = append(required, "idempotencyKey")
		}
		isCut := cut
		toolName := name
		tools = append(tools, mcpserver.Tool{Name: name, Description: description, InputSchema: mcpSchema(props, required...), IdempotentHint: cut, Handler: func(ctx context.Context, data json.RawMessage) (any, error) {
			input, err := decodeMCP[struct {
				BaseRevision   int64    `json:"baseRevision"`
				ClipIDs        []string `json:"clipIds"`
				IncludeLinked  *bool    `json:"includeLinked,omitempty"`
				IdempotencyKey string   `json:"idempotencyKey,omitempty"`
			}](data)
			if err != nil {
				return nil, err
			}
			if len(input.ClipIDs) > 1000 {
				return nil, &mcpserver.ToolError{Code: "selection_too_large", Message: "MCP剪贴板每次最多1000个显式片段"}
			}
			return s.mcpProjectCall(ctx, scope, toolName, true, func(p editor.Project) (any, error) {
				selection := ClipboardSelectionInput{ProjectID: p.ID, SourceEpoch: s.clipboardEpochLocked(), BaseRevision: input.BaseRevision, ClipIDs: input.ClipIDs, IncludeLinked: input.IncludeLinked}
				var result ClipboardResult
				if isCut {
					result, err = s.cutClipboardLocked(p, ClipboardCutInput{ClipboardSelectionInput: selection, IdempotencyKey: input.IdempotencyKey})
				} else {
					result, err = s.copyClipboardLocked(p, selection)
				}
				if err != nil {
					return nil, err
				}
				return clipboardMCPResult(result), nil
			})
		}})
	}
	tools = append(tools, mcpserver.Tool{Name: "aividcut_paste_clips", Description: "按明确clipboardId将工程快照粘贴到atFrame，保持间距及源采样；自动字幕跟随当前源转写，手改保留。需要最新baseRevision及幂等标识；整体失败/一次撤销。", InputSchema: mcpSchema(map[string]any{"clipboardId": mcpString(), "baseRevision": map[string]any{"type": "integer", "minimum": 0}, "idempotencyKey": mcpString(), "atFrame": map[string]any{"type": "integer", "minimum": 0}, "trackDelta": map[string]any{"type": "integer"}}, "clipboardId", "baseRevision", "idempotencyKey", "atFrame"), IdempotentHint: true, Handler: func(ctx context.Context, data json.RawMessage) (any, error) {
		input, err := decodeMCP[struct {
			ClipboardID    string `json:"clipboardId"`
			BaseRevision   int64  `json:"baseRevision"`
			IdempotencyKey string `json:"idempotencyKey"`
			AtFrame        int64  `json:"atFrame"`
			TrackDelta     int    `json:"trackDelta,omitempty"`
		}](data)
		if err != nil {
			return nil, err
		}
		return s.mcpProjectCall(ctx, scope, "aividcut_paste_clips", true, func(p editor.Project) (any, error) {
			result, err := s.pasteClipboardLocked(ClipboardPasteInput{ProjectID: p.ID, SourceEpoch: s.clipboardEpochLocked(), BaseRevision: input.BaseRevision, ClipboardID: input.ClipboardID, IdempotencyKey: input.IdempotencyKey, AtFrame: input.AtFrame, TrackDelta: input.TrackDelta})
			if err != nil {
				return nil, err
			}
			return clipboardMCPResult(result), nil
		})
	}})
	return tools
}
