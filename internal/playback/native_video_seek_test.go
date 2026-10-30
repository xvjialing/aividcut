package playback

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"aividcut/internal/editor"
	"aividcut/internal/media"
)

func numberedSource(t *testing.T, variable bool) editor.Asset {
	t.Helper()
	path := filepath.Join(t.TempDir(), "numbered.mp4")
	graph := "geq=lum='if(mod(floor(N/pow(2,floor(X/8)+8*floor(Y/16))),2),220,40)':cb=128:cr=128"
	if variable {
		graph += ",select='not(eq(mod(n,5),2))'"
	}
	nativeSourceCommand(t, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "nullsrc=s=64x32:r=60:d=30", "-vf", graph, "-fps_mode", "vfr", "-c:v", "libx264", "-preset", "fast", "-crf", "18", "-pix_fmt", "yuv420p", "-g", "120", "-bf", "2", "-x264-params", "b-adapt=0:scenecut=0", "-threads", "1", path)
	asset, err := media.Discover().Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return asset
}

func sourceFrameNumber(pixels []byte, width int) int {
	value := 0
	for bit := 0; bit < 16; bit++ {
		at := ((8+16*(bit/8))*width + 4 + 8*(bit%8)) * 4
		if int(pixels[at])+int(pixels[at+1])+int(pixels[at+2]) > 3*128 {
			value |= 1 << bit
		}
	}
	return value
}

func numberedProject(asset editor.Asset, n, d int) editor.Project {
	p := editor.NewProject("长GOP编号帧验收")
	p.Settings.Width, p.Settings.Height, p.Settings.FPSNum, p.Settings.FPSDen = 64, 32, 30000, 1001
	p.Assets = []editor.Asset{asset}
	clip := editor.DefaultClip("numbered", "track-video-1", "video")
	clip.AssetID, clip.SourceInUs, clip.SpeedNum, clip.SpeedDen = asset.ID, 12345, n, d
	clip.DurationFrames = (asset.DurationUs - clip.SourceInUs) * int64(p.Settings.FPSNum) * int64(d) / (1_000_000 * int64(p.Settings.FPSDen) * int64(n))
	p.Clips = []editor.Clip{clip}
	return p
}

func TestNativeVideoLongGOPSeekAndSplitMatchIndependentFrameNumbers(t *testing.T) {
	s := nativeSourceSession(t)
	for _, variable := range []bool{false, true} {
		asset := numberedSource(t, variable)
		for _, speed := range [][2]int{{1, 2}, {1, 1}, {3, 2}, {2, 1}} {
			t.Run(fmt.Sprintf("vfr-%t-speed-%d-%d", variable, speed[0], speed[1]), func(t *testing.T) {
				p := numberedProject(asset, speed[0], speed[1])
				duration := editor.DurationFrames(p)
				reference := nativeSourceCommand(t, "-hide_banner", "-loglevel", "error", "-threads", "1", "-ss", "0.012345", "-i", asset.Path, "-vf", fmt.Sprintf("setpts=(PTS-STARTPTS)*%d/%d,fps=30000/1001", speed[1], speed[0]), "-frames:v", fmt.Sprint(duration), "-pix_fmt", "rgba", "-f", "rawvideo", "pipe:1")
				if int64(len(reference)) != duration*64*32*4 {
					t.Fatal("参考视频帧数不足")
				}
				assertFrame := func(at int64) {
					frame, err := s.Preview(context.Background(), at)
					if err != nil {
						t.Fatal(err)
					}
					want := sourceFrameNumber(reference[at*64*32*4:], 64)
					actual := sourceFrameNumber(frame.RGBA, 64)
					if actual != want {
						t.Fatalf("实际选帧改变：工程帧%d source%d != reference%d", at, actual, want)
					}
				}
				if _, err := s.Load(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				for _, at := range []int64{0, duration - 2, duration - 2, 1, duration / 2, 119, 120, 121, 0} {
					assertFrame(at)
				}
				for i := int64(1); i < 32; i++ {
					at := duration * i / 32
					for _, part := range p.Clips {
						if part.StartFrame < at && at < part.StartFrame+part.DurationFrames {
							p = nativeSourceEdit(t, p, editor.Command{Type: "split_clip", ClipID: part.ID, AtFrame: at})
							break
						}
					}
				}
				if _, err := s.Load(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				for i := int64(1); i < 32; i++ {
					at := duration * i / 32
					assertFrame(at - 1)
					assertFrame(at)
					assertFrame(at + 1)
				}
				diagnostics, err := s.Diagnostics(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if diagnostics.SourceVideo == nil || diagnostics.SourceVideo.ActiveDecoders != 1 {
					t.Fatal("派生视频未共享单个活跃源解码器")
				}
			})
		}
	}
}

func TestNativeVideoSourceReuseKeepsPlacementAndAppearanceIndependent(t *testing.T) {
	asset := numberedSource(t, false)
	reference := nativeSourceCommand(t, "-hide_banner", "-loglevel", "error", "-threads", "1", "-ss", "0.012345", "-i", asset.Path, "-vf", "setpts=PTS-STARTPTS,fps=30000/1001", "-frames:v", "20", "-pix_fmt", "rgba", "-f", "rawvideo", "pipe:1")
	for _, delay := range []int64{0, 5} {
		t.Run(fmt.Sprintf("placement-%d", delay), func(t *testing.T) {
			s := nativeSourceSession(t)
			p := numberedProject(asset, 1, 1)
			p.Settings.Width = 128
			clip, err := editor.CaptureSourcePhase(p.Clips[0], asset, p.Settings)
			if err != nil {
				t.Fatal(err)
			}
			clip.Transform.X = -32
			copy := clip
			copy.SourcePhase = editor.CloneSourcePhase(clip.SourcePhase)
			copy.ID, copy.TrackID, copy.StartFrame, copy.Transform.X = "copy", "copy-track", delay, 32
			p.Tracks = append(p.Tracks, editor.Track{ID: copy.TrackID, Name: "独立构图", Kind: "video"})
			p.Clips = []editor.Clip{clip, copy}
			if _, err := s.Load(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			for _, at := range []int64{10, 11, 5, 15, 10} {
				frame, err := s.Preview(context.Background(), at)
				if err != nil {
					t.Fatal(err)
				}
				left, right := sourceFrameNumber(frame.RGBA, 128), sourceFrameNumber(frame.RGBA[64*4:], 128)
				wantLeft, wantRight := sourceFrameNumber(reference[at*64*32*4:], 64), sourceFrameNumber(reference[(at-delay)*64*32*4:], 64)
				if left != wantLeft || right != wantRight {
					t.Fatalf("放置或构图交叉污染：frame%d left%d/%d right%d/%d", at, left, wantLeft, right, wantRight)
				}
			}
			d, err := s.Diagnostics(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			wanted := uint64(1)
			if delay != 0 {
				wanted = 2
			}
			if d.SourceVideo == nil || d.SourceVideo.Opens != wanted || d.SourceVideo.ActiveDecoders != wanted {
				t.Fatalf("放置相位复用范围不符：%+v", d.SourceVideo)
			}
		})
	}
}

func TestNativeVideoCacheEvictionKeepsBorrowedFramesValid(t *testing.T) {
	asset := numberedSource(t, false)
	s := nativeSourceSession(t)
	p := numberedProject(asset, 1, 1)
	p.Assets = nil
	p.Clips = nil
	for n := 0; n < 12; n++ {
		copy := asset
		copy.ID = fmt.Sprintf("source-%02d", n)
		track := editor.Track{ID: fmt.Sprintf("track-%d", n), Name: "叠加验证", Kind: "video"}
		clip := editor.DefaultClip(fmt.Sprintf("clip-%d", n), track.ID, "video")
		clip.AssetID, clip.SourceInUs, clip.DurationFrames = copy.ID, 12345, 8
		p.Tracks = append(p.Tracks, track)
		p.Assets = append(p.Assets, copy)
		p.Clips = append(p.Clips, clip)
	}
	reference := nativeSourceCommand(t, "-hide_banner", "-loglevel", "error", "-ss", "0.012345", "-i", asset.Path, "-vf", "setpts=PTS-STARTPTS,fps=30000/1001", "-frames:v", "8", "-pix_fmt", "rgba", "-f", "rawvideo", "pipe:1")
	if _, err := s.Load(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	for _, at := range []int64{0, 1, 5, 2, 7} {
		frame, err := s.Preview(context.Background(), at)
		if err != nil {
			t.Fatal(err)
		}
		if sourceFrameNumber(frame.RGBA, 64) != sourceFrameNumber(reference[at*64*32*4:], 64) {
			t.Fatal("视频缓存驱逐使待合成帧失效")
		}
		d, err := s.Diagnostics(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if d.SourceVideo == nil || d.SourceVideo.ActiveDecoders > 8 || d.SourceVideo.ResidentStates > 8 {
			t.Fatalf("视频处理缓存未受限：%+v", d.SourceVideo)
		}
	}
	if _, err := s.Load(context.Background(), numberedProject(asset, 1, 1)); err != nil {
		t.Fatal(err)
	}
	d, err := s.Diagnostics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d.SourceVideo.ActiveDecoders != 0 || d.SourceVideo.ResidentStates != 0 {
		t.Fatal("Load未释放旧视频缓存")
	}
}
