package editor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
)

type captionInheritance struct {
	parents    map[string]string
	templates  []Clip
	exclusions map[string][]CaptionExclusion
}
type clipboardTranscript struct{ ID, AssetID, Fingerprint string }
type clipboardExclusion struct {
	TranscriptID string
	Value        CaptionExclusion
}

// TimelineClipboard的内容不导出，外部只能查询摘要并通过受控命令粘贴。
type TimelineClipboard struct {
	sequenceID    string
	id, projectID string
	settings      Settings
	clips         []Clip
	assets        []Asset
	transcripts   []clipboardTranscript
	exclusions    []clipboardExclusion
	info          ClipboardInfo
}
type ClipboardInfo struct {
	SequenceID       string `json:"sequenceId"`
	ID               string `json:"id"`
	ProjectID        string `json:"projectId"`
	ClipCount        int    `json:"clipCount"`
	DetachedCaptions int    `json:"detachedCaptions"`
	StartFrame       int64  `json:"startFrame"`
	DurationFrames   int64  `json:"durationFrames"`
}

func (c *TimelineClipboard) Info() ClipboardInfo {
	if c == nil {
		return ClipboardInfo{}
	}
	return c.info
}

func CaptureTimelineClipboard(project Project, ids []string, includeLinked *bool) (*TimelineClipboard, error) {
	if err := ValidateProject(project); err != nil {
		return nil, err
	}
	selected, err := selectionIDs(&project, Command{Type: "copy_clips", ClipIDs: ids, IncludeLinked: includeLinked})
	if err != nil {
		return nil, err
	}
	c := &TimelineClipboard{id: NewID("clipboard"), projectID: project.ID, sequenceID: CurrentSequenceID(project), settings: project.Settings}
	assets := map[string]Asset{}
	needed := map[string]bool{}
	sourceAssets := map[string]bool{}
	for _, a := range project.Assets {
		assets[a.ID] = a
	}
	start, end := int64(math.MaxInt64), int64(0)
	for _, original := range project.Clips {
		if !selected[original.ID] && (original.CaptionSource == nil || !selected[original.CaptionSource.SourceClipID]) {
			continue
		}
		clip := cloneClip(original)
		if includeLinked != nil && !*includeLinked {
			clip.LinkGroup = ""
		}
		if clip.CaptionSource != nil && !selected[clip.CaptionSource.SourceClipID] {
			clip.CaptionSource = nil
			clip.LinkGroup = ""
			c.info.DetachedCaptions++
		}
		if mediaSourceKind(clip) {
			clip, err = CaptureSourcePhase(clip, assets[clip.AssetID], project.Settings)
			if err != nil {
				return nil, err
			}
			sourceAssets[clip.AssetID] = true
		}
		if clip.AssetID != "" {
			needed[clip.AssetID] = true
		}
		if clip.LUT != nil {
			needed[clip.LUT.AssetID] = true
		}
		if clip.TextStyle.FontAssetID != "" {
			needed[clip.TextStyle.FontAssetID] = true
		}
		c.clips = append(c.clips, clip)
		start = min(start, clip.StartFrame)
		end = max(end, clip.StartFrame+clip.DurationFrames)
	}
	for _, a := range project.Assets {
		if needed[a.ID] {
			c.assets = append(c.assets, cloneAsset(a))
		}
	}
	for _, t := range project.Transcripts {
		if !sourceAssets[t.AssetID] || t.CaptionTrackID == "" {
			continue
		}
		c.transcripts = append(c.transcripts, clipboardTranscript{t.ID, t.AssetID, t.SourceFingerprint})
		for _, e := range t.CaptionExclusions {
			if selected[e.SourceClipID] {
				c.exclusions = append(c.exclusions, clipboardExclusion{t.ID, e})
			}
		}
	}
	encoded, err := json.Marshal(struct {
		Clips       []Clip
		Assets      []Asset
		Transcripts []clipboardTranscript
		Exclusions  []clipboardExclusion
	}{c.clips, c.assets, c.transcripts, c.exclusions})
	if err != nil {
		return nil, err
	}
	if len(encoded) > 64<<20 {
		return nil, problem("clipboard_budget", "复制内容超过64MiB，请缩小选区；原剪贴板保留")
	}
	c.info.ID, c.info.ProjectID, c.info.ClipCount = c.id, c.projectID, len(c.clips)
	c.info.SequenceID = c.sequenceID
	c.info.StartFrame, c.info.DurationFrames = start, end-start
	return c, nil
}

// 快照不参与外部JSON；幂等哈希仍包含摘要ID和目标参数。
func ClipboardPasteCommand(snapshot *TimelineClipboard, id string, atFrame int64, trackDelta int) Command {
	return Command{Type: "paste_clips", ClipboardID: id, AtFrame: atFrame, TrackDelta: trackDelta, clipboard: snapshot}
}
func clipboardPastePrefix(id, key string) string {
	sum := sha256.Sum256([]byte(id + "\x00" + key))
	return "clip-paste-" + hex.EncodeToString(sum[:12]) + "-"
}
func ClipboardPasteSelection(project Project, id, key string) []string {
	prefix := clipboardPastePrefix(id, key)
	ids := []string{}
	for _, c := range project.Clips {
		if strings.HasPrefix(c.ID, prefix) || (c.CaptionSource != nil && strings.HasPrefix(c.CaptionSource.SourceClipID, prefix)) {
			ids = append(ids, c.ID)
		}
	}
	return ids
}
func clipboardAssetIdentity(a Asset) Asset {
	a.Path, a.URL, a.ThumbnailURL, a.Name = "", "", "", ""
	a.Missing = false
	return a
}
func pasteClipboard(project *Project, command Command) error {
	c := command.clipboard
	if c == nil || command.ClipboardID != c.id || c.projectID != project.ID {
		return problem("clipboard_stale", "剪贴板已更换或属于其它工程，请核对后重试")
	}
	if c.sequenceID != CurrentSequenceID(*project) {
		return problem("clipboard", "剪贴板来自其它序列，请返回来源序列或重新复制")
	}
	if command.AtFrame < 0 || command.FrameDelta != 0 || len(command.ClipIDs) > 0 || command.ClipID != "" || command.Clip != nil || command.TrackID != "" || command.Track != nil || command.Asset != nil || command.Settings != nil || command.IncludeLinked != nil || command.Transcript != nil || command.TranscriptID != "" || len(command.Words) > 0 || command.TranscriptRevision != 0 || command.CaptionLayout != nil || command.AudioCarrier != nil || command.AudioMode != "" || command.BeforeTrackID != "" || command.Name != "" {
		return problem("clipboard", "粘贴参数无效或夹带无关字段")
	}
	if int64(c.settings.FPSNum)*int64(project.Settings.FPSDen) != int64(project.Settings.FPSNum)*int64(c.settings.FPSDen) {
		return problem("clipboard_clock", "工程帧率已改变，无法按复制时的帧位置粘贴")
	}
	assets := map[string]Asset{}
	tracks := map[string]int{}
	transcripts := map[string]Transcript{}
	for _, a := range project.Assets {
		assets[a.ID] = a
	}
	for i, t := range project.Tracks {
		tracks[t.ID] = i
	}
	for _, t := range project.Transcripts {
		transcripts[t.ID] = t
	}
	for _, a := range c.assets {
		current, ok := assets[a.ID]
		if !ok || !reflect.DeepEqual(clipboardAssetIdentity(a), clipboardAssetIdentity(current)) {
			return problem("clipboard_asset", "复制时引用的素材已移除或身份改变，请先恢复素材")
		}
	}
	for _, proof := range c.transcripts {
		t, ok := transcripts[proof.ID]
		if !ok || t.CaptionTrackID == "" || t.AssetID != proof.AssetID || t.SourceFingerprint != proof.Fingerprint || t.SourceFingerprint != assets[t.AssetID].Fingerprint {
			return problem("clipboard_transcript", "复制时的字幕来源已关闭或替换，请恢复来源后粘贴")
		}
	}
	accepted := map[string]map[string]bool{}
	for _, clip := range c.clips {
		if clip.CaptionSource == nil || (!clip.CaptionSource.TextOverride && !clip.CaptionSource.StyleOverride) {
			continue
		}
		id := clip.CaptionSource.TranscriptID
		if accepted[id] == nil {
			accepted[id] = map[string]bool{}
			for _, w := range transcripts[id].AcceptedWords {
				accepted[id][w.ID] = true
			}
		}
		for _, word := range clip.CaptionSource.WordIDs {
			if !accepted[id][word] {
				return problem("caption_override_conflict", "复制时手改字幕依赖的词条已移除，不能静默丢失文案")
			}
		}
	}
	templates := make([]Clip, 0, len(c.clips))
	aliases := map[string]string{}
	groups := map[string]string{}
	start := int64(math.MaxInt64)
	for _, clip := range c.clips {
		if clip.CaptionSource == nil {
			aliases[clip.ID] = NewID("clipboard-source")
			start = min(start, clip.StartFrame)
		}
	}
	copies := make([]Clip, 0, len(aliases))
	for i, clip := range c.clips {
		template := cloneClip(clip)
		if clip.CaptionSource != nil {
			template.ID = NewID("clipboard-caption")
			template.CaptionSource.SourceClipID = aliases[clip.CaptionSource.SourceClipID]
			templates = append(templates, template)
			continue
		}
		template.ID = aliases[clip.ID]
		templates = append(templates, template)
		origin, ok := tracks[clip.TrackID]
		if !ok || command.TrackDelta < -origin || command.TrackDelta >= len(project.Tracks)-origin {
			return problem("clipboard_track", "复制时的目标轨道已移除或超出范围")
		}
		relative := clip.StartFrame - start
		if command.AtFrame > math.MaxInt64-relative || command.AtFrame+relative > math.MaxInt64-clip.DurationFrames {
			return problem("clipboard_range", "粘贴位置超出时间范围")
		}
		copy := cloneClip(clip)
		copy.ID, copy.StartFrame, copy.TrackID = fmt.Sprintf("%s%x", command.pasteNamespace, i), command.AtFrame+relative, project.Tracks[origin+command.TrackDelta].ID
		_, copy, err := prepareDuplicateClip(project, template, copy)
		if err != nil {
			return err
		}
		copy.LinkGroup = ""
		if clip.LinkGroup != "" {
			if groups[clip.LinkGroup] == "" {
				groups[clip.LinkGroup] = NewID("link")
			}
			copy.LinkGroup = groups[clip.LinkGroup]
		}
		command.inheritance.parents[copy.ID] = template.ID
		copies = append(copies, copy)
	}
	command.inheritance.templates = templates
	command.inheritance.exclusions = map[string][]CaptionExclusion{}
	for _, e := range c.exclusions {
		if alias := aliases[e.Value.SourceClipID]; alias != "" {
			command.inheritance.exclusions[e.TranscriptID] = append(command.inheritance.exclusions[e.TranscriptID], CaptionExclusion{SourceClipID: alias, WordID: e.Value.WordID})
		}
	}
	project.Clips = append(project.Clips, copies...)
	return nil
}
