package application

import (
	"aividcut/internal/media"
	"context"
	"errors"
	"image"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestThumbnailApplicationReadOnlyScopeAndRevocation(t *testing.T) {
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	path := filepath.Join(t.TempDir(), "源图.png")
	f, _ := os.Create(path)
	png.Encode(f, image.NewRGBA(image.Rect(0, 0, 64, 36)))
	f.Close()
	state, err := s.ImportPaths(context.Background(), []string{path})
	if err != nil {
		t.Fatal(err)
	}
	a := state.Project.Assets[0]
	request := ThumbnailRequest{RequestID: "read", ProjectID: state.Project.ID, AssetID: a.ID, Fingerprint: a.Fingerprint}
	result, err := s.GetAssetThumbnail(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(result.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	after := s.GetBootstrap()
	if after.Project.Revision != state.Project.Revision || after.Dirty != state.Dirty || after.CanUndo != state.CanUndo {
		t.Fatal("缩略图读取改变工程")
	}
	for _, field := range []string{"project", "fingerprint", "asset", "position"} {
		bad := request
		bad.RequestID = "bad-" + field
		switch field {
		case "project":
			bad.ProjectID = "other"
		case "fingerprint":
			bad.Fingerprint = "changed"
		case "asset":
			bad.AssetID = "/etc/passwd"
		case "position":
			bad.PositionUs = 1
		}
		if _, err := s.GetAssetThumbnail(context.Background(), bad); err == nil {
			t.Fatal("非法范围被接受", field)
		}
	}
	request.RequestID = "cancel-first"
	s.CancelThumbnailRequest(request.RequestID)
	if _, err := s.GetAssetThumbnail(context.Background(), request); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	save := filepath.Join(t.TempDir(), "saved.aividcut")
	if _, err := s.SaveProjectTo(save); err != nil {
		t.Fatal(err)
	}
	reopened, err := s.OpenProjectPath(save)
	if err != nil {
		t.Fatal(err)
	}
	request.RequestID = "old-instance"
	if _, err := s.GetAssetThumbnail(context.Background(), request); err == nil {
		t.Fatal("同一工程重开后接受旧实例请求")
	}
	request.SourceEpoch = reopened.SourceEpoch
	request.RequestID = "reopen"
	fresh, err := s.GetAssetThumbnail(context.Background(), request)
	if err != nil || fresh.URL == result.URL {
		t.Fatal("重开工程复用了旧能力", err)
	}
	response, err = http.Get(result.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("旧缩略图未撤销", response.StatusCode)
	}
	s.NewProject("切换")
	request.RequestID = "old"
	if _, err := s.GetAssetThumbnail(context.Background(), request); err == nil {
		t.Fatal("切换后仍接受旧素材")
	}
}
