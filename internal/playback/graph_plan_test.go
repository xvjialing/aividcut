package playback

import (
	"context"
	"fmt"
	"image/color"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"aividcut/internal/editor"
)

func graphTestCaps() Capabilities {
	return Capabilities{Available: true, WindowedGraph: Feature{Supported: true}, Text: Feature{Supported: true}, Speed: Feature{Supported: true}, LUT: Feature{Supported: true}, Producers: []string{"aividcut.source_time"}, Filters: []string{"qtext", "volume", "aividcut.audio_source"}, Transitions: []string{nativeVisualService, "aividcut.audio_mix"}}
}
func imageCuts(t *testing.T, count int) editor.Project {
	t.Helper()
	p := editor.NewProject("密集图窗口")
	p.Settings.Width, p.Settings.Height = 128, 96
	a := picture(t, "window-source.png", color.RGBA{R: 180, G: 40, B: 20, A: 255})
	p.Assets = []editor.Asset{a}
	for i := 0; i < count; i++ {
		clip := editor.DefaultClip(fmt.Sprintf("c%d", i), "track-video-1", "image")
		clip.AssetID = a.ID
		clip.StartFrame, clip.DurationFrames = int64(i), 1
		p.Clips = append(p.Clips, clip)
	}
	return p
}
func TestGraphPlanDenseEdgesWholeValidationAndImmutableSnapshot(t *testing.T) {
	p := imageCuts(t, 1000)
	caps := graphTestCaps()
	plan, err := prepareGraphPlan(p, caps, 128, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []int64{0, 1, 127, 128, 129, 239, 240, 998, 999} {
		window, err := plan.window(at)
		if err != nil {
			t.Fatal(err)
		}
		if window.from > at || window.to <= at || len(window.clips) > 128 {
			t.Fatal("窗口没有覆盖目标或超过预算", at, window)
		}
		for frame := window.from; frame < window.to; frame++ {
			found := false
			for _, clip := range window.clips {
				if clip.StartFrame == frame {
					found = true
					break
				}
			}
			if !found {
				t.Fatal("省略了窗口内的片段", frame)
			}
		}
	}
	p.Clips[0].Transform.Scale = 4
	if plan.project.Clips[0].Transform.Scale != 1 {
		t.Fatal("调用方可修改计划快照")
	}
	p.Clips[999].SpeedNum, p.Clips[999].SpeedDen = 100, 1
	if _, err := prepareGraphPlan(p, caps, 128, 0); err == nil {
		t.Fatal("后段不支持的速度被窗口隐藏")
	}
	if _, err := plan.window(-1); err == nil {
		t.Fatal("接受负位置")
	}
	if _, err := plan.window(1000); err == nil {
		t.Fatal("接受末端外位置")
	}
	caps.WindowedGraph.Supported = false
	if err := ValidateProject(imageCuts(t, 129), caps); err == nil {
		t.Fatal("旧worker静默接受超128片段")
	}
}

func TestGraphPlanIntervalTreeMatchesBruteForceAndHiddenGap(t *testing.T) {
	p := imageCuts(t, 0)
	p.Tracks = nil
	rng := rand.New(rand.NewSource(91))
	for track := 0; track < 20; track++ {
		id := fmt.Sprintf("t%d", track)
		hidden := track%5 == 0
		p.Tracks = append(p.Tracks, editor.Track{ID: id, Name: id, Kind: "video", Hidden: hidden})
		at := int64(rng.Intn(10))
		for j := 0; j < 20; j++ {
			clip := editor.DefaultClip(fmt.Sprintf("%s-%d", id, j), id, "image")
			clip.AssetID = p.Assets[0].ID
			clip.StartFrame, clip.DurationFrames = at, int64(1+rng.Intn(25))
			p.Clips = append(p.Clips, clip)
			at += clip.DurationFrames + int64(rng.Intn(8))
		}
	}
	plan, err := prepareGraphPlan(p, graphTestCaps(), 32, 0)
	if err != nil {
		t.Fatal(err)
	}
	for at := int64(0); at < plan.duration; at += 3 {
		window, err := plan.window(at)
		if err != nil {
			t.Fatal(err)
		}
		want, got := []string{}, []string{}
		for _, clip := range p.Clips {
			hidden := false
			for _, track := range p.Tracks {
				if track.ID == clip.TrackID {
					hidden = track.Hidden
					break
				}
			}
			if !hidden && clip.StartFrame < window.to && clip.StartFrame+clip.DurationFrames > window.from {
				want = append(want, clip.ID)
			}
		}
		for _, clip := range window.clips {
			got = append(got, clip.ID)
		}
		sort.Strings(want)
		sort.Strings(got)
		if !reflect.DeepEqual(want, got) {
			t.Fatal("索引遗漏或多选", at, window.from, window.to, want, got)
		}
	}
	gap := imageCuts(t, 1)
	gap.Clips[0].StartFrame = 600
	plan, err = prepareGraphPlan(gap, graphTestCaps(), 128, 0)
	if err != nil {
		t.Fatal(err)
	}
	window, err := plan.window(0)
	if err != nil || len(window.clips) != 0 || window.to != 600 {
		t.Fatal("空隙没有保留", window, err)
	}
}

func TestGraphPlanOverlapBudgetAndGlobalGraphExtent(t *testing.T) {
	p := imageCuts(t, 1)
	for i := 0; i < 128; i++ {
		id := fmt.Sprintf("layer%d", i)
		p.Tracks = append(p.Tracks, editor.Track{ID: id, Name: id, Kind: "video"})
		clip := p.Clips[0]
		clip.ID, clip.TrackID = id, id
		p.Clips = append(p.Clips, clip)
	}
	if _, err := prepareGraphPlan(p, graphTestCaps(), 128, 0); err == nil {
		t.Fatal("同时129个片段绕过资源上限")
	}
	p = imageCuts(t, 1000)
	plan, err := prepareGraphPlan(p, graphTestCaps(), 128, 0)
	if err != nil {
		t.Fatal(err)
	}
	window, _ := plan.window(999)
	xml, duration, err := compileSelection(plan.project, window.clips, graphTestCaps(), plan.duration, false)
	if err != nil || duration != 1000 || !strings.Contains(xml, `out="999"`) || !strings.Contains(xml, `<blank length="999"/>`) {
		t.Fatal("最后窗口重锚了全局时间", duration, err)
	}
}

func TestGraphWindowReplacementFailureStopsWorker(t *testing.T) {
	path, _ := fixtureExecutable(t)
	s, err := Start(context.Background(), Config{WorkerPath: path, WindowClipLimit: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.capabilities = graphTestCaps()
	if _, err := s.Load(context.Background(), imageCuts(t, 20)); err != nil {
		t.Fatal(err)
	}
	// 协议夹具不支持load_window；拒绝后不能继续在旧图中拉取别的位置。
	if _, err := s.Seek(context.Background(), 10); err == nil {
		t.Fatal("不支持的替换被当作成功")
	}
	select {
	case <-s.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("图替换失败未停止worker")
	}
	if _, err := s.Seek(context.Background(), 0); err == nil {
		t.Fatal("失败后继续使用不确定会话")
	}
}

func TestGraphPlanIncludesSubframeAudioBeforeAndAfterDisplayRange(t *testing.T) {
	p := imageCuts(t, 1)
	p.Settings.FPSNum, p.Settings.FPSDen = 30, 1
	p.Clips[0].DurationFrames = 8
	for i := range p.Tracks {
		if p.Tracks[i].ID == "track-video-1" {
			p.Tracks[i].Hidden = true
		}
	}
	asset := tone(t)
	p.Assets = append(p.Assets, asset)
	clip := editor.DefaultClip("carry", "track-audio-1", "audio")
	clip.AssetID = asset.ID
	clip.StartFrame, clip.DurationFrames, clip.SourceInUs = 5, 1, 1000
	clip.AudioRange = &editor.AudioRange{Unit: "us", Start: 1000, End: 34334, OffsetDen: 1}
	p.Clips = append(p.Clips, clip)
	plan, err := prepareGraphPlan(p, graphTestCaps(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	window, err := plan.window(6)
	if err != nil || len(window.clips) != 1 || window.clips[0].ID != "carry" {
		t.Fatal("丢失显示帧之后的真实声音", window, err)
	}
	p.Clips[1].AudioRange = &editor.AudioRange{Unit: "us", Start: 1000, End: 34333, OffsetNum: -1, OffsetDen: 2}
	plan, err = prepareGraphPlan(p, graphTestCaps(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	window, err = plan.window(4)
	if err != nil || len(window.clips) != 1 {
		t.Fatal("丢失负子帧偏移的真实声音", window, err)
	}
}
