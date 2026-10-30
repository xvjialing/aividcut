package editor

import (
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
)

func fixture(t *testing.T) *Engine {
	t.Helper()
	project := NewProject("演示工程")
	project.Assets = []Asset{
		{ID: "video", Name: "口播.mp4", Kind: "video", Path: "/tmp/口播.mp4", DurationUs: 20_000_000, Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, HasAudio: true},
		{ID: "audio", Name: "口播.wav", Kind: "audio", Path: "/tmp/口播.wav", DurationUs: 20_000_000, HasAudio: true},
		{ID: "lut", Name: "风格.cube", Kind: "lut", Path: "/tmp/风格.cube"},
	}
	video := DefaultClip("video-clip", "track-video-1", "video")
	video.AssetID, video.Name, video.DurationFrames = "video", "口播", 300
	project.Clips = []Clip{video}
	engine, err := NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func apply(t *testing.T, engine *Engine, commands ...Command) Project {
	t.Helper()
	result, err := engine.Apply(EditRequest{BaseRevision: engine.Snapshot().Revision, IdempotencyKey: NewID("test"), Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("期望错误 %s，实际为 %v", code, err)
	}
}

func TestNewProjectAndSnapshotIsolation(t *testing.T) {
	project := NewProject("  ")
	if len(project.Tracks) != 4 || project.Name != "未命名工程" || project.Assets == nil || project.Clips == nil {
		t.Fatalf("默认工程不完整：%+v", project)
	}
	for i, kind := range []string{"caption", "text", "video", "audio"} {
		if project.Tracks[i].Kind != kind {
			t.Fatal("字幕、文字和视频默认叠放顺序错误")
		}
	}
	engine := fixture(t)
	snapshot := engine.Snapshot()
	snapshot.Assets[0].Name = "外部修改"
	snapshot.Clips[0].Name = "外部修改"
	if engine.Snapshot().Assets[0].Name == "外部修改" || engine.Snapshot().Clips[0].Name == "外部修改" {
		t.Fatal("读模型可从外部修改核心")
	}
	clip := engine.Snapshot().Clips[0]
	clip.LUT = &LUT{AssetID: "lut", Intensity: .4}
	apply(t, engine, Command{Type: "update_clip", Clip: &clip})
	clip.LUT.Intensity = .9
	read := engine.Snapshot()
	read.Clips[0].LUT.Intensity = .7
	if engine.Snapshot().Clips[0].LUT.Intensity != .4 {
		t.Fatal("LUT 指针越过了快照边界")
	}
}

func TestSplitMoveTrimUndoAndRedo(t *testing.T) {
	engine := fixture(t)
	project := apply(t, engine, Command{Type: "split_clip", ClipID: "video-clip", AtFrame: 120})
	if len(project.Clips) != 2 || project.Clips[0].DurationFrames != 120 || project.Clips[1].StartFrame != 120 || project.Clips[1].SourceInUs != 4_000_000 || project.Clips[1].DurationFrames != 180 {
		t.Fatalf("分割源映射错误：%+v", project.Clips)
	}
	right := project.Clips[1]
	right.StartFrame, right.DurationFrames, right.SourceInUs = 180, 90, 5_000_000
	project = apply(t, engine, Command{Type: "update_clip", Clip: &right})
	if DurationFrames(project) != 270 || !engine.CanUndo() {
		t.Fatal("移动与修剪未更新工程")
	}
	undone, err := engine.Undo()
	if err != nil || undone.Revision != 3 || undone.Clips[1].StartFrame != 120 || !engine.CanRedo() {
		t.Fatalf("撤销失败：%+v, %v", undone, err)
	}
	redone, err := engine.Redo()
	if err != nil || redone.Revision != 4 || redone.Clips[1].StartFrame != 180 {
		t.Fatalf("重做失败：%+v, %v", redone, err)
	}
	_, _ = engine.Undo()
	apply(t, engine, Command{Type: "update_project", Name: "另一个方向"})
	if engine.CanRedo() {
		t.Fatal("新操作后仍保留过期重做路径")
	}
}

func TestFailedBatchIsAtomicAndDoesNotConsumeIdempotency(t *testing.T) {
	engine := fixture(t)
	before := engine.Snapshot()
	request := EditRequest{BaseRevision: before.Revision, IdempotencyKey: "atomic", Commands: []Command{
		{Type: "update_project", Name: "不应保存"},
		{Type: "split_clip", ClipID: "video-clip", AtFrame: 0},
	}}
	_, err := engine.Apply(request)
	assertCode(t, err, "invalid_split")
	if !reflect.DeepEqual(before, engine.Snapshot()) || engine.CanUndo() {
		t.Fatal("失败批次修改了工程或历史")
	}
	request.Commands = []Command{{Type: "update_project", Name: "修正后的请求"}}
	if _, err := engine.Apply(request); err != nil {
		t.Fatalf("失败请求占用了幂等标识：%v", err)
	}
}

func TestReplaceSourceKeepsClipIdentityAndChecksNewBounds(t *testing.T) {
	engine := fixture(t)
	image := Asset{ID: "image", Name: "补充画面.png", Kind: "image", Path: "/tmp/补充画面.png", Width: 1920, Height: 1080}
	clip := engine.Snapshot().Clips[0]
	clip.AssetID, clip.Kind, clip.Name = image.ID, "image", image.Name
	project := apply(t, engine, Command{Type: "add_asset", Asset: &image}, Command{Type: "update_clip", Clip: &clip})
	if len(project.Clips) != 1 || project.Clips[0].ID != "video-clip" || project.Clips[0].DurationFrames != 300 || project.Clips[0].Kind != "image" {
		t.Fatal("替换素材没有保持片段身份、时长与位置")
	}
	short := Asset{ID: "short", Name: "短镜头.mp4", Kind: "video", Path: "/tmp/短镜头.mp4", DurationUs: 1_000_000}
	clip.AssetID, clip.Kind = short.ID, "video"
	_, err := engine.Apply(EditRequest{BaseRevision: project.Revision, IdempotencyKey: "replace-short", Commands: []Command{{Type: "add_asset", Asset: &short}, {Type: "update_clip", Clip: &clip}}})
	assertCode(t, err, "source_out_of_range")
	if len(engine.Snapshot().Assets) != len(project.Assets) || engine.Snapshot().Clips[0].Kind != "image" {
		t.Fatal("失败的素材替换部分写入了工程")
	}
}

func TestIdempotencyAndStaleRevision(t *testing.T) {
	engine := fixture(t)
	request := EditRequest{BaseRevision: 0, IdempotencyKey: "split-once", Commands: []Command{{Type: "split_clip", ClipID: "video-clip", AtFrame: 120}}}
	first, err := engine.Apply(request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Apply(request)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("重试没有返回幂等结果：%v", err)
	}
	request.Commands[0].AtFrame = 60
	_, err = engine.Apply(request)
	assertCode(t, err, "idempotency_conflict")
	_, err = engine.Apply(EditRequest{BaseRevision: 0, IdempotencyKey: "stale", Commands: []Command{{Type: "update_project", Name: "过时修改"}}})
	assertCode(t, err, "revision_conflict")
	apply(t, engine, Command{Type: "update_project", Name: "最新工程"})
	request.Commands[0].AtFrame = 120
	latest, err := engine.Apply(request)
	if err != nil || latest.Name != "最新工程" || latest.Revision != 2 || len(latest.Clips) != 2 {
		t.Fatalf("幂等旧响应导致读模型倒退：%+v, %v", latest, err)
	}
}

func TestLockOverlapSourceAndTypeChecks(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Engine) Command
		code string
	}{
		{"越过素材末尾", func(e *Engine) Command {
			c := e.Snapshot().Clips[0]
			c.SourceInUs = 19_000_000
			return Command{Type: "update_clip", Clip: &c}
		}, "source_out_of_range"},
		{"同轨重叠", func(e *Engine) Command {
			c := e.Snapshot().Clips[0]
			c.ID = "second"
			c.StartFrame = 10
			return Command{Type: "add_clip", Clip: &c}
		}, "overlap"},
		{"放到不兼容轨道", func(e *Engine) Command {
			c := e.Snapshot().Clips[0]
			c.TrackID = "track-audio-1"
			return Command{Type: "update_clip", Clip: &c}
		}, "validation"},
		{"删除非空轨道", func(e *Engine) Command { return Command{Type: "delete_track", TrackID: "track-video-1"} }, "track_not_empty"},
		{"无效速度", func(e *Engine) Command {
			c := e.Snapshot().Clips[0]
			c.SpeedDen = 0
			return Command{Type: "update_clip", Clip: &c}
		}, "validation"},
		{"非法数值", func(e *Engine) Command {
			c := e.Snapshot().Clips[0]
			c.Volume = math.NaN()
			return Command{Type: "update_clip", Clip: &c}
		}, "validation"},
		{"不支持命令", func(e *Engine) Command { return Command{Type: "write_arbitrary_file"} }, "unknown_command"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := fixture(t)
			before := engine.Snapshot()
			_, err := engine.Apply(EditRequest{BaseRevision: 0, IdempotencyKey: "invalid", Commands: []Command{test.edit(engine)}})
			assertCode(t, err, test.code)
			if !reflect.DeepEqual(before, engine.Snapshot()) {
				t.Fatal("非法编辑改变了工程")
			}
		})
	}
	engine := fixture(t)
	track := engine.Snapshot().Tracks[2]
	track.Locked = true
	apply(t, engine, Command{Type: "update_track", Track: &track})
	_, err := engine.Apply(EditRequest{BaseRevision: 1, IdempotencyKey: "locked", Commands: []Command{{Type: "delete_clip", ClipID: "video-clip"}}})
	assertCode(t, err, "track_locked")
	track.Locked = false
	apply(t, engine, Command{Type: "update_track", Track: &track})
	apply(t, engine, Command{Type: "delete_clip", ClipID: "video-clip"})
}

func TestLinkedSplitAndMoveAreAtomic(t *testing.T) {
	engine := fixture(t)
	video := engine.Snapshot().Clips[0]
	video.LinkGroup = "take-1"
	audio := DefaultClip("audio-clip", "track-audio-1", "audio")
	audio.AssetID, audio.Name, audio.DurationFrames, audio.LinkGroup = "audio", "单独录音", 300, "take-1"
	apply(t, engine, Command{Type: "update_clip", Clip: &video}, Command{Type: "add_clip", Clip: &audio})
	project := apply(t, engine, Command{Type: "split_clip", ClipID: "video-clip", AtFrame: 90})
	if len(project.Clips) != 4 || project.Clips[2].LinkGroup == "take-1" || project.Clips[2].LinkGroup == "" || project.Clips[2].LinkGroup != project.Clips[3].LinkGroup {
		t.Fatalf("分割后的链接关系无效：%+v", project.Clips)
	}
	rightVideo := project.Clips[2]
	rightVideo.StartFrame += 30
	project = apply(t, engine, Command{Type: "update_clip", Clip: &rightVideo})
	if project.Clips[3].StartFrame != rightVideo.StartFrame {
		t.Fatal("关联声音没有跟随移动")
	}
	audioTrack := project.Tracks[3]
	audioTrack.Locked = true
	project = apply(t, engine, Command{Type: "update_track", Track: &audioTrack})
	before := engine.Snapshot()
	_, err := engine.Apply(EditRequest{BaseRevision: project.Revision, IdempotencyKey: "locked-linked", Commands: []Command{{Type: "delete_clip", ClipID: rightVideo.ID}}})
	assertCode(t, err, "track_locked")
	if !reflect.DeepEqual(before, engine.Snapshot()) {
		t.Fatal("关联轨道锁定时发生部分删除")
	}
}

func TestFrameRateCannotChangeWithClips(t *testing.T) {
	engine := fixture(t)
	settings := engine.Snapshot().Settings
	settings.FPSNum, settings.FPSDen = 30000, 1001
	_, err := engine.Apply(EditRequest{BaseRevision: 0, IdempotencyKey: "rate", Commands: []Command{{Type: "update_project", Settings: &settings}}})
	assertCode(t, err, "validation")
	project := NewProject("空工程")
	empty, _ := NewEngine(project)
	apply(t, empty, Command{Type: "update_project", Settings: &settings})
	equivalent := engine.Snapshot().Settings
	equivalent.FPSNum, equivalent.FPSDen = 60, 2
	apply(t, engine, Command{Type: "update_project", Settings: &equivalent})
}

func TestLinkedDifferentSourceRatesPreserveTimelineRelationship(t *testing.T) {
	engine := fixture(t)
	video := engine.Snapshot().Clips[0]
	video.LinkGroup = "linked"
	audio := DefaultClip("linked-audio", "track-audio-1", "audio")
	audio.AssetID, audio.Name, audio.DurationFrames, audio.LinkGroup = "audio", "半速声音", 300, "linked"
	audio.SpeedNum, audio.SpeedDen = 1, 2
	apply(t, engine, Command{Type: "update_clip", Clip: &video}, Command{Type: "add_clip", Clip: &audio})
	video.StartFrame, video.SourceInUs, video.DurationFrames = 30, 1_000_000, 270
	project := apply(t, engine, Command{Type: "update_clip", Clip: &video})
	if project.Clips[1].StartFrame != 30 || project.Clips[1].DurationFrames != 270 || project.Clips[1].SourceInUs != 500_000 {
		t.Fatalf("不同源速度的关联修剪映射错误：%+v", project.Clips[1])
	}
	video.SpeedNum, video.SpeedDen, video.DurationFrames = 2, 1, 135
	project = apply(t, engine, Command{Type: "update_clip", Clip: &video})
	if project.Clips[1].SpeedNum != 1 || project.Clips[1].SpeedDen != 1 {
		t.Fatalf("关联片段没有按比例变速：%+v", project.Clips[1])
	}
}

func TestLockedLinkedUpdatesAndSplitsCannotPartiallyCommit(t *testing.T) {
	engine := fixture(t)
	video := engine.Snapshot().Clips[0]
	video.LinkGroup = "linked"
	audio := DefaultClip("linked-audio", "track-audio-1", "audio")
	audio.AssetID, audio.Name, audio.DurationFrames, audio.LinkGroup = "audio", "声音", 300, "linked"
	apply(t, engine, Command{Type: "update_clip", Clip: &video}, Command{Type: "add_clip", Clip: &audio})
	audioTrack := engine.Snapshot().Tracks[3]
	audioTrack.Locked = true
	before := apply(t, engine, Command{Type: "update_track", Track: &audioTrack})
	video.StartFrame = 30
	for _, command := range []Command{{Type: "update_clip", Clip: &video}, {Type: "split_clip", ClipID: "video-clip", AtFrame: 90}} {
		_, err := engine.Apply(EditRequest{BaseRevision: before.Revision, IdempotencyKey: NewID("locked"), Commands: []Command{command}})
		assertCode(t, err, "track_locked")
		if !reflect.DeepEqual(before, engine.Snapshot()) {
			t.Fatal("关联轨道锁定时发生部分更新或分割")
		}
	}
	includeLinked := false
	after := apply(t, engine, Command{Type: "split_clip", ClipID: "video-clip", AtFrame: 90, IncludeLinked: &includeLinked})
	if len(after.Clips) != 3 || after.Clips[0].LinkGroup != "" || after.Clips[2].LinkGroup != "" {
		t.Fatal("单独分割没有解除被分割片段的旧链接")
	}
}

func TestConcurrentCommandsDoNotOverwriteEachOther(t *testing.T) {
	engine := fixture(t)
	var wg sync.WaitGroup
	var succeeded, conflicted int
	var lock sync.Mutex
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := engine.Apply(EditRequest{BaseRevision: 0, IdempotencyKey: NewID("concurrent"), Commands: []Command{{Type: "update_project", Name: "并发修改"}}})
			lock.Lock()
			defer lock.Unlock()
			if err == nil {
				succeeded++
			} else {
				var typed *Error
				if errors.As(err, &typed) && typed.Code == "revision_conflict" {
					conflicted++
				}
			}
		}()
	}
	wg.Wait()
	if succeeded != 1 || conflicted != 9 || engine.Snapshot().Revision != 1 {
		t.Fatalf("并发请求覆盖了状态：成功 %d，冲突 %d", succeeded, conflicted)
	}
}

func TestHistoryHasBoundAndRevisionNeverRewinds(t *testing.T) {
	engine := fixture(t)
	for range historyLimit + 10 {
		apply(t, engine, Command{Type: "update_project", Name: NewID("版本")})
	}
	previous := engine.Snapshot().Revision
	for range historyLimit {
		project, err := engine.Undo()
		if err != nil || project.Revision != previous+1 {
			t.Fatal("撤销使修订号倒退或历史损坏")
		}
		previous = project.Revision
	}
	if engine.CanUndo() {
		t.Fatal("历史超出有界存储范围")
	}
}
