package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aividcut/internal/editor"
	"aividcut/internal/localagent"
	"aividcut/internal/media"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func sourceFrameFixture(t *testing.T, s *Service) editor.Asset {
	t.Helper()
	p := filepath.Join(t.TempDir(), "本地画面.png")
	picture := image.NewRGBA(image.Rect(0, 0, 480, 270))
	picture.Set(5, 5, color.RGBA{R: 255, A: 255})
	var data bytes.Buffer
	png.Encode(&data, picture)
	if err := os.WriteFile(p, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	asset, err := (media.Toolchain{}).Probe(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	asset.ID = "source-image"
	project := s.GetBootstrap().Project
	project.Assets = []editor.Asset{asset}
	engine, err := editor.NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	s.engine = engine
	s.savedHash = projectHash(project)
	return asset
}
func TestSourceFrameRequiresActualFingerprintAndSameEpoch(t *testing.T) {
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	asset := sourceFrameFixture(t, s)
	bootstrap := s.GetBootstrap()
	input := SourceFrameRequest{ThumbnailRequest: ThumbnailRequest{SourceEpoch: bootstrap.SourceEpoch, RequestID: "first-frame", ProjectID: bootstrap.Project.ID, AssetID: asset.ID, Fingerprint: asset.Fingerprint}, Profile: "detail"}
	value, err := s.GetSourceFrame(context.Background(), input)
	if err != nil || !validFrameEvidence(value.Evidence) || value.Evidence.Profile != "detail" || value.Evidence.Width <= 320 || value.URL == "" {
		t.Fatalf("源证据缺失: %+v %v", value, err)
	}
	if _, err = s.GetSourceFrame(context.Background(), SourceFrameRequest{ThumbnailRequest: input.ThumbnailRequest, Profile: "unknown"}); err == nil {
		t.Fatal("未知档位被接受")
	}
	s.resetThumbnails(false)
	if _, err = s.GetSourceFrame(context.Background(), input); err == nil {
		t.Fatal("旧实例可读画面")
	}
	input.SourceEpoch = s.GetBootstrap().SourceEpoch
	if err := os.WriteFile(asset.Path, []byte("changed source"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetSourceFrame(context.Background(), input); err == nil {
		t.Fatal("源变化仍返回旧指纹画面")
	}
}
func TestMCPSourceFramesAreOptInAndReturnBoundedImages(t *testing.T) {
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	asset := sourceFrameFixture(t, s)
	noFrames := mcpApplicationClient(t, s, false)
	callApplicationMCP(t, noFrames, "aividcut_read_source_frames", map[string]any{"assetId": asset.ID, "positionsUs": []int{0}}, true)
	s.StopMCP()
	status, err := s.StartMCP(MCPStartInput{ProjectID: s.GetBootstrap().Project.ID, AllowSourceFrames: true})
	if err != nil || !status.AllowSourceFrames || status.AllowEdits {
		t.Fatal("画面权限与编辑权限混淆")
	}
	config, err := s.GetMCPConnection()
	if err != nil {
		t.Fatal(err)
	}
	input := localagent.ExecutionInput{MCP: localagent.ExecutionMCP{URL: config.URL, Token: strings.TrimPrefix(config.HTTPHeaders["Authorization"], "Bearer ")}}
	session, err := fixtureExecutionClient(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "aividcut_read_source_frames", Arguments: map[string]any{"assetId": asset.ID, "positionsUs": []int{0}, "profile": "detail"}})
	if err != nil || result.IsError || len(result.Content) != 2 {
		t.Fatalf("没有图像content: %+v %v", result, err)
	}
	picture, ok := result.Content[1].(*mcp.ImageContent)
	if !ok || picture.MIMEType != "image/jpeg" {
		t.Fatal("图片不是可见MCP内容")
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Frames []SourceFrameEvidence `json:"frames"`
	}
	json.Unmarshal(raw, &metadata)
	if len(metadata.Frames) != 1 || metadata.Frames[0].ImageSHA256 != fmt.Sprintf("%x", sha256.Sum256(picture.Data)) {
		t.Fatal("图片哈希与元数据不对应")
	}
	if strings.Contains(string(raw), asset.Path) || strings.Contains(string(raw), "url") || strings.Contains(string(raw), "base64") {
		t.Fatal("结构化结果泄露路径/URL或图片数据")
	}
	for _, args := range []map[string]any{{"assetId": asset.ID, "positionsUs": []int{1}}, {"assetId": asset.ID, "positionsUs": []int{0, 0}}, {"assetId": asset.ID, "positionsUs": []int{0}, "url": "file:///private/source.png"}} {
		callApplicationMCP(t, session, "aividcut_read_source_frames", args, true)
	}
	s.StopMCP()
	callApplicationMCP(t, session, "aividcut_read_source_frames", map[string]any{"assetId": asset.ID, "positionsUs": []int{0}}, true)
}
func TestAgentExecutionPersistsFrameMetadataWithoutImagesOrReplay(t *testing.T) {
	directory := t.TempDir()
	runner := &fixtureExecutionAgent{fixtureAgent: &fixtureAgent{}}
	s := newAgentService(t, directory, runner)
	asset := sourceFrameFixture(t, s)
	runner.execute = func(ctx context.Context, input localagent.ExecutionInput, _ func(localagent.Event)) (localagent.ExecutionResult, error) {
		if !strings.Contains(input.SkillInstructions, "证据ID") || !strings.Contains(strings.Join(input.MCP.Tools, ","), "aividcut_read_source_frames") {
			return localagent.ExecutionResult{}, fmt.Errorf("未注入画面方法或工具")
		}
		session, err := fixtureExecutionClient(ctx, input)
		if err != nil {
			return localagent.ExecutionResult{}, err
		}
		defer session.Close()
		response, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "aividcut_read_source_frames", Arguments: map[string]any{"assetId": asset.ID, "positionsUs": []int{0}}})
		if err != nil || response.IsError {
			return localagent.ExecutionResult{}, fmt.Errorf("读取画面失败")
		}
		if _, ok := response.Content[1].(*mcp.ImageContent); !ok {
			return localagent.ExecutionResult{}, fmt.Errorf("只见文字未见图片")
		}
		return localagent.ExecutionResult{Provider: input.Provider, Summary: "夹具已读源画面，需真人核对内容；没有编辑工程。"}, nil
	}
	input := executionInput(s, "read-source-image")
	input.AllowEdits = false
	input.AllowSourceFrames = true
	if _, err := s.StartAgentExecution(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	record := awaitExecution(t, s, input.ID, "succeeded")
	if len(record.FrameEvidence) != 1 || !validFrameEvidence(record.FrameEvidence[0]) || s.GetBootstrap().Dirty {
		t.Fatalf("画面元数据未记录或只读修改工程: %+v", record)
	}
	s.Close()
	restored := newAgentService(t, directory, runner)
	saved, err := restored.GetAgentExecution(input.ID)
	if err != nil || len(saved.FrameEvidence) != 1 || runner.calls.Load() != 1 {
		t.Fatal("重开未恢复证据或自动重跑")
	}
	data, err := os.ReadFile(filepath.Join(directory, "executions.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{asset.Path, "base64", "image/jpeg", "Bearer ", "http://"} {
		if strings.Contains(string(data), bad) {
			t.Fatalf("持久记录泄露图像数据/路径/配置: %s", bad)
		}
	}
}
