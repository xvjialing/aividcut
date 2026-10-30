package playback

import (
	"aividcut/internal/editor"
	"aividcut/internal/projectstore"
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

func TestNaturalLongTailMatchesIsolatedSourceFrame(t *testing.T) {
	path := os.Getenv("AIVIDCUT_LONG_PROJECT")
	if path == "" {
		t.Skip("需要显式长摄影工程，不自动读取用户文件")
	}
	config := nativeConfig(t)
	config.MaxWidth, config.MaxHeight = 640, 360
	p, err := projectstore.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Clips) != 1 || p.Clips[0].Kind != "video" || p.Clips[0].SourcePhase != nil || p.Clips[0].SourceInUs != 0 || p.Clips[0].SpeedNum != 1 || p.Clips[0].SpeedDen != 1 || p.Clips[0].DurationFrames <= 15000 {
		t.Fatal("长尾帧对照需要超过15000帧的单源原速工程")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := Start(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err = session.Load(ctx, p); err != nil {
		t.Fatal(err)
	}
	for _, at := range []int64{0, 14999, 15000, p.Clips[0].DurationFrames - 1, 7000} {
		frame, err := session.Preview(ctx, at)
		if err != nil {
			t.Fatalf("整片定位%d失败：%v", at, err)
		}
		pixels := append([]byte{}, frame.RGBA...)
		isolated := editor.CloneProject(p)
		isolated.Clips[0].DurationFrames = 1
		isolated.Clips[0].SourceInUs = at * 1_000_000 * int64(p.Settings.FPSDen) / int64(p.Settings.FPSNum)
		other, err := Start(ctx, config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = other.Load(ctx, isolated); err != nil {
			other.Close()
			t.Fatal(err)
		}
		expected, err := other.Preview(ctx, 0)
		if err != nil {
			other.Close()
			t.Fatal(err)
		}
		matches := bytes.Equal(pixels, expected.RGBA)
		other.Close()
		if !matches {
			t.Fatalf("整片%d帧与对应源时间单帧不同，可能在默认边界冻结", at)
		}
	}
}
