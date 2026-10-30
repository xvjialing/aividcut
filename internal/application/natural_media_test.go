package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/exporter"
	"aividcut/internal/media"
	"aividcut/internal/playback"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"time"
)

type naturalMediaCase struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	SourceInUs int64  `json:"sourceInUs"`
	Frames     int64  `json:"frames"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	FPSNum     int    `json:"fpsNum"`
	FPSDen     int    `json:"fpsDen"`
	SpeedNum   int    `json:"speedNum"`
	SpeedDen   int    `json:"speedDen"`
}
type naturalCapture struct {
	Frames        int64   `json:"frames"`
	Samples       int64   `json:"samples"`
	RGBA          string  `json:"rgbaSHA256"`
	PCM           string  `json:"pcmSHA256"`
	PeakAmplitude float64 `json:"peakAmplitude"`
	Nonzero       int64   `json:"nonzeroChannelSamples"`
}
type naturalDecoded struct {
	RGBA      string `json:"rgbaSHA256"`
	PCM       string `json:"pcmSHA256"`
	RGBABytes int64  `json:"rgbaBytes"`
	PCMBytes  int64  `json:"pcmBytes"`
}
type naturalHashWriter struct {
	hash.Hash
	count int64
}

func (w *naturalHashWriter) Write(data []byte) (int, error) {
	n, err := w.Hash.Write(data)
	w.count += int64(n)
	return n, err
}

// 需显式提供来源/指纹清单；默认不联网下载，也不将skip当作摄影素材验收。
func TestNaturalMediaImportSplitUndoExportAndReopen(t *testing.T) {
	manifestPath := os.Getenv("AIVIDCUT_MEDIA_SAMPLES")
	if manifestPath == "" {
		t.Skip("需要显式AIVIDCUT_MEDIA_SAMPLES与原生SDK，未自动下载素材")
	}
	if os.Getenv("AIVIDCUT_MLT_WORKER") == "" {
		t.Fatal("实际素材验收需要显式原生worker")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil || len(data) > 65536 {
		t.Fatal("样本清单不可读或超过64KiB", err)
	}
	var manifest struct {
		SchemaVersion int                `json:"schemaVersion"`
		Cases         []naturalMediaCase `json:"cases"`
	}
	if err = json.Unmarshal(data, &manifest); err != nil || manifest.SchemaVersion != 1 || len(manifest.Cases) < 1 || len(manifest.Cases) > 16 {
		t.Fatal("需要v1的1到16个样本清单", err)
	}
	output := t.TempDir()
	if explicit := os.Getenv("AIVIDCUT_MEDIA_EVIDENCE"); explicit != "" {
		output, err = filepath.Abs(explicit)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Mkdir(output, 0700); err != nil {
			t.Fatal("证据输出目录须尚不存在，未覆盖旧结果", err)
		}
	}
	tools := preciseApplicationTools(t)
	rows := []map[string]any{}
	seen := map[string]bool{}
	for _, sample := range manifest.Cases {
		if !regexp.MustCompile(`^[a-z0-9-]{1,64}$`).MatchString(sample.ID) || seen[sample.ID] || len(sample.SHA256) != 64 || sample.Frames < 36 || sample.Frames > 900 || sample.Width < 64 || sample.Width > 1920 || sample.Width%2 != 0 || sample.Height < 32 || sample.Height > 1080 || sample.Height%2 != 0 {
			t.Fatal("样本标识、指纹或画幅/帧数超出有界验收范围")
		}
		seen[sample.ID] = true
		t.Run(sample.ID, func(t *testing.T) {
			row := map[string]any{"id": sample.ID, "inputSHA256": sample.SHA256}
			defer func() { row["passed"] = !t.Failed(); rows = append(rows, row) }()
			dir := filepath.Join(output, sample.ID)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			fingerprint := func() string {
				t.Helper()
				f, err := os.Open(sample.Path)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				h := sha256.New()
				if _, err = io.Copy(h, f); err != nil {
					t.Fatal(err)
				}
				return hex.EncodeToString(h.Sum(nil))
			}
			if fingerprint() != sample.SHA256 {
				t.Fatal("样本字节与清单不一致")
			}
			s := preciseApplication(t, tools)
			state, err := s.ImportPaths(ctx, []string{sample.Path})
			if err != nil || len(state.Project.Assets) != 1 || state.Project.Assets[0].Kind != "video" || !state.Project.Assets[0].HasAudio {
				t.Fatal("实际有声视频导入失败", err)
			}
			row["importWarnings"] = state.Warnings
			settings := state.Project.Settings
			settings.Width, settings.Height, settings.FPSNum, settings.FPSDen = sample.Width, sample.Height, sample.FPSNum, sample.FPSDen
			clip := editor.DefaultClip("natural-source", "track-video-1", "video")
			clip.AssetID, clip.SourceInUs, clip.DurationFrames, clip.SpeedNum, clip.SpeedDen = state.Project.Assets[0].ID, sample.SourceInUs, sample.Frames, sample.SpeedNum, sample.SpeedDen
			state, err = s.ApplyEdit(editor.EditRequest{BaseRevision: state.Project.Revision, IdempotencyKey: "natural-setup", Commands: []editor.Command{{Type: "update_project", Settings: &settings}, {Type: "add_clip", Clip: &clip}}})
			if err != nil {
				t.Fatal(err)
			}
			whole := state.Project
			capture := func(p editor.Project, label string) naturalCapture {
				t.Helper()
				session, err := playback.Start(ctx, PlaybackEnvironment())
				if err != nil {
					t.Fatal(err)
				}
				defer session.Close()
				if _, err = session.Load(ctx, p); err != nil {
					t.Fatal(err)
				}
				if _, err = session.Preview(ctx, 0); err != nil {
					t.Fatal(err)
				}
				if err = session.Play(ctx); err != nil {
					t.Fatal(err)
				}
				pixels, pcm := sha256.New(), sha256.New()
				result := naturalCapture{}
				for at := int64(0); at < sample.Frames; at++ {
					frame, err := session.Next(ctx)
					if err != nil || frame.Position != at || frame.AudioStartSample != result.Samples || frame.Samples < 1 || len(frame.PCM) != frame.Samples*8 {
						t.Fatal("实际帧/声音锚不连续", at, err)
					}
					if at == 0 {
						imageFile, err := os.Create(filepath.Join(dir, label+".png"))
						if err != nil {
							t.Fatal(err)
						}
						err = png.Encode(imageFile, &image.RGBA{Pix: frame.RGBA, Stride: frame.Width * 4, Rect: image.Rect(0, 0, frame.Width, frame.Height)})
						closeErr := imageFile.Close()
						if err != nil || closeErr != nil {
							t.Fatal(err, closeErr)
						}
					}
					for offset := 0; offset < len(frame.PCM); offset += 4 {
						v := float64(math.Float32frombits(binary.LittleEndian.Uint32(frame.PCM[offset:])))
						if math.IsNaN(v) || math.IsInf(v, 0) {
							t.Fatal("自然素材输出非有限PCM")
						}
						if v != 0 {
							result.Nonzero++
							result.PeakAmplitude = max(result.PeakAmplitude, math.Abs(v))
						}
					}
					pixels.Write(frame.RGBA)
					pcm.Write(frame.PCM)
					result.Frames++
					result.Samples += int64(frame.Samples)
				}
				result.RGBA, result.PCM = hex.EncodeToString(pixels.Sum(nil)), hex.EncodeToString(pcm.Sum(nil))
				if result.Nonzero == 0 {
					t.Fatal("所选有声片段没有实际非零PCM")
				}
				return result
			}
			before := capture(whole, "whole")
			commands := []editor.Command{}
			for _, at := range []int64{sample.Frames - 1, sample.Frames / 2, 17, 1} {
				commands = append(commands, editor.Command{Type: "split_clip", ClipID: clip.ID, AtFrame: at})
			}
			split, err := s.ApplyEdit(editor.EditRequest{BaseRevision: state.Project.Revision, IdempotencyKey: "natural-cuts", Commands: commands})
			if err != nil {
				t.Fatal(err)
			}
			after := capture(split.Project, "split")
			row["whole"], row["split"] = before, after
			if before != after {
				t.Fatal("真实素材分割后音画改变", before, after)
			}
			undone, err := s.Undo()
			if err != nil || !reflect.DeepEqual(undone.Project.Clips, whole.Clips) {
				t.Fatal("真实素材分割未一次撤销", err)
			}
			redone, err := s.Redo()
			if err != nil || !reflect.DeepEqual(redone.Project.Clips, split.Project.Clips) {
				t.Fatal("真实素材重做改变片段", err)
			}
			projectPath := filepath.Join(dir, "split.aividcut")
			if _, err = s.SaveProjectTo(projectPath); err != nil {
				t.Fatal(err)
			}
			reopened, err := s.OpenProjectPath(projectPath)
			if err != nil || !reflect.DeepEqual(reopened.Project.Clips, split.Project.Clips) {
				t.Fatal("真实素材保存重开改变片段", err)
			}
			decoded := []naturalDecoded{}
			for i, p := range []editor.Project{whole, reopened.Project} {
				path := filepath.Join(dir, []string{"whole.mp4", "split.mp4"}[i])
				if err = exporter.Native(ctx, tools, PlaybackEnvironment(), p, media.ExportOptions{Path: path, Preset: "project", VideoEncoder: "libx264"}, nil); err != nil {
					t.Fatal("自然素材原生导出失败", err)
				}
				outputs := []*naturalHashWriter{}
				for _, args := range [][]string{{"-map", "0:v:0", "-pix_fmt", "rgba", "-f", "rawvideo"}, {"-map", "0:a:0", "-ac", "2", "-ar", "48000", "-f", "f32le"}} {
					writer := &naturalHashWriter{Hash: sha256.New()}
					command := exec.CommandContext(ctx, tools.FFmpegPath, append(append([]string{"-v", "error", "-nostdin", "-i", path}, args...), "pipe:1")...)
					command.Stdout = writer
					if err = command.Run(); err != nil {
						t.Fatal("独立FFmpeg解码导出失败", err)
					}
					outputs = append(outputs, writer)
				}
				decoded = append(decoded, naturalDecoded{RGBA: hex.EncodeToString(outputs[0].Sum(nil)), PCM: hex.EncodeToString(outputs[1].Sum(nil)), RGBABytes: outputs[0].count, PCMBytes: outputs[1].count})
			}
			row["exports"] = decoded
			if decoded[0] != decoded[1] || decoded[0].RGBABytes != sample.Frames*int64(sample.Width*sample.Height*4) || decoded[0].PCMBytes != before.Samples*8 {
				t.Fatal("两次导出解码不一致或帧/音尾错误", decoded, before.Samples)
			}
			if fingerprint() != sample.SHA256 {
				t.Fatal("实际验收期间源字节发生变化")
			}
		})
	}
	report, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(output, "matrix.json"), report, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("实际素材矩阵：%s", filepath.Join(output, "matrix.json"))
}
