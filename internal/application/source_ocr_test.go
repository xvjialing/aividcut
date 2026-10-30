package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"aividcut/internal/editor"
	"aividcut/internal/media"
	"aividcut/internal/ocr"
)

type ocrFixtureFunc func(context.Context, []byte) (ocr.Result, error)

func (f ocrFixtureFunc) Recognize(c context.Context, b []byte) (ocr.Result, error) { return f(c, b) }
func ocrFixtureResult() ocr.Result {
	return ocr.Result{Version: 1, Engine: "apple-vision", Revision: 3, Languages: []string{"zh-Hans", "en-US"}, Lines: []ocr.Line{{Text: "原始识别", Confidence: new(0.8)}}}
}
func setupOCRFixture(t *testing.T) (*Service, SourceOCRInput) {
	t.Helper()
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	asset := sourceFrameFixture(t, s)
	b := s.GetBootstrap()
	s.ocrRecognizer = ocrFixtureFunc(func(context.Context, []byte) (ocr.Result, error) { return ocrFixtureResult(), nil })
	return s, SourceOCRInput{ThumbnailRequest: ThumbnailRequest{RequestID: "ocr-test", ProjectID: b.Project.ID, SourceEpoch: b.SourceEpoch, AssetID: asset.ID, Fingerprint: asset.Fingerprint}, ProjectRevision: b.Project.Revision}
}
func TestSourceOCRReviewAcceptUndoReopenAndScopedSource(t *testing.T) {
	s, input := setupOCRFixture(t)
	draft, err := s.PrepareSourceOCR(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.GetBootstrap().Project.VisualAnalyses) != 0 || draft.Frame.Evidence.PositionUs != 0 {
		t.Fatal("识别未经核对写入或证据错误")
	}
	accept := AcceptSourceOCRInput{ID: draft.ID, ProjectID: draft.ProjectID, ProjectRevision: draft.ProjectRevision, SourceEpoch: draft.SourceEpoch, Lines: []string{"人工纠正文字"}}
	b, err := s.AcceptSourceOCR(context.Background(), accept)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Project.Clips) != 0 || len(b.Project.VisualAnalyses) != 1 || b.Project.VisualAnalyses[0].Producer != "apple-vision" || b.Project.VisualAnalyses[0].Notes[0].Text != "人工纠正文字" {
		t.Fatal("来源、人工修正或时间线不符")
	}
	if _, err = s.AcceptSourceOCR(context.Background(), accept); err != nil {
		t.Fatal("重复确认未幂等", err)
	}
	different := accept
	different.Lines = []string{"另一个文本"}
	if _, err = s.AcceptSourceOCR(context.Background(), different); err == nil {
		t.Fatal("已接纳回执静默接收不同内容")
	}
	file := filepath.Join(t.TempDir(), "ocr.aividcut")
	if _, err = s.SaveProjectTo(file); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Undo(); err != nil || len(s.GetBootstrap().Project.VisualAnalyses) != 0 {
		t.Fatal("撤销未清理", err)
	}
	if _, err = s.AcceptSourceOCR(context.Background(), accept); err != nil || len(s.GetBootstrap().Project.VisualAnalyses) != 0 {
		t.Fatal("旧回执重放了撤销", err)
	}
	if _, err = s.OpenProjectPath(file); err != nil {
		t.Fatal(err)
	}
	if s.GetBootstrap().Project.SchemaVersion != editor.SchemaVersion || len(s.GetBootstrap().Project.VisualAnalyses) != 1 {
		t.Fatal("保存重开丢记录")
	}
	if _, err = s.AcceptSourceOCR(context.Background(), accept); err == nil {
		t.Fatal("新实例仍允许旧候选")
	}
}

func TestSourceOCRRejectsStaleWorkEmptyResultAndPreCancelledID(t *testing.T) {
	s, input := setupOCRFixture(t)
	s.CancelSourceOCR(input.RequestID)
	if _, err := s.PrepareSourceOCR(context.Background(), input); !errors.Is(err, context.Canceled) {
		t.Fatal("先到的取消未阻止启动", err)
	}
	input.RequestID = "empty-ocr"
	s.ocrRecognizer = ocrFixtureFunc(func(context.Context, []byte) (ocr.Result, error) {
		r := ocrFixtureResult()
		r.Lines = []ocr.Line{}
		return r, nil
	})
	draft, err := s.PrepareSourceOCR(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptSourceOCR(context.Background(), AcceptSourceOCRInput{ID: draft.ID, ProjectID: draft.ProjectID, ProjectRevision: draft.ProjectRevision, SourceEpoch: draft.SourceEpoch, Lines: []string{"空结果不伪造"}}); err == nil {
		t.Fatal("空结果允许伪造接纳")
	}
	input.RequestID = "stale-ocr"
	s.ocrRecognizer = ocrFixtureFunc(func(context.Context, []byte) (ocr.Result, error) {
		_, err := s.ApplyEdit(editor.EditRequest{BaseRevision: input.ProjectRevision, IdempotencyKey: "during-ocr", Commands: []editor.Command{{Type: "update_project", Name: "识别期间修改"}}})
		return ocrFixtureResult(), err
	})
	if _, err = s.PrepareSourceOCR(context.Background(), input); err == nil {
		t.Fatal("旧版本识别结果仍展示")
	}
}
func TestSourceOCRCancellationStaleAndMissingSource(t *testing.T) {
	s, input := setupOCRFixture(t)
	started := make(chan struct{})
	s.ocrRecognizer = ocrFixtureFunc(func(ctx context.Context, _ []byte) (ocr.Result, error) {
		close(started)
		<-ctx.Done()
		return ocr.Result{}, ctx.Err()
	})
	done := make(chan error, 1)
	go func() { _, err := s.PrepareSourceOCR(context.Background(), input); done <- err }()
	<-started
	if _, err := s.PrepareSourceOCR(context.Background(), input); err == nil {
		t.Fatal("并发识别未拒绝")
	}
	s.CancelSourceOCR(input.RequestID)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("取消未贯通", err)
	}
	s.ocrRecognizer = ocrFixtureFunc(func(context.Context, []byte) (ocr.Result, error) { return ocrFixtureResult(), nil })
	input.RequestID = "ocr-after-cancel"
	draft, err := s.PrepareSourceOCR(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	asset := s.GetBootstrap().Project.Assets[0]
	if err = os.Remove(asset.Path); err != nil {
		t.Fatal(err)
	}
	_, err = s.AcceptSourceOCR(context.Background(), AcceptSourceOCRInput{ID: draft.ID, ProjectID: draft.ProjectID, ProjectRevision: draft.ProjectRevision, SourceEpoch: draft.SourceEpoch, Lines: []string{"不可写入"}})
	if err == nil || len(s.GetBootstrap().Project.VisualAnalyses) != 0 {
		t.Fatal("源缺失仍接纳")
	}
}
