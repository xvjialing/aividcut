package application

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceOCRActualVisionImageBlankAndVideoPosition(t *testing.T) {
	if os.Getenv("AIVIDCUT_OCR_WORKER") == "" || os.Getenv("AIVIDCUT_OCR_FIXTURE_IMAGE") == "" {
		t.Skip("实际Vision验收需要显式工作进程和自有图像")
	}
	tools := preciseApplicationTools(t)
	s, err := New(tools, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	image := os.Getenv("AIVIDCUT_OCR_FIXTURE_IMAGE")
	blank := filepath.Join(filepath.Dir(image), "无文字源画面.png")
	video := filepath.Join(t.TempDir(), "source-ocr.mp4")
	preciseApplicationCommand(t, tools, "-v", "error", "-framerate", "1", "-i", filepath.Join(filepath.Dir(image), "计数-%d.png"), "-t", "4", "-r", "30", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", video)
	b, err := s.ImportPaths(context.Background(), []string{image, blank, video})
	if err != nil {
		t.Fatal(err)
	}
	var videoDraft SourceOCRDraft
	for _, asset := range b.Project.Assets {
		position := int64(0)
		if asset.Kind == "video" {
			position = 2000000
		}
		input := SourceOCRInput{ThumbnailRequest: ThumbnailRequest{RequestID: "actual-ocr-" + asset.ID, ProjectID: b.Project.ID, SourceEpoch: b.SourceEpoch, AssetID: asset.ID, Fingerprint: asset.Fingerprint, PositionUs: position}, ProjectRevision: b.Project.Revision}
		draft, e := s.PrepareSourceOCR(context.Background(), input)
		if e != nil {
			t.Fatal(e)
		}
		texts := []string{}
		for _, line := range draft.Result.Lines {
			texts = append(texts, line.Text)
		}
		joined := strings.Join(texts, "\n")
		if asset.Path == blank {
			if len(texts) != 0 {
				t.Fatal("空白被伪造文字", joined)
			}
		} else if !strings.Contains(joined, "本地文字识别") || !strings.Contains(joined, "SOURCE FRAME 120") {
			t.Fatal("实际中英文识别不符", joined)
		}
		t.Logf("%s 源位置%dus：%q", asset.Name, position, joined)
		if asset.Kind == "video" {

			if !strings.Contains(joined, "SOURCE SECOND 2") {
				t.Fatal("OCR没有读到第2秒的实际画面计数", joined)
			}
			videoDraft = draft
		}
	}
	input := AcceptSourceOCRInput{ID: videoDraft.ID, ProjectID: videoDraft.ProjectID, ProjectRevision: videoDraft.ProjectRevision, SourceEpoch: videoDraft.SourceEpoch, Lines: []string{"人工确认的画面文字", "SOURCE FRAME 120"}}
	b, err = s.AcceptSourceOCR(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.SearchSourceContent(context.Background(), SourceContentQuery{ProjectID: b.Project.ID, ProjectRevision: b.Project.Revision, SourceEpoch: b.SourceEpoch, Text: "人工确认", Limit: 40})
	if err != nil || len(result.Hits) != 1 {
		t.Fatal("真实OCR记录未进入检索", err)
	}
	path := filepath.Join(t.TempDir(), "ocr-actual.aividcut")
	if _, err = s.SaveProjectTo(path); err != nil {
		t.Fatal(err)
	}
	b, err = s.OpenProjectPath(path)
	if err != nil || b.Project.VisualAnalyses[0].Frames[0].PositionUs != 2000000 {
		t.Fatal("实际源位置保存重开丢失", err)
	}
}
