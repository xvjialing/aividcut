package editor

import (
	"fmt"
	"reflect"
	"testing"
)

func TestSequenceCapacityAndLegacyFieldGuard(t *testing.T) {
	for version := 1; version <= 4; version++ {
		p := NewProject("旧工程")
		p.SchemaVersion = version
		upgraded, err := UpgradeProject(p)
		if err != nil || upgraded.SchemaVersion != SchemaVersion {
			t.Fatal(version, err)
		}
		p.SequenceID, p.SequenceName = "sequence-main", "主时间线"
		if _, err = UpgradeProject(p); err == nil {
			t.Fatal("旧格式不能夹带序列字段", version)
		}
	}
	e, _ := NewEngine(NewProject("容量"))
	for i := 1; i < MaxSequences; i++ {
		apply(t, e, Command{Type: "create_sequence", SequenceID: fmt.Sprintf("s%d", i), Name: fmt.Sprintf("序列%d", i)})
	}
	before := e.Snapshot()
	if len(SequenceSummaries(before)) != MaxSequences {
		t.Fatal("未达到声明容量")
	}
	if _, err := e.Apply(EditRequest{BaseRevision: before.Revision, IdempotencyKey: "overflow", Commands: []Command{{Type: "create_sequence", SequenceID: "overflow", Name: "超出"}}}); err == nil {
		t.Fatal("超过32应拒绝")
	}
	if !reflect.DeepEqual(before, e.Snapshot()) {
		t.Fatal("超限失败不能部分修改")
	}
}

func TestSequencesDuplicateIndependentWordsAndNavigationHistory(t *testing.T) {
	e := transcriptFixture(t)
	original := e.Snapshot()
	undoCount := len(e.undo)
	copy := apply(t, e, Command{Type: "duplicate_sequence", SequenceID: "copy", SourceSequenceID: MainSequenceID, Name: "独立副本"})
	if CurrentSequenceID(copy) != "copy" || len(copy.OtherSequences) != 1 || !reflect.DeepEqual(copy.Clips, original.Clips) || !reflect.DeepEqual(copy.Transcripts, original.Transcripts) {
		t.Fatal("复制没有完整保持绑定")
	}
	words := append([]SourceWord{}, copy.Transcripts[0].AcceptedWords...)
	words[2].Text = "副本改词"
	copy = apply(t, e, Command{Type: "correct_transcript", TranscriptID: copy.Transcripts[0].ID, TranscriptRevision: copy.Transcripts[0].Revision, Words: words})
	if copy.OtherSequences[0].Transcripts[0].AcceptedWords[2].Text == "副本改词" {
		t.Fatal("改写泄漏到其它序列")
	}
	count := len(e.undo)
	nav, err := e.ActivateSequence(MainSequenceID)
	if err != nil || len(e.undo) != count || CurrentSequenceID(nav) != MainSequenceID {
		t.Fatal("切换不应创建Undo", err)
	}
	if !reflect.DeepEqual(nav.Clips, original.Clips) || !reflect.DeepEqual(nav.Transcripts, original.Transcripts) {
		t.Fatal("原序列发生改变")
	}
	undo, err := e.Undo()
	if err != nil || CurrentSequenceID(undo) != "copy" || undo.Transcripts[0].AcceptedWords[2].Text == "副本改词" {
		t.Fatal("Undo应回到被编辑序列", err)
	}
	if _, err = e.ActivateSequence(MainSequenceID); err != nil {
		t.Fatal(err)
	}
	redo, err := e.Redo()
	if err != nil || CurrentSequenceID(redo) != "copy" || redo.Transcripts[0].AcceptedWords[2].Text != "副本改词" {
		t.Fatal("导航不能清掉Redo", err)
	}
	if len(e.undo) != undoCount+2 {
		t.Fatal("复制与改词应各一条历史")
	}
	mutated := e.Snapshot()
	mutated.OtherSequences[0].Transcripts[0].RawWords[0].Text = "外部修改"
	if e.Snapshot().OtherSequences[0].Transcripts[0].RawWords[0].Text == "外部修改" {
		t.Fatal("快照不是深拷贝")
	}
}
func TestSequenceCreateSettingsDeleteLocksAndFullValidation(t *testing.T) {
	e, _ := NewEngine(NewProject("多规格"))
	p := apply(t, e, Command{Type: "create_sequence", SequenceID: "portrait", Name: "竖屏", Settings: &Settings{Width: 1080, Height: 1920, FPSNum: 30000, FPSDen: 1001, SampleRate: 48000}})
	if p.Settings.Width != 1080 || p.Settings.FPSDen != 1001 || len(p.Clips) != 0 || len(p.Assets) != 0 || len(p.OtherSequences) != 1 {
		t.Fatal(p)
	}
	bad := CloneProject(p)
	bad.OtherSequences[0].Tracks = nil
	if ValidateProject(bad) == nil {
		t.Fatal("必须校验非当前序列")
	}
	if _, err := e.Apply(EditRequest{BaseRevision: p.Revision, IdempotencyKey: "same-name", Commands: []Command{{Type: "rename_sequence", SequenceID: "portrait", Name: "主时间线"}}}); err == nil {
		t.Fatal("重名应拒绝")
	}
	track := p.Tracks[0]
	track.Locked = true
	p = apply(t, e, Command{Type: "update_track", Track: &track})
	if _, err := e.Apply(EditRequest{BaseRevision: p.Revision, IdempotencyKey: "locked-delete", Commands: []Command{{Type: "delete_sequence", SequenceID: "portrait"}}}); err == nil {
		t.Fatal("删除不得越过锁轨")
	}
	track.Locked = false
	p = apply(t, e, Command{Type: "update_track", Track: &track})
	p = apply(t, e, Command{Type: "delete_sequence", SequenceID: "portrait"})
	if CurrentSequenceID(p) != MainSequenceID || len(p.OtherSequences) != 0 {
		t.Fatal("删除当前应回到剩余项")
	}
	if _, err := e.Apply(EditRequest{BaseRevision: p.Revision, IdempotencyKey: "last-delete", Commands: []Command{{Type: "delete_sequence", SequenceID: MainSequenceID}}}); err == nil {
		t.Fatal("不得删最后一个序列")
	}
}
func TestSequenceAssetChangesRespectInactiveLocksAndClipboardIdentity(t *testing.T) {
	e, c := fadeFixture(t)
	p := e.Snapshot()
	a := p.Assets[0]
	a.Fingerprint = "sha256:known-source"
	p = apply(t, e, Command{Type: "update_asset", Asset: &a})
	track := p.Tracks[2]
	track.Locked = true
	p = apply(t, e, Command{Type: "update_track", Track: &track})
	clipboard, err := CaptureTimelineClipboard(p, []string{c.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p = apply(t, e, Command{Type: "create_sequence", SequenceID: "new", Name: "空白"})
	if _, err := e.Apply(EditRequest{BaseRevision: p.Revision, IdempotencyKey: "foreign-paste", Commands: []Command{ClipboardPasteCommand(clipboard, clipboard.Info().ID, 60, 0)}}); err == nil {
		t.Fatal("局部相同ID不能跨序列粘贴")
	}
	a.DurationUs++
	if _, err := e.Apply(EditRequest{BaseRevision: p.Revision, IdempotencyKey: "other-locked", Commands: []Command{{Type: "update_asset", Asset: &a}}}); err == nil {
		t.Fatal("其它序列锁轨必须保护素材元数据")
	}
	a = p.Assets[0]
	a.Path = "/same-content-new-path.wav"
	p = apply(t, e, Command{Type: "update_asset", Asset: &a})
	if p.Assets[0].Path != a.Path {
		t.Fatal("同指纹全工程重定位应可用")
	}
}
