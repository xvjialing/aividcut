package mcpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"image"
	"image/png"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func imageFixture(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 10, 10))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestImageContentRoundTripsAsImageInCurrentAndLegacySDK(t *testing.T) {
	data := imageFixture(t)
	for _, legacy := range []bool{false, true} {
		server, err := Start(Options{ProjectID: "image-fixture", Tools: []Tool{{Name: "fixture_frame", Description: "读取有界图像", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`), Handler: func(context.Context, json.RawMessage) (any, error) {
			value := ImageResult{Metadata: map[string]any{"assetId": "registered", "positionUs": 123}, Images: []Image{{MIME: "image/png", Data: data}}}
			if legacy {
				return &value, nil
			}
			return value, nil
		}}}})
		if err != nil {
			t.Fatal(err)
		}
		session, _ := connectFixtureClient(t, server, legacy)
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "fixture_frame", Arguments: map[string]any{}})
		if err != nil || result.IsError || len(result.Content) != 2 {
			t.Fatalf("未获得真正图片content: %+v %v", result, err)
		}
		picture, ok := result.Content[1].(*mcp.ImageContent)
		if !ok || picture.MIMEType != "image/png" || sha256.Sum256(picture.Data) != sha256.Sum256(data) {
			t.Fatal("图片被转成文本或双重base64")
		}
		text := result.Content[0].(*mcp.TextContent).Text
		if bytes.Contains([]byte(text), []byte("base64")) {
			t.Fatal("大图数据进入普通文本")
		}
		session.Close()
		server.Close()
	}
}
func TestImageResultRejectsTypesCorruptionAndBudgets(t *testing.T) {
	data := imageFixture(t)
	for _, value := range []ImageResult{
		{Metadata: map[string]any{}, Images: []Image{}},
		{Metadata: map[string]any{}, Images: []Image{{MIME: "image/svg+xml", Data: data}}},
		{Metadata: map[string]any{}, Images: []Image{{MIME: "image/jpeg", Data: data}}},
		{Metadata: map[string]any{}, Images: []Image{{MIME: "image/png", Data: []byte("bad")}}},
		{Metadata: []string{"bad"}, Images: []Image{{MIME: "image/png", Data: data}}},
	} {
		if !encodeImageResult(value, 2<<20).IsError {
			t.Fatal("非法图片结果被接受")
		}
	}
	oversized := ImageResult{Metadata: map[string]any{"info": string(bytes.Repeat([]byte("x"), 2000))}, Images: []Image{{MIME: "image/png", Data: data}}}
	if !encodeImageResult(oversized, 4096).IsError {
		t.Fatal("图片绕过输出预算")
	}
}

func TestImageResultCannotFallBackToOrdinaryJSON(t *testing.T) {
	value := ImageResult{Metadata: map[string]any{}, Images: []Image{{MIME: "image/png", Data: imageFixture(t)}}}
	for _, input := range []any{value, &value, map[string]any{"nested": value}} {
		if _, err := json.Marshal(input); err == nil {
			t.Fatal("图片原始结果可误入普通JSON")
		}
	}
}
