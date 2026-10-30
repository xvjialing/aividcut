package projectcollect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"aividcut/internal/editor"
	"aividcut/internal/projectstore"
)

func fixture(t *testing.T) (editor.Project, string) {
	t.Helper()
	dir := t.TempDir()
	p := editor.NewProject("收集夹具")
	p.Settings.FPSNum, p.Settings.FPSDen = 30000, 1001
	for index, item := range []struct{ id, kind, ext, body string }{{"video", "video", ".mp4", strings.Repeat("video", 200000)}, {"image", "image", ".png", "image-A"}, {"duplicate", "image", ".png", "image-A"}, {"other", "image", ".png", "image-B"}, {"voice", "audio", ".wav", "audio"}, {"lut", "lut", ".cube", "cube"}} {
		folder := filepath.Join(dir, item.id)
		if err := os.Mkdir(folder, 0700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(folder, "同名"+item.ext)
		if err := os.WriteFile(path, []byte(item.body), 0600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(item.body))
		a := editor.Asset{ID: item.id, Name: "同名素材", Kind: item.kind, Path: path, DurationUs: 4_000_000, Width: 640, Height: 360, FPSNum: 30000, FPSDen: 1001, Fingerprint: "sha256:" + hex.EncodeToString(sum[:]), URL: "http://local/?token=fixture-secret"}
		if index == 0 {
			count := int64(192000)
			a.HasAudio = true
			a.AudioSampleRate = 48000
			a.AudioSampleCount = &count
		}
		p.Assets = append(p.Assets, a)
	}
	v := editor.DefaultClip("video-clip", "track-video-1", "video")
	v.AssetID = "video"
	v.SourceInUs = 137000
	v.LUT = &editor.LUT{AssetID: "lut", Intensity: .37}
	v, err := editor.CaptureSourcePhase(v, p.Assets[0], p.Settings)
	if err != nil {
		t.Fatal(err)
	}
	c := editor.DefaultClip("manual-caption", "track-caption-1", "caption")
	c.Text = "人工字幕保留"
	c.StartFrame = 10
	c.DurationFrames = 20
	p.Clips = []editor.Clip{v, c}
	return p, dir
}

func TestCollectRelativeMigrationDedupAndFullProject(t *testing.T) {
	p, sources := fixture(t)
	original := editor.CloneProject(p)
	parent := t.TempDir()
	path, err := Collect(context.Background(), p, parent, "中文副本", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p, original) {
		t.Fatal("收集修改了源工程")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "token=") || strings.Contains(string(contents), sources) {
		t.Fatal("副本保留会话地址或原始绝对路径")
	}
	var portable editor.Project
	if err := json.Unmarshal(contents, &portable); err != nil {
		t.Fatal(err)
	}
	if portable.Assets[1].Path != portable.Assets[2].Path || portable.Assets[1].Path == portable.Assets[3].Path {
		t.Fatal("重复内容未复用或同名内容覆盖")
	}
	for _, asset := range portable.Assets {
		if filepath.IsAbs(asset.Path) || !strings.HasPrefix(asset.Path, "media/") || strings.Contains(asset.Path, "\\") {
			t.Fatal("不是跨平台相对路径", asset.Path)
		}
	}
	moved := filepath.Join(t.TempDir(), "已迁移")
	if err := os.Rename(filepath.Dir(path), moved); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(sources); err != nil {
		t.Fatal(err)
	}
	loaded, err := projectstore.Load(filepath.Join(moved, "project.aividcut"))
	if err != nil {
		t.Fatal(err)
	}
	for index, asset := range loaded.Assets {
		if asset.Missing {
			t.Fatal("迁移后仍依赖原路径", asset.ID)
		}
		original.Assets[index].Path = asset.Path
		original.Assets[index].URL = ""
	}
	if !reflect.DeepEqual(original, loaded) {
		t.Fatal("工程字段、字幕、LUT或取样相位在迁移中变化")
	}
	var manifest Manifest
	data, _ := os.ReadFile(filepath.Join(moved, "manifest.json"))
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 5 || manifest.AssetCount != 6 {
		t.Fatal(manifest)
	}
	for _, file := range manifest.Files {
		body, err := os.ReadFile(filepath.Join(moved, filepath.FromSlash(file.Path)))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != file.SHA256 || int64(len(body)) != file.Bytes {
			t.Fatal("清单与副本内容不符")
		}
	}
}

func TestCollectionCancellationChangedSourceAndOccupiedDestination(t *testing.T) {
	for _, mode := range []string{"cancel", "source-change", "fingerprint", "missing", "occupied"} {
		t.Run(mode, func(t *testing.T) {
			p, _ := fixture(t)
			parent := t.TempDir()
			target := filepath.Join(parent, "copy")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			changed := false
			if mode == "fingerprint" {
				p.Assets[0].Fingerprint = "sha256:" + strings.Repeat("0", 64)
			}
			if mode == "missing" {
				if err := os.Remove(p.Assets[0].Path); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "occupied" {
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, "keep"), []byte("用户文件"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Collect(ctx, p, parent, "copy", func(float64, string) {
				if !changed {
					changed = true
					if mode == "cancel" {
						cancel()
					}
					if mode == "source-change" {
						if err := os.WriteFile(p.Assets[0].Path, []byte("changed"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
			})
			if err == nil {
				t.Fatal("错误或取消未拒绝")
			}
			if mode == "occupied" {
				body, err := os.ReadFile(filepath.Join(target, "keep"))
				if err != nil || string(body) != "用户文件" {
					t.Fatal("覆盖已有目标")
				}
			} else if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatal("未清理本次未完成目录", err)
			}
		})
	}
}

func TestCollectionDirectoryReplacementDoesNotDeleteOtherFiles(t *testing.T) {
	p, _ := fixture(t)
	parent := t.TempDir()
	target, moved := filepath.Join(parent, "copy"), filepath.Join(parent, "moved")
	changed := false
	_, err := Collect(context.Background(), p, parent, "copy", func(float64, string) {
		if changed {
			return
		}
		changed = true
		if err := os.Rename(target, moved); err != nil {
			t.Skip("当前文件系统不支持在打开时移动目录")
		}
		if err := os.Mkdir(target, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, "keep"), []byte("替换目录"), 0600); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("目录替换未拒绝")
	}
	data, err := os.ReadFile(filepath.Join(target, "keep"))
	if err != nil || string(data) != "替换目录" {
		t.Fatal("误删替换目录")
	}
	if _, err := os.Stat(filepath.Join(moved, "project.aividcut")); !os.IsNotExist(err) {
		t.Fatal("移动位置仍发布了工程文件")
	}
}

func TestCollectionNameAndCommitCancellation(t *testing.T) {
	for _, name := range []string{"../escape", "a/b", "a\\b", "CON", "com1.txt", "name.", "x:y", strings.Repeat("字", 81)} {
		if _, err := FolderName(name); err == nil {
			t.Fatal("无效跨平台名称未拒绝", name)
		}
	}
	p, _ := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path, err := Collect(ctx, p, t.TempDir(), "完成后取消", func(_ float64, message string) {
		if message == "素材及工程副本已收集完成" {
			cancel()
		}
	})
	if err != nil {
		t.Fatal("提交后取消不应回滚", err)
	}
	if _, err := projectstore.Load(path); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionUnknownFingerprintRejectsSameSizeTimestampRewrite(t *testing.T) {
	p, _ := fixture(t)
	p.Assets[0].Fingerprint = ""
	path := p.Assets[0].Path
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	parent := t.TempDir()
	_, err = Collect(context.Background(), p, parent, "copy", func(float64, string) {
		if changed {
			return
		}
		changed = true
		if err := os.WriteFile(path, []byte(strings.Repeat("other", 200000)), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("无指纹源在相同大小/时间戳下的在途改写没有拒绝")
	}
	if _, err := os.Stat(filepath.Join(parent, "copy")); !os.IsNotExist(err) {
		t.Fatal("残留不完整副本")
	}
}
