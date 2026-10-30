package captions

import (
	"aividcut/internal/editor"
	"reflect"
	"strings"
	"testing"
)

func TestImportOverlappingAndSameStartCuesAllocatesTracksWithoutLosingText(t *testing.T) {
	project := editor.NewProject("字幕导入")
	before := editor.CloneProject(project)
	cues := []Cue{{0, 1000, "第一句"}, {0, 2000, "第二句\n第二行"}, {1000, 2000, "第三句"}}
	commands, err := ImportCommands(project, "", cues)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(project, before) {
		t.Fatal("导入命令生成修改了原工程")
	}
	engine, _ := editor.NewEngine(project)
	imported, err := engine.Apply(editor.EditRequest{BaseRevision: 0, IdempotencyKey: "captions", Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.Clips) != 3 || len(imported.Tracks) != 5 {
		t.Fatalf("重叠字幕没有分轨：%+v", imported)
	}
	if imported.Tracks[0].Kind != "caption" || imported.Tracks[1].Kind != "caption" || imported.Clips[0].TrackID == imported.Clips[1].TrackID || imported.Clips[1].Transform.Y >= 0 {
		t.Fatal("字幕轨分区或重叠布局错误")
	}
	output, err := ExportProjectSRT(imported, "")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSRT(output)
	if err != nil || !reflect.DeepEqual(parsed, cues) {
		t.Fatalf("工程 SRT 往返丢字：%+v, %v", parsed, err)
	}
	undone, _ := engine.Undo()
	if len(undone.Clips) != 0 || len(undone.Tracks) != 4 {
		t.Fatal("字幕导入未作为一个事务撤销")
	}
}

func TestImportSpecifiedTrackRejectsOverlapAndLock(t *testing.T) {
	project := editor.NewProject("指定轨道")
	overlap := []Cue{{0, 1000, "一"}, {0, 1000, "二"}}
	if _, err := ImportCommands(project, "track-caption-1", overlap); err == nil {
		t.Fatal("指定轨道接受了重叠字幕")
	}
	project.Tracks[0].Locked = true
	if _, err := ImportCommands(project, "track-caption-1", []Cue{{0, 1000, "一"}}); err == nil {
		t.Fatal("锁定轨道接受导入")
	}
	commands, err := ImportCommands(project, "", []Cue{{0, 1000, "一"}})
	if err != nil || len(commands) != 2 || commands[0].Type != "add_track" {
		t.Fatalf("自动导入没有绕开锁定空轨：%v", err)
	}
	if _, err := ImportCommands(project, "track-video-1", []Cue{{0, 1000, "一"}}); err == nil {
		t.Fatal("导入到了视频轨")
	}
}

func TestImportDoesNotMixWithExistingCaptionSourceOrReuseHiddenTrack(t *testing.T) {
	project := editor.NewProject("已有字幕")
	clip := editor.DefaultClip("existing", "track-caption-1", "caption")
	clip.Text = "已有字幕"
	project.Clips = []editor.Clip{clip}
	commands, err := ImportCommands(project, "", []Cue{{0, 1000, "新字幕"}})
	if err != nil || commands[0].Type != "add_track" {
		t.Fatalf("新字幕被混入已有字幕来源：%v", err)
	}
	project.Clips = nil
	project.Tracks[0].Hidden = true
	commands, err = ImportCommands(project, "", []Cue{{0, 1000, "可见字幕"}})
	if err != nil || commands[0].Type != "add_track" {
		t.Fatal("自动导入复用了隐藏轨道")
	}
}

func TestExportCaptionTrackDoesNotIncludeTitlesAndCanSelectSource(t *testing.T) {
	project := editor.NewProject("字幕导出")
	caption := editor.DefaultClip("caption", "track-caption-1", "caption")
	caption.Text = "字幕文本"
	title := editor.DefaultClip("title", "track-text-1", "text")
	title.Text = "标题不应混入"
	project.Clips = []editor.Clip{caption, title}
	project.Tracks[0].Hidden = true
	output, err := ExportProjectSRT(project, "track-caption-1")
	if err != nil || !strings.Contains(output, "字幕文本") || strings.Contains(output, "标题不应混入") {
		t.Fatalf("字幕导出混入标题或隐式丢掉隐藏源：%s %v", output, err)
	}
	if _, err := ExportProjectSRT(project, "track-text-1"); err == nil {
		t.Fatal("接受了非字幕轨道")
	}
	project.Clips = nil
	if _, err := ExportProjectSRT(project, ""); err == nil {
		t.Fatal("没有字幕却声称导出成功")
	}
}
