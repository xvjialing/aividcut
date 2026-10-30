package localagent

import (
	"aividcut/internal/editor"
	"encoding/json"
	"testing"
)

func TestBatchPlansPreserveSelectionAndNewObjectTimeScope(t *testing.T) {
	p := editor.NewProject("批量计划范围")
	a := editor.DefaultClip("a", "track-text-1", "text")
	a.Text = "甲"
	a.DurationFrames = 30
	b := editor.DefaultClip("b", "track-text-1", "text")
	b.Text = "乙"
	b.StartFrame = 60
	b.DurationFrames = 30
	p.Clips = []editor.Clip{a, b}
	p.Tracks = append(p.Tracks, editor.Track{ID: "text-two", Name: "文字2", Kind: "text"})
	input := PlanInput{Project: p, Selection: []string{"a"}}
	cases := []struct {
		name    string
		command editor.Command
		ok      bool
	}{
		{"trim", editor.Command{Type: "trim_clips", ClipIDs: []string{"a"}, Edge: "end", FrameDelta: -1}, true},
		{"outside-trim", editor.Command{Type: "trim_clips", ClipIDs: []string{"b"}, Edge: "start", FrameDelta: 1}, false},
		{"split", editor.Command{Type: "split_clips", ClipIDs: []string{"a"}, AtFrame: 15}, true},
		{"move", editor.Command{Type: "move_clips", ClipIDs: []string{"a"}, FrameDelta: 5}, true},
		{"delete", editor.Command{Type: "delete_clips", ClipIDs: []string{"a"}}, true},
		{"copy-in-window", editor.Command{Type: "duplicate_clips", ClipIDs: []string{"a"}, AtFrame: 0, TrackDelta: 3}, true},
		{"copy-out-window", editor.Command{Type: "duplicate_clips", ClipIDs: []string{"a"}, AtFrame: 100, TrackDelta: 3}, false},
		{"outside-split", editor.Command{Type: "split_clips", ClipIDs: []string{"b"}, AtFrame: 75}, false},
		{"outside-delete", editor.Command{Type: "delete_clips", ClipIDs: []string{"a", "b"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: tc.name, Commands: []editor.Command{tc.command}}
			if err := ValidatePlanForInput(input, req); (err == nil) != tc.ok {
				t.Fatalf("规划范围结果错误：%v", err)
			}
		})
	}
	full := editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "full-copy", Commands: []editor.Command{{Type: "duplicate_clips", ClipIDs: []string{"a"}, AtFrame: 100}}}
	if err := ValidatePlan(p, full); err != nil {
		t.Fatal("全工程明确复制不能被选区时间窗误拒绝", err)
	}
}

func TestBatchPlanSchemaAdvertisesCommandsWithoutInternalParents(t *testing.T) {
	data, err := planSchema(0, "test")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err = json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	props := schema["properties"].(map[string]any)["commands"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if _, ok := props["sourceCopies"]; ok {
		t.Fatal("内部字幕父源映射泄漏为规划字段")
	}
	for _, key := range []string{"inheritance", "clipboard", "pasteNamespace"} {
		if _, ok := props[key]; ok {
			t.Fatal("内部快照字段泄漏", key)
		}
	}
	if values := props["clipboardId"].(map[string]any)["enum"].([]any); len(values) != 1 || values[0] != "" {
		t.Fatal("快照计划可以伪造剪贴板")
	}
	if props["clipIds"].(map[string]any)["maxItems"].(float64) != 1000 {
		t.Fatal("缺少批量规划限制")
	}
	values := props["type"].(map[string]any)["enum"].([]any)
	for _, kind := range []string{"move_clips", "delete_clips", "split_clips", "duplicate_clips", "trim_clips"} {
		found := false
		for _, v := range values {
			if v == kind {
				found = true
			}
		}
		if !found {
			t.Fatal("Schema未声明", kind)
		}
	}
}

func TestRipplePlanRequiresProjectScope(t *testing.T) {
	p := editor.NewProject("全轨计划")
	a := editor.DefaultClip("a", "track-text-1", "text")
	a.Text = "甲"
	a.DurationFrames = 30
	p.Clips = []editor.Clip{a}
	request := editor.EditRequest{BaseRevision: 0, IdempotencyKey: "ripple", Commands: []editor.Command{{Type: "ripple_delete_range", FromFrame: 5, ToFrame: 10}}}
	if err := ValidatePlanForInput(PlanInput{Project: p}, request); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlanForInput(PlanInput{Project: p, Selection: []string{"a"}}, request); err == nil {
		t.Fatal("选区规划扩大为全工程")
	}
}
