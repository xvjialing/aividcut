package application

import (
	"context"
	"errors"
	"testing"

	"aividcut/internal/media"
)

func TestWaveformApplicationScopeReadOnlyAndCancelBeforeStart(t *testing.T) {
	tools := preciseApplicationTools(t)
	s := preciseApplication(t, tools)
	state, err := s.ImportPaths(context.Background(), []string{preciseApplicationWAV(t)})
	if err != nil {
		t.Fatal(err)
	}
	a := state.Project.Assets[0]
	r := WaveformRequest{RequestID: "wave-read", ProjectID: state.Project.ID, AssetID: a.ID, Fingerprint: a.Fingerprint}
	tile, err := s.GetWaveformTile(context.Background(), r)
	if err != nil || tile.SampleCount != 5777 || len(tile.Peaks) != 13 {
		t.Fatal("真实素材波形未接通", tile.SampleCount, err)
	}
	after := s.GetBootstrap()
	if after.Project.Revision != state.Project.Revision || after.Dirty != state.Dirty || after.CanUndo != state.CanUndo {
		t.Fatal("波形读取修改工程状态")
	}
	for _, bad := range []WaveformRequest{{RequestID: "bad-project", ProjectID: "other", AssetID: a.ID, Fingerprint: a.Fingerprint}, {RequestID: "bad-source", ProjectID: r.ProjectID, AssetID: a.ID, Fingerprint: "changed"}, {RequestID: "bad-index", ProjectID: r.ProjectID, AssetID: a.ID, Fingerprint: a.Fingerprint, Index: 21600}} {
		if _, err := s.GetWaveformTile(context.Background(), bad); err == nil {
			t.Fatal("无效范围被接受", bad)
		}
	}
	r.RequestID = "cancel-raced"
	s.CancelWaveformRequest(r.RequestID)
	if _, err := s.GetWaveformTile(context.Background(), r); !errors.Is(err, context.Canceled) {
		t.Fatal("先到的取消请求被丢失", err)
	}
	_, _ = s.NewProject("新工程")
	r.RequestID = "old-project"
	if _, err := s.GetWaveformTile(context.Background(), r); err == nil {
		t.Fatal("切换工程仍接受旧范围")
	}
}

func TestWaveformApplicationUnavailableToolsAreExplicit(t *testing.T) {
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.GetWaveformTile(context.Background(), WaveformRequest{RequestID: "empty"}); err == nil {
		t.Fatal("空工程返回假波形")
	}
}
