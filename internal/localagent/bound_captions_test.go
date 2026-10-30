package localagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"aividcut/internal/editor"
)

func TestSelectedSourceIncludesItsBoundCaptionChanges(t *testing.T) {
	project := editor.NewProject("源字幕选区")
	asset := editor.Asset{ID: "source", Name: "原素材", Kind: "video", Path: "/fixture/source.mp4", DurationUs: 5_000_000, HasAudio: true, Fingerprint: "sha256:" + strings.Repeat("a", 64)}
	project.Assets = append(project.Assets, asset)
	source := editor.DefaultClip("source-clip", "track-video-1", "video")
	source.AssetID = asset.ID
	source.DurationFrames = 90
	project.Clips = append(project.Clips, source)
	engine, err := editor.NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	transcript := editor.Transcript{ID: "source-transcript", AssetID: asset.ID, SourceFingerprint: asset.Fingerprint, RawWords: []editor.SourceWord{{ID: "word-1", StartUs: 200_000, EndUs: 900_000, Text: "原话"}}}
	project, err = engine.Apply(editor.EditRequest{BaseRevision: 0, IdempotencyKey: "accept-and-bind", Commands: []editor.Command{{Type: "accept_transcript", Transcript: &transcript}, {Type: "generate_bound_captions", TranscriptID: transcript.ID, TrackID: "track-caption-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	var captionID string
	for _, clip := range project.Clips {
		if clip.CaptionSource != nil {
			captionID = clip.ID
		}
	}
	if captionID == "" {
		t.Fatal("测试没有产生绑定字幕")
	}
	selected, err := selectionIDs(project, []string{source.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !selected[captionID] {
		t.Fatal("源选区遗漏核心自动派生的字幕")
	}
	source.StartFrame = 15
	request := editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: "move-source", Commands: []editor.Command{{Type: "update_clip", Clip: &source}}}
	if err := ValidatePlanForInput(PlanInput{Project: project, Selection: []string{source.ID}}, request); err != nil {
		t.Fatal("合法源移动因关联字幕被误判越界", err)
	}
}

func TestLoginMetadataCancellationCannotReturnSuccessfulInstructions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	planner, err := NewPlanner(Config{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	planner.inspect = func(context.Context, Provider, string, string) Installation {
		cancel()
		return Installation{Installed: true, Path: "/fixture/cli"}
	}
	if _, err := planner.LoginInstructions(ctx, Codex); !errors.Is(err, context.Canceled) {
		t.Fatal("检测期间取消后返回了登录说明", err)
	}
	planner.inspect = func(context.Context, Provider, string, string) Installation {
		t.Error("已取消时仍检测CLI")
		return Installation{}
	}
	if _, err := planner.LoginInstructions(ctx, Codex); !errors.Is(err, context.Canceled) {
		t.Fatal("检测前取消未传播", err)
	}
}

func TestCoreTranscriptFieldsDoNotImplicitlyExpandAgentAuthority(t *testing.T) {
	project := editor.NewProject("转写权限")
	request := editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: "hidden-transcript", Commands: []editor.Command{{Type: "update_project", Name: "已有编辑", Transcript: &editor.Transcript{ID: "not-an-agent-import"}}}}
	if err := ValidatePlan(project, request); err == nil {
		t.Fatal("核心新增转写字段后模型被隐式授予未开放的写入参数")
	}
	data, err := planSchema(project.Revision, "schema-key")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	props := schema["properties"].(map[string]any)["commands"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if props["transcript"].(map[string]any)["type"] != "null" {
		t.Fatal("输出schema仍要求填充不允许的原始转写")
	}
}
