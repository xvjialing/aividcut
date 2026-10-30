package workflows

import (
	"aividcut/internal/editor"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func allTracksInput() Input {
	return Input{WorkflowID: "talking-head", Selection: []string{"video"}, Parameters: map[string]any{"rippleScope": "all_tracks"}}
}

func TestTalkingHeadAllTracksUsesOriginalDescendingRangesAndAtomicUndo(t *testing.T) {
	p := algorithmProject(t, true)
	for i := range p.Tracks {
		if p.Tracks[i].ID == "music-track" {
			p.Tracks[i].Locked = false
			p.Tracks[i].Muted, p.Tracks[i].Hidden = true, true
		}
	}
	title := editor.DefaultClip("later-title", "track-text-1", "text")
	title.StartFrame, title.DurationFrames, title.Text = 160, 30, "随后标题"
	p.Clips = append(p.Clips, title)
	original := editor.CloneProject(p)
	plan, err := Preview(p, allTracksInput())
	if err != nil {
		t.Fatal(err)
	}
	s := plan.SpeechRippleSummary
	if s == nil || s.CutCount != 2 || s.RemovedFrames != 94 || s.BeforeFrames != 300 || s.AfterFrames != 206 || s.AffectedTracks != 5 || s.TotalAffectedClips != len(plan.AffectedClipIDs) {
		t.Fatal("全轨统计与完整事务不符", s)
	}
	commands := plan.Request.Commands
	if len(commands) != 2 || commands[0].Type != "ripple_delete_range" || commands[0].FromFrame != 98 || commands[0].ToFrame != 142 || commands[1].FromFrame != 20 || commands[1].ToFrame != 70 {
		t.Fatal("多个切口没有按原工程坐标倒序复用核心", commands)
	}
	if !reflect.DeepEqual(p, original) {
		t.Fatal("预览修改了原工程")
	}
	e, after := applyAlgorithm(t, p, plan)
	if clipWithID(after, title.ID).StartFrame != 66 || editor.DurationFrames(after) != 206 {
		t.Fatal("未选标题或配乐没有全轨闭合")
	}
	counts := map[string]int{}
	manual := false
	for _, c := range after.Clips {
		counts[c.TrackID]++
		if c.CaptionSource != nil && c.Text == "人工第二句" {
			manual = c.TextStyle.FontSize == 68 && c.Transform.Y == -123 && c.CaptionSource.TextOverride && c.CaptionSource.StyleOverride
		}
	}
	if counts["music-track"] != 3 || counts["track-video-1"] != 3 || counts["track-audio-1"] != 3 || !manual || !reflect.DeepEqual(p.Transcripts, after.Transcripts) {
		t.Fatal("未保留全部存续音画、原话或手改字幕", counts)
	}
	proof := false
	for _, v := range plan.Evidence {
		proof = proof || v.AssetID == "source-audio" && v.Kind == "speech-ripple-retained-source"
	}
	if !proof || !strings.Contains(strings.Join(plan.Warnings, " "), "隐藏和静音轨") {
		t.Fatal("缺少其它媒体来源或全轨后果")
	}
	again, err := e.Apply(plan.Request)
	if err != nil || !reflect.DeepEqual(again, after) {
		t.Fatal("相同计划重试产生重复删除", err)
	}
	undo, err := e.Undo()
	if err != nil || !reflect.DeepEqual(noRevision(undo), noRevision(p)) {
		t.Fatal("多切口不是一次撤销", err)
	}
	redo, err := e.Redo()
	if err != nil || !reflect.DeepEqual(noRevision(redo), noRevision(after)) {
		t.Fatal("重做未恢复相同切口与标识", err)
	}
}

func TestTalkingHeadAllTracksRejectsRelevantLocksButKeepsDefaultAndEarlierLock(t *testing.T) {
	p := algorithmProject(t, true)
	before := editor.CloneProject(p)
	if _, err := Preview(p, allTracksInput()); err == nil {
		t.Fatal("全轨忽略未选中的锁定配乐")
	}
	if !reflect.DeepEqual(before, p) {
		t.Fatal("拒绝部分写入")
	}
	old, err := Preview(p, Input{WorkflowID: "talking-head", Selection: []string{"video"}})
	if err != nil || old.SpeechRippleSummary != nil || old.SavedFrames != 0 {
		t.Fatal("默认行为被扩大到全轨", err)
	}
	for i := range p.Tracks {
		if p.Tracks[i].ID == "music-track" {
			p.Tracks[i].Locked = false
		}
	}
	p.Tracks = append(p.Tracks, editor.Track{ID: "earlier-locked", Kind: "text", Name: "切口前标题", Locked: true})
	c := editor.DefaultClip("earlier", "earlier-locked", "text")
	c.Text, c.DurationFrames = "保留", 5
	p.Clips = append(p.Clips, c)
	plan, err := Preview(p, allTracksInput())
	if err != nil {
		t.Fatal("未受影响前方锁轨阻断", err)
	}
	_, after := applyAlgorithm(t, p, plan)
	if !reflect.DeepEqual(c, clipWithID(after, c.ID)) {
		t.Fatal("切口前锁轨被改动")
	}
}

func TestTalkingHeadAllTracksMultipleGroupsAndBoundedLargePreview(t *testing.T) {
	p := algorithmProject(t, false)
	other := clipWithID(p, "video")
	other.ID, other.LinkGroup, other.StartFrame = "second", "", 220
	p.Clips = append(p.Clips, other)
	for i := 0; i < 3000; i++ {
		c := editor.DefaultClip(fmt.Sprintf("later-%d", i), "track-text-1", "text")
		c.StartFrame, c.DurationFrames, c.Text = 500+int64(i*3), 2, "后续"
		p.Clips = append(p.Clips, c)
	}
	in := allTracksInput()
	in.Selection = append(in.Selection, other.ID)
	plan, err := Preview(p, in)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SpeechRippleSummary.CutCount != 4 || plan.SavedFrames != 188 || plan.SpeechRippleSummary.TotalAffectedClips <= 3000 || len(plan.Effects) > 200 || len(plan.AffectedClipIDs) != 2000 {
		t.Fatal("多源坐标或完整/展示统计有误", plan.SpeechRippleSummary)
	}
	_, after := applyAlgorithm(t, p, plan)
	if clipWithID(after, "later-2999").StartFrame != 500+2999*3-188 {
		t.Fatal("只处理展示上限内的片段")
	}
}

func TestTalkingHeadAllTracksProtectsJoinedManualCaptionAndUnknownRetainedMedia(t *testing.T) {
	p := algorithmProject(t, false)
	clips := p.Clips[:0]
	for _, c := range p.Clips {
		if c.CaptionSource == nil {
			clips = append(clips, c)
		}
	}
	p.Clips = clips
	p.Transcripts[0].CaptionTrackID = ""
	e, err := editor.NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	p, err = e.Apply(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "joined-captions", Commands: []editor.Command{{Type: "generate_bound_captions", TranscriptID: p.Transcripts[0].ID, TrackID: "track-caption-1", CaptionLayout: &editor.CaptionLayout{Mode: "readable", MaxCharsPerLine: 40, MaxLines: 2, MaxDurationUs: 10_000_000, PauseThresholdUs: 5000_000}}}})
	if err != nil {
		t.Fatal(err)
	}
	manual := editor.Clip{}
	for _, c := range p.Clips {
		if c.CaptionSource != nil && len(c.CaptionSource.WordIDs) > 1 {
			manual = c
			break
		}
	}
	if manual.ID == "" {
		t.Fatal("夹具没有跨切口整句")
	}
	manual.Text = "不能自动拆分的人工整句"
	p, err = e.Apply(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "manual-sentence", Commands: []editor.Command{{Type: "update_clip", Clip: &manual}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Preview(p, allTracksInput()); err == nil {
		t.Fatal("跨切口人工整句被自动拆分")
	}

	p = algorithmProject(t, false)
	p.Assets = append(p.Assets, editor.Asset{ID: "unknown", Name: "待重定位源", Kind: "video", Path: "/offline", DurationUs: 20_000_000})
	p.Tracks = append(p.Tracks, editor.Track{ID: "offline", Kind: "video", Name: "离线占位"})
	c := editor.DefaultClip("offline", "offline", "video")
	c.AssetID, c.DurationFrames = "unknown", 180
	p.Clips = append(p.Clips, c)
	if _, err := Preview(p, allTracksInput()); err == nil {
		t.Fatal("裁切未核对来源的其它媒体")
	}
	p.Clips[len(p.Clips)-1].StartFrame = 200
	if _, err := Preview(p, allTracksInput()); err != nil {
		t.Fatal("纯移动的离线占位被不必要读取", err)
	}
}
