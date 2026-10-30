package editor

import (
	"reflect"
	"testing"
)

func visualReplacementFixture(t *testing.T) (*Engine, Clip, Clip, Track) {
	t.Helper()
	engine := transcriptFixture(t)
	project := engine.Snapshot()
	asset := Asset{ID: "replacement-image", Name: "替代画面.png", Kind: "image", Path: "/tmp/replacement.png", Width: 1920, Height: 1080, Fingerprint: "sha256:image"}
	project = apply(t, engine, Command{Type: "add_asset", Asset: &asset})
	before := project.Clips[clipIndex(&project, "speech-clip")]
	after := before
	after.Kind, after.AssetID, after.SourceInUs, after.SpeedNum, after.SpeedDen, after.Muted, after.LinkGroup = "image", asset.ID, 0, 1, 1, true, "replacement-av"
	track := Track{ID: "real-original-audio", Name: "真实原声", Kind: "audio"}
	carrier := DefaultClip("original-audio", track.ID, "audio")
	carrier.AssetID, carrier.StartFrame, carrier.DurationFrames, carrier.SourceInUs, carrier.SpeedNum, carrier.SpeedDen, carrier.Volume, carrier.LinkGroup = before.AssetID, before.StartFrame, before.DurationFrames, before.SourceInUs, before.SpeedNum, before.SpeedDen, before.Volume, after.LinkGroup
	return engine, after, carrier, track
}

func TestAudioClipCanUseRealVideoAudioStreamButNeverSilentVideo(t *testing.T) {
	engine := fixture(t)
	project := engine.Snapshot()
	source := project.Clips[0]
	audio := DefaultClip("video-audio", "track-audio-1", "audio")
	audio.AssetID, audio.DurationFrames = "video", source.DurationFrames
	apply(t, engine, Command{Type: "add_clip", Clip: &audio})
	project = engine.Snapshot()
	project.Assets[0].HasAudio = false
	if err := ValidateProject(project); err == nil {
		t.Fatal("无音频流的视频不能假装成为audio源")
	}
}

func TestReplaceVisualRejectsForgedCarrierAndChangedShotGeometryAtomically(t *testing.T) {
	for _, scenario := range []string{"asset", "source", "speed", "volume", "duration", "start", "track", "missing-carrier", "unmuted-old-speech"} {
		t.Run(scenario, func(t *testing.T) {
			engine, after, carrier, track := visualReplacementFixture(t)
			before := engine.Snapshot()
			mode := "keep_original"
			audio := &carrier
			switch scenario {
			case "asset":
				carrier.AssetID = "replacement-image"
			case "source":
				carrier.SourceInUs++
			case "speed":
				carrier.SpeedNum++
			case "volume":
				carrier.Volume = 0.1
			case "duration":
				after.DurationFrames++
			case "start":
				after.StartFrame++
			case "track":
				after.TrackID = "track-audio-1"
			case "missing-carrier":
				audio = nil
			case "unmuted-old-speech":
				mode = "mute"
			}
			if audio == nil {
				track = Track{}
			}
			var trackPointer *Track
			if track.ID != "" {
				trackPointer = &track
			}
			_, err := engine.Apply(EditRequest{BaseRevision: before.Revision, IdempotencyKey: "invalid-replacement", Commands: []Command{{Type: "update_project", Name: "不得部分保留"}, {Type: "replace_visual", Clip: &after, AudioCarrier: audio, AudioMode: mode, Track: trackPointer}}})
			if err == nil || !reflect.DeepEqual(before, engine.Snapshot()) {
				t.Fatal("伪造承载或视觉几何变化没有整批拒绝")
			}
		})
	}
}

func TestAudioCarrierFieldsCannotHideInsideOrdinaryCommands(t *testing.T) {
	engine, after, carrier, _ := visualReplacementFixture(t)
	before := engine.Snapshot()
	_, err := engine.Apply(EditRequest{BaseRevision: before.Revision, IdempotencyKey: "hidden-carrier", Commands: []Command{{Type: "update_clip", Clip: &after, AudioCarrier: &carrier, AudioMode: "keep_original"}}})
	assertCode(t, err, "validation")
}
