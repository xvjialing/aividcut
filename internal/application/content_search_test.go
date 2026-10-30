package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"aividcut/internal/editor"
)

func acceptedContentFixture(t *testing.T) *Service {
	t.Helper()
	s, a, r := sourceTranscriptFixture(t)
	p := s.GetBootstrap().Project
	if _, err := s.acceptSpeechResult(context.Background(), "speech-result", p.ID, a.ID, a.Fingerprint, a.DurationUs, r, AcceptTranscriptOptions{}); err != nil {
		t.Fatal(err)
	}
	return s
}
func contentQuery(s *Service, text string) SourceContentQuery {
	bootstrap := s.GetBootstrap()
	p := bootstrap.Project
	return SourceContentQuery{SourceEpoch: bootstrap.SourceEpoch, ProjectID: p.ID, ProjectRevision: p.Revision, Text: text, Limit: 40}
}
func TestSourceContentCacheCorrectionUndoAndSameIDEngine(t *testing.T) {
	s := acceptedContentFixture(t)
	ctx := context.Background()
	q := contentQuery(s, "话语")
	r, err := s.SearchSourceContent(ctx, q)
	if err != nil || r.Total != 1 {
		t.Fatalf("未检索接受版本: %+v %v", r, err)
	}
	cache := s.contentCache
	before := s.GetBootstrap()
	if _, err = s.SearchSourceContent(ctx, q); err != nil || s.contentCache != cache {
		t.Fatal("相同版本未复用索引")
	}
	after := s.GetBootstrap()
	if before.Dirty != after.Dirty || before.Project.Revision != after.Project.Revision || before.CanUndo != after.CanUndo {
		t.Fatal("只读检索更改工程")
	}
	transcript := after.Project.Transcripts[0]
	words := append([]editor.SourceWord{}, transcript.AcceptedWords...)
	words[0].Text = "新的内容"
	corrected, err := s.CorrectSourceTranscript(ctx, CorrectTranscriptInput{ProjectID: q.ProjectID, TranscriptID: transcript.ID, TranscriptRevision: transcript.Revision, Words: words})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SearchSourceContent(ctx, q); err == nil {
		t.Fatal("旧版本检索仍可读取")
	}
	current := contentQuery(s, "新的内容")
	r, err = s.SearchSourceContent(ctx, current)
	if err != nil || r.Total != 1 || s.contentCache == cache || r.Hits[0].TranscriptRevision != corrected.Project.Transcripts[0].Revision {
		t.Fatal("纠错未重建证据")
	}
	if _, err = s.Undo(); err != nil {
		t.Fatal(err)
	}
	r, err = s.SearchSourceContent(ctx, contentQuery(s, "话语"))
	if err != nil || r.Total != 1 {
		t.Fatal("Undo未恢复索引")
	}
	// 同项目ID/revision换工程实例也不可沿用旧索引。
	p := s.GetBootstrap().Project
	p.Transcripts[0].AcceptedWords[0].Text = "另一实例"
	replacement, err := editor.NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.engine = replacement
	s.mu.Unlock()
	r, err = s.SearchSourceContent(ctx, contentQuery(s, "另一实例"))
	if err != nil || r.Total != 1 {
		t.Fatal("同ID重开复用了旧索引")
	}
	s.Close()
	if _, err = s.SearchSourceContent(ctx, SourceContentQuery{Text: "内容"}); err == nil {
		t.Fatal("关闭后仍可检索")
	}
}
func TestContentSourceEpochRejectsSameIDReopenedRead(t *testing.T) {
	s := acceptedContentFixture(t)
	q := contentQuery(s, "话语")
	if _, err := s.SearchSourceContent(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	s.resetThumbnails(false)
	if _, err := s.SearchSourceContent(context.Background(), q); err == nil {
		t.Fatal("同ID旧实例查询未拒绝")
	}
	if r, err := s.SearchSourceContent(context.Background(), contentQuery(s, "话语")); err != nil || r.Total != 1 {
		t.Fatal("当前实例无法重新检索")
	}
}

func TestMCPContentSearchReadOnlyEvidenceAndRevokedScope(t *testing.T) {
	s := acceptedContentFixture(t)
	session := mcpApplicationClient(t, s, false)
	p := s.GetBootstrap().Project
	result := callApplicationMCP(t, session, "aividcut_search_content", map[string]any{"text": "话语", "projectRevision": p.Revision}, false)
	if result["total"] != float64(1) {
		t.Fatalf("只读MCP未找到证据: %+v", result)
	}
	raw, _ := json.Marshal(result)
	for _, forbidden := range []string{"registered-source.bin", "http://", "/private/", "apiKey"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("内容检索泄露本地地址或凭据")
		}
	}
	callApplicationMCP(t, session, "aividcut_search_content", map[string]any{"text": "话语", "projectRevision": p.Revision - 1}, true)
	s.StopMCP()
	callApplicationMCP(t, session, "aividcut_search_content", map[string]any{"text": "话语", "projectRevision": p.Revision}, true)
}
