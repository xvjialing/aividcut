package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/playback"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClipboardActualVideoRestoresSplitPhaseAndEffects(t *testing.T) {
	if os.Getenv("AIVIDCUT_MLT_WORKER") == "" {
		t.Skip("需要显式原生worker，不把跳过当作真实视频验证")
	}
	tools := preciseApplicationTools(t)
	s := preciseApplication(t, tools)
	dir := t.TempDir()
	videoPath, lutPath := filepath.Join(dir, "moving.mp4"), filepath.Join(dir, "look.cube")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, tools.FFmpegPath, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30000/1001", "-t", "2", "-an", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", videoPath).CombinedOutput()
	if err != nil {
		t.Fatalf("生成自建视频：%v %s", err, out)
	}
	var cube strings.Builder
	cube.WriteString("LUT_3D_SIZE 2\nDOMAIN_MIN 0 0 0\nDOMAIN_MAX 1 1 1\n")
	for b := 0; b < 2; b++ {
		for g := 0; g < 2; g++ {
			for r := 0; r < 2; r++ {
				fmt.Fprintf(&cube, "%d %d %d\n", 1-r, g, b)
			}
		}
	}
	if err = os.WriteFile(lutPath, []byte(cube.String()), 0600); err != nil {
		t.Fatal(err)
	}
	imported, err := s.ImportPaths(ctx, []string{videoPath, lutPath})
	if err != nil {
		t.Fatal(err)
	}
	p := imported.Project
	settings := p.Settings
	settings.Width, settings.Height, settings.FPSNum, settings.FPSDen = 320, 180, 30000, 1001
	clip := editor.DefaultClip("moving", "track-video-1", "video")
	clip.DurationFrames, clip.SourceInUs, clip.SpeedNum, clip.SpeedDen = 12, 137000, 3, 2
	clip.Transform.Scale, clip.Transform.Rotation = 0.85, 7
	for _, a := range p.Assets {
		if a.Kind == "video" {
			clip.AssetID = a.ID
		}
		if a.Kind == "lut" {
			clip.LUT = &editor.LUT{AssetID: a.ID, Intensity: 0.37}
		}
	}
	state, err := s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "clipboard-video-fixture", Commands: []editor.Command{{Type: "update_project", Settings: &settings}, {Type: "add_clip", Clip: &clip}, {Type: "split_clips", ClipIDs: []string{clip.ID}, AtFrame: 2}, {Type: "delete_clip", ClipID: clip.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	collect := func(project editor.Project, start int64) []byte {
		t.Helper()
		session, err := playback.Start(ctx, PlaybackEnvironment())
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		if _, err = session.Load(ctx, project); err != nil {
			t.Fatal(err)
		}
		if _, err = session.Preview(ctx, start); err != nil {
			t.Fatal(err)
		}
		if err = session.Play(ctx); err != nil {
			t.Fatal(err)
		}
		var pixels []byte
		for i := int64(0); i < 6; i++ {
			frame, err := session.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if frame.Position != start+i {
				t.Fatal("帧序错误")
			}
			pixels = append(pixels, frame.RGBA...)
		}
		return pixels
	}
	before := collect(state.Project, 2)
	original := state.Project.Clips[0]
	copied, err := s.CopyTimelineSelection(ClipboardSelectionInput{ProjectID: state.Project.ID, SourceEpoch: state.SourceEpoch, BaseRevision: state.Project.Revision, ClipIDs: []string{original.ID}})
	if err != nil {
		t.Fatal(err)
	}
	original.Transform.X = 40
	original.LUT.Intensity = 0.75
	changed, err := s.ApplyEdit(editor.EditRequest{BaseRevision: state.Project.Revision, IdempotencyKey: "modify-delete-video", Commands: []editor.Command{{Type: "update_clip", Clip: &original}, {Type: "delete_clip", ClipID: original.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	pasted, err := s.PasteTimelineSelection(ClipboardPasteInput{ProjectID: changed.Project.ID, SourceEpoch: changed.SourceEpoch, BaseRevision: changed.Project.Revision, ClipboardID: copied.Clipboard.ID, IdempotencyKey: "paste-frozen-video", AtFrame: 32})
	if err != nil {
		t.Fatal(err)
	}
	after := collect(pasted.Bootstrap.Project, 32)
	if !bytes.Equal(before, after) {
		t.Fatal("视频分割相位、LUT或变换在快照恢复后改变", len(before), len(after))
	}
	if pasted.Bootstrap.Project.Clips[0].SourcePhase.OffsetFrames != 2 {
		t.Fatal("源处理相位丢失")
	}
	trimExpected := collect(pasted.Bootstrap.Project, 33)
	trimmed, err := s.ApplyEdit(editor.EditRequest{BaseRevision: pasted.Bootstrap.Project.Revision, IdempotencyKey: "trim-frozen-video-head", Commands: []editor.Command{{Type: "trim_clips", ClipIDs: []string{pasted.Bootstrap.Project.Clips[0].ID}, Edge: "start", FrameDelta: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(trimExpected, collect(trimmed.Project, 33)) || trimmed.Project.Clips[0].SourcePhase.OffsetFrames != 3 {
		t.Fatal("视频头部修剪改变原处理输出窗")
	}
	shortened, err := s.ApplyEdit(editor.EditRequest{BaseRevision: trimmed.Project.Revision, IdempotencyKey: "trim-frozen-video-tail", Commands: []editor.Command{{Type: "trim_clips", ClipIDs: []string{trimmed.Project.Clips[0].ID}, Edge: "end", FrameDelta: -2}}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(trimExpected, collect(shortened.Project, 33)) {
		t.Fatal("视频尾部修剪改变保留窗口")
	}
	t.Logf("真实视频两端修剪，保留6帧%d字节画面完全一致，相位偏移3", len(trimExpected))
	t.Logf("实际移动视频：剪切相位2帧、1.5倍速、37%% LUT及7度旋转，修改/删除原片段后粘贴，6帧%d画面字节完全一致", len(after))
}
