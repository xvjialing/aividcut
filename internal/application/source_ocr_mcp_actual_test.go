package application

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPSourceOCRActualVisionReturnsOnlyRawTextAndFrameEvidence(t *testing.T) {
	if os.Getenv("AIVIDCUT_OCR_WORKER") == "" || os.Getenv("AIVIDCUT_OCR_FIXTURE_IMAGE") == "" {
		t.Skip("实际MCP OCR需显式本机helper及自有图片")
	}
	tools := preciseApplicationTools(t)
	s, err := New(tools, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	image := os.Getenv("AIVIDCUT_OCR_FIXTURE_IMAGE")
	video := filepath.Join(t.TempDir(), "agent-ocr-counter.mp4")
	preciseApplicationCommand(t, tools, "-v", "error", "-framerate", "1", "-i", filepath.Join(filepath.Dir(image), "计数-%d.png"), "-t", "4", "-r", "30", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", video)
	before, err := s.ImportPaths(context.Background(), []string{image, video})
	if err != nil {
		t.Fatal(err)
	}
	client := ocrMCPClient(t, s, true)
	for _, asset := range before.Project.Assets {
		position := int64(0)
		if asset.Kind == "video" {
			position = 2000000
		}
		response, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "aividcut_read_source_ocr", Arguments: map[string]any{"assetId": asset.ID, "positionUs": position}})
		if err != nil || response.IsError || len(response.Content) != 1 {
			t.Fatal("实际OCR工具失败或返回额外内容", err)
		}
		text, ok := response.Content[0].(*mcp.TextContent)
		if !ok || !strings.Contains(text.Text, "本地文字识别") || !strings.Contains(text.Text, "SOURCE FRAME 120") {
			t.Fatal("未返回真实中英文OCR")
		}
		if strings.Contains(text.Text, asset.Path) || strings.Contains(text.Text, `"url":`) || strings.Contains(text.Text, "image/jpeg") {
			t.Fatal("OCR返回路径或图像")
		}
		after := s.GetBootstrap()
		if projectHash(after.Project) != projectHash(before.Project) || len(s.mcpScope.frameEvidence) > 2 {
			t.Fatal("只读OCR改工程或丢证据")
		}
		if asset.Kind == "video" && !strings.Contains(text.Text, "SOURCE SECOND 2") {
			t.Fatal("实际视频取样位置不符")
		}
		t.Logf("实际MCP源位置%dus仅返回文本：%s", position, text.Text)
	}
	if len(s.mcpScope.frameEvidence) != 2 {
		t.Fatal("图片与视频证据未保留")
	}
}
