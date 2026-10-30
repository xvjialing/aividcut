package editor

import (
	"reflect"
	"testing"
)

func TestAddTracksPreservesVisualGroupsAndExistingRelativeOrder(t *testing.T) {
	engine, err := NewEngine(NewProject("轨道顺序"))
	if err != nil {
		t.Fatal(err)
	}
	original := engine.Snapshot().Tracks
	for _, track := range []Track{
		{ID: "video-2", Name: "视频 2", Kind: "video"},
		{ID: "text-2", Name: "文字 2", Kind: "text"},
		{ID: "caption-2", Name: "字幕 2", Kind: "caption"},
		{ID: "audio-2", Name: "音频 2", Kind: "audio"},
		{ID: "caption-3", Name: "字幕 3", Kind: "caption"},
		{ID: "text-3", Name: "文字 3", Kind: "text"},
	} {
		apply(t, engine, Command{Type: "add_track", Track: &track})
	}
	project := engine.Snapshot()
	ids := make([]string, len(project.Tracks))
	for i, track := range project.Tracks {
		ids[i] = track.ID
	}
	expected := []string{"track-caption-1", "caption-2", "caption-3", "track-text-1", "text-2", "text-3", "track-video-1", "video-2", "track-audio-1", "audio-2"}
	if !reflect.DeepEqual(ids, expected) {
		t.Fatalf("新轨道分区错误：%v", ids)
	}
	old := make([]Track, 0, len(original))
	for _, track := range project.Tracks {
		for _, previous := range original {
			if track.ID == previous.ID {
				old = append(old, track)
			}
		}
	}
	if !reflect.DeepEqual(old, original) {
		t.Fatal("添加轨道重排了已有轨道")
	}
}

func TestAddTrackCreatesMissingVisualGroupsBeforeAudio(t *testing.T) {
	project := NewProject("缺少视觉轨道")
	project.Tracks = []Track{{ID: "audio", Name: "声音", Kind: "audio"}}
	engine, err := NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"video", "text", "caption"} {
		track := Track{ID: kind, Name: kind, Kind: kind}
		apply(t, engine, Command{Type: "add_track", Track: &track})
	}
	for i, kind := range []string{"caption", "text", "video", "audio"} {
		if engine.Snapshot().Tracks[i].Kind != kind {
			t.Fatal("新建视觉分区顺序错误")
		}
	}
}
