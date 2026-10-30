package inspectordrafts

import (
	"aividcut/internal/editor"
	"testing"
)

func TestAudioFadeDraftsRestoreThroughCore(t *testing.T) {
	p := editor.NewProject("淡化草稿")
	a := editor.Asset{ID: "a", Name: "声音", Kind: "audio", Path: "/fixture.wav", HasAudio: true, DurationUs: 3_000_000}
	p.Assets = []editor.Asset{a}
	c := editor.DefaultClip("c", "track-audio-1", "audio")
	c.AssetID = a.ID
	c.DurationFrames = 60
	p.Clips = []editor.Clip{c}
	rows, commands, err := Preview(p, c.ID, []Field{field("fadeInUs", "0.2", int64(0)), field("fadeOutUs", "0.3", int64(0))}, false)
	if err != nil || len(rows) != 2 || len(commands) != 1 {
		t.Fatal(rows, err)
	}
	e, err := editor.NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	after, err := e.Apply(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "restore-fade", Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	f := after.Clips[0].AudioFade
	if f == nil || f.InUs != 200000 || f.OutUs != 300000 || f.SpanFrames != 60 {
		t.Fatal("淡化草稿没有实际恢复", f)
	}
}
