package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/ocr"
	"context"
	"path/filepath"
	"testing"
)

func TestWindowsOCRContractAcceptPersistsRealSourceAndReopens(t *testing.T) {
	s, input := setupOCRFixture(t)
	s.ocrRecognizer = ocrFixtureFunc(func(context.Context, []byte) (ocr.Result, error) {
		return ocr.Result{Version: 1, Engine: "windows-media-ocr", Revision: 0, Languages: []string{"en-US"}, Lines: []ocr.Line{{Text: "Windows contract fixture"}}}, nil
	})
	draft, err := s.PrepareSourceOCR(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AcceptSourceOCR(context.Background(), AcceptSourceOCRInput{ID: draft.ID, ProjectID: draft.ProjectID, ProjectRevision: draft.ProjectRevision, SourceEpoch: draft.SourceEpoch, Lines: []string{"人工核对Windows契约"}})
	if err != nil {
		t.Fatal(err)
	}
	value := b.Project.VisualAnalyses[0]
	if value.Producer != "windows-media-ocr" || value.MethodVersion != "windows-media-ocr-v1" || b.Project.SchemaVersion != editor.SchemaVersion {
		t.Fatal("来源或工程版本丢失")
	}
	path := filepath.Join(t.TempDir(), "windows-contract.aividcut")
	if _, err = s.SaveProjectTo(path); err != nil {
		t.Fatal(err)
	}
	if _, err = s.OpenProjectPath(path); err != nil {
		t.Fatal(err)
	}
	if s.GetBootstrap().Project.VisualAnalyses[0].Producer != "windows-media-ocr" {
		t.Fatal("重开伪装来源")
	}
}
