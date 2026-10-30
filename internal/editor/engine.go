package editor

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const historyLimit = 50
const idempotencyLimit = 1024

var fallbackID atomic.Uint64

func NewID(prefix string) string {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return prefix + "-" + hex.EncodeToString(bytes[:])
	}
	return fmt.Sprintf("%s-%x-%x", prefix, time.Now().UnixNano(), fallbackID.Add(1))
}

func NewProject(name string) Project {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "未命名工程"
	}
	return Project{
		SchemaVersion: SchemaVersion, ID: NewID("project"), Name: name,
		Settings: Settings{Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, SampleRate: 48000},
		Assets:   []Asset{}, Clips: []Clip{},
		Tracks: []Track{
			{ID: "track-caption-1", Name: "字幕 1", Kind: "caption"},
			{ID: "track-text-1", Name: "文字 1", Kind: "text"},
			{ID: "track-video-1", Name: "视频 1", Kind: "video"},
			{ID: "track-audio-1", Name: "音频 1", Kind: "audio"},
		},
	}
}

func NewProjectWithSettings(name string, settings Settings) (Project, error) {
	if err := ValidateSettings(settings); err != nil {
		return Project{}, err
	}
	// 帧率存有理数，不用四舍五入后的小数替代；相同比值统一约分。
	a, b := settings.FPSNum, settings.FPSDen
	for b != 0 {
		a, b = b, a%b
	}
	settings.FPSNum /= a
	settings.FPSDen /= a
	p := NewProject(name)
	p.Settings = settings
	return p, ValidateProject(p)
}

func DefaultClip(id, trackID, kind string) Clip {
	return Clip{ID: id, TrackID: trackID, Kind: kind, Name: "新片段", DurationFrames: 90,
		SpeedNum: 1, SpeedDen: 1, Volume: 1, Transform: Transform{Scale: 1},
		TextStyle: TextStyle{FontSize: 48, Color: "#ffffff", Background: "transparent", Align: "center"}}
}

func CloneProject(project Project) Project {
	project.SequenceOrder = append([]string(nil), project.SequenceOrder...)
	if project.OtherSequences != nil {
		project.OtherSequences = append([]Sequence{}, project.OtherSequences...)
		for i := range project.OtherSequences {
			project.OtherSequences[i] = cloneSequence(project.OtherSequences[i])
		}
	}
	project.Assets = append([]Asset{}, project.Assets...)
	for i := range project.Assets {
		project.Assets[i] = cloneAsset(project.Assets[i])
	}
	project.Tracks = append([]Track{}, project.Tracks...)
	project.Clips = append([]Clip{}, project.Clips...)
	for i := range project.Clips {
		project.Clips[i] = cloneClip(project.Clips[i])
	}
	if project.Transcripts != nil {
		project.Transcripts = append([]Transcript{}, project.Transcripts...)
		for i := range project.Transcripts {
			project.Transcripts[i] = cloneTranscript(project.Transcripts[i])
		}
	}
	if project.VisualAnalyses != nil {
		project.VisualAnalyses = append([]VisualAnalysis{}, project.VisualAnalyses...)
		for i := range project.VisualAnalyses {
			project.VisualAnalyses[i] = CloneVisualAnalysis(project.VisualAnalyses[i])
		}
	}
	return project
}

func cloneClip(clip Clip) Clip {
	clip.AudioFade = CloneAudioFade(clip.AudioFade)
	clip.SourcePhase = CloneSourcePhase(clip.SourcePhase)
	if clip.AudioRange != nil {
		copy := *clip.AudioRange
		clip.AudioRange = &copy
	}
	if clip.LUT != nil {
		value := *clip.LUT
		clip.LUT = &value
	}
	if clip.CaptionSource != nil {
		value := *clip.CaptionSource
		value.WordIDs = append([]string{}, value.WordIDs...)
		clip.CaptionSource = &value
	}
	return clip
}

func cloneAsset(asset Asset) Asset {
	if asset.Font != nil {
		font := *asset.Font
		asset.Font = &font
	}
	if asset.AudioSampleCount != nil {
		copy := *asset.AudioSampleCount
		asset.AudioSampleCount = &copy
	}
	return asset
}

type Engine struct {
	mu          sync.RWMutex
	project     Project
	undo        []Project
	redo        []Project
	idempotency map[string][32]byte
	keyOrder    []string
}

func NewEngine(project Project) (*Engine, error) {
	if project.ActiveSequenceProjection {
		return nil, problem("validation", "不能用当前序列读模型创建完整工程引擎")
	}
	var err error
	project, err = UpgradeProject(project)
	if err != nil {
		return nil, err
	}
	return &Engine{project: CloneProject(project), idempotency: make(map[string][32]byte)}, nil
}

func (engine *Engine) Snapshot() Project {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	return CloneProject(engine.project)
}

// Identity 仅用于轻量范围/修订检查，不构造或暴露工程数组。
func (engine *Engine) Identity() (projectID string, revision int64) {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	return engine.project.ID, engine.project.Revision
}

func (engine *Engine) CanUndo() bool {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	return len(engine.undo) > 0
}

func (engine *Engine) CanRedo() bool {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	return len(engine.redo) > 0
}

func (engine *Engine) Apply(request EditRequest) (Project, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if strings.TrimSpace(request.IdempotencyKey) == "" || len(request.IdempotencyKey) > 256 || len(request.Commands) == 0 {
		return Project{}, problem("validation", "编辑请求需要幂等标识和至少一条命令")
	}
	if len(request.Commands) > 10_000 {
		return Project{}, problem("validation", "单次编辑命令过多，请分批处理")
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return Project{}, problem("validation", "编辑参数不能序列化："+err.Error())
	}
	hash := sha256.Sum256(encoded)
	if previous, exists := engine.idempotency[request.IdempotencyKey]; exists {
		if previous != hash {
			return Project{}, problem("idempotency_conflict", "相同幂等标识不能用于不同的编辑请求")
		}
		// 重试返回当前读模型，避免旧响应把前端倒退到较早的修订。
		return CloneProject(engine.project), nil
	}
	if request.BaseRevision != engine.project.Revision {
		return Project{}, problem("revision_conflict", "工程已发生变化，请刷新后重新执行编辑")
	}
	if engine.project.Revision == math.MaxInt64 {
		return Project{}, problem("validation", "工程修订号超出可表示范围")
	}
	next := CloneProject(engine.project)
	for commandIndex, command := range request.Commands {
		if isSequenceCommand(command.Type) {
			if err := applySequenceCommand(&next, command); err != nil {
				return Project{}, err
			}
			continue
		}
		if command.Type == "accept_visual_analysis" || command.Type == "delete_visual_analysis" {
			if err := applyCommand(&next, command); err != nil {
				return Project{}, err
			}
			continue
		}
		if command.Type == "split_clips" || command.Type == "duplicate_clips" || command.Type == "paste_clips" || command.Type == "ripple_delete_range" {
			command.inheritance = &captionInheritance{parents: map[string]string{}}
		}
		if command.Type == "ripple_delete_range" {
			command.rippleNamespace = fmt.Sprintf("%s/%d/%s/%d", next.ID, request.BaseRevision, request.IdempotencyKey, commandIndex)
		}
		if command.Type == "paste_clips" {
			command.pasteNamespace = clipboardPastePrefix(command.ClipboardID, request.IdempotencyKey)
		}
		var beforeClips []Clip
		previousCaptionTrack := ""
		if len(next.Transcripts) > 0 {
			beforeClips = append([]Clip{}, next.Clips...)
		}
		if command.Type == "generate_bound_captions" {
			if index := transcriptIndex(&next, command.TranscriptID); index >= 0 {
				previousCaptionTrack = next.Transcripts[index].CaptionTrackID
			}
		}
		if err := applyCommand(&next, command); err != nil {
			return Project{}, err
		}
		if err := reconcileCaptions(&next, beforeClips, command, previousCaptionTrack); err != nil {
			return Project{}, err
		}
		if command.Type == "update_asset" {
			for i, s := range next.OtherSequences {
				view := SequenceProject(next, s)
				if err := reconcileCaptions(&view, s.Clips, command, ""); err != nil {
					return Project{}, err
				}
				next.OtherSequences[i] = sequenceFromProject(view)
			}
		}
	}
	refreshVisualAnalyses(&next)
	next.Revision = engine.project.Revision + 1
	if err := ValidateProject(next); err != nil {
		return Project{}, err
	}
	previous := engine.project
	previous.historySequenceID = CurrentSequenceID(next)
	engine.undo = appendHistory(engine.undo, previous)
	engine.redo = nil
	engine.project = next
	engine.idempotency[request.IdempotencyKey] = hash
	engine.keyOrder = append(engine.keyOrder, request.IdempotencyKey)
	if len(engine.keyOrder) > idempotencyLimit {
		delete(engine.idempotency, engine.keyOrder[0])
		engine.keyOrder = engine.keyOrder[1:]
	}
	return CloneProject(next), nil
}

func appendHistory(history []Project, project Project) []Project {
	history = append(history, CloneProject(project))
	if len(history) > historyLimit {
		copy(history, history[len(history)-historyLimit:])
		clear(history[historyLimit:])
		history = history[:historyLimit]
	}
	return history
}

func (engine *Engine) Undo() (Project, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if len(engine.undo) == 0 {
		return Project{}, problem("history_empty", "没有可以撤销的操作")
	}
	if engine.project.Revision == math.MaxInt64 {
		return Project{}, problem("validation", "工程修订号超出可表示范围")
	}
	previous := engine.undo[len(engine.undo)-1]
	redoState := CloneProject(engine.project)
	if previous.historySequenceID != "" {
		if err := activateSequence(&redoState, previous.historySequenceID); err != nil {
			return Project{}, err
		}
	}
	redoState.historySequenceID = CurrentSequenceID(previous)
	engine.undo = engine.undo[:len(engine.undo)-1]
	engine.redo = appendHistory(engine.redo, redoState)
	previous.historySequenceID = ""
	previous.Revision = engine.project.Revision + 1
	engine.project = previous
	return CloneProject(previous), nil
}

func (engine *Engine) Redo() (Project, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if len(engine.redo) == 0 {
		return Project{}, problem("history_empty", "没有可以重做的操作")
	}
	if engine.project.Revision == math.MaxInt64 {
		return Project{}, problem("validation", "工程修订号超出可表示范围")
	}
	next := engine.redo[len(engine.redo)-1]
	beforeState := CloneProject(engine.project)
	if next.historySequenceID != "" {
		if err := activateSequence(&beforeState, next.historySequenceID); err != nil {
			return Project{}, err
		}
	}
	beforeState.historySequenceID = CurrentSequenceID(next)
	engine.redo = engine.redo[:len(engine.redo)-1]
	engine.undo = appendHistory(engine.undo, beforeState)
	next.historySequenceID = ""
	refreshVisualAnalyses(&next)
	next.Revision = engine.project.Revision + 1
	engine.project = next
	return CloneProject(next), nil
}

func trackIndex(project *Project, id string) int {
	for i, track := range project.Tracks {
		if track.ID == id {
			return i
		}
	}
	return -1
}

func clipIndex(project *Project, id string) int {
	for i, clip := range project.Clips {
		if clip.ID == id {
			return i
		}
	}
	return -1
}

func editableTrack(project *Project, id string) error {
	i := trackIndex(project, id)
	if i < 0 {
		return problem("not_found", "目标轨道不存在")
	}
	if project.Tracks[i].Locked {
		return problem("track_locked", "轨道已锁定，请先解锁后编辑")
	}
	return nil
}

func linked(command Command) bool { return command.IncludeLinked == nil || *command.IncludeLinked }

func selectedIDs(project *Project, clip Clip, includeLinked bool) map[string]bool {
	ids := map[string]bool{clip.ID: true}
	if includeLinked && clip.LinkGroup != "" {
		for _, other := range project.Clips {
			if other.LinkGroup == clip.LinkGroup {
				ids[other.ID] = true
			}
		}
	}
	return ids
}

func applyCommand(project *Project, command Command) error {
	if command.SequenceID != "" || command.SourceSequenceID != "" {
		return problem("validation", "序列字段只能通过序列管理命令使用")
	}
	if command.VisualAnalysis != nil && command.Type != "accept_visual_analysis" || command.VisualAnalysisID != "" && command.Type != "delete_visual_analysis" {
		return problem("visual_analysis", "普通编辑不能携带分析写入参数")
	}
	if command.Type == "accept_visual_analysis" || command.Type == "delete_visual_analysis" {
		return applyVisualCommand(project, command)
	}
	if command.Edge != "" && command.Type != "trim_clips" {
		return problem("validation", "边缘字段仅用于明确的整组修剪")
	}
	if command.ClipboardID != "" && command.Type != "paste_clips" {
		return problem("clipboard", "剪贴板标识只用于受控粘贴")
	}
	if command.Type == "paste_clips" {
		return pasteClipboard(project, command)
	}
	if (len(command.ClipIDs) > 0 || command.FrameDelta != 0 || (command.TrackDelta != 0 && command.Type != "move_track")) && command.Type != "move_clips" && command.Type != "delete_clips" && command.Type != "split_clips" && command.Type != "duplicate_clips" && command.Type != "trim_clips" {
		return problem("validation", "片段集合与位移仅用于批量编辑")
	}
	if command.CaptionLayout != nil && command.Type != "generate_bound_captions" {
		return problem("caption_layout", "阅读分句设置仅用于明确的绑定字幕生成命令")
	}
	if (command.AudioCarrier != nil || command.AudioMode != "") && command.Type != "replace_visual" {
		return problem("validation", "声音承载与策略字段仅用于受控镜头替换")
	}
	if command.BeforeTrackID != "" && command.Type != "add_track" {
		return problem("validation", "仅新增轨道命令可以指定插入位置")
	}
	if (command.FromFrame != 0 || command.ToFrame != 0) && command.Type != "ripple_delete_range" {
		return problem("validation", "起止范围字段仅用于明确的全轨波纹删除")
	}
	switch command.Type {
	case "move_track":
		return moveTrack(project, command)
	case "ripple_delete_range":
		return rippleDeleteRange(project, command)
	case "trim_clips":
		return trimClipSelection(project, command)
	case "move_clips", "delete_clips":
		return editClipSelection(project, command)
	case "split_clips", "duplicate_clips":
		return constructClipSelection(project, command)
	case "replace_visual":
		return replaceVisual(project, command)
	case "accept_transcript", "correct_transcript", "generate_bound_captions", "detach_caption":
		return applyTranscriptCommand(project, command)
	case "add_asset":
		if command.Asset == nil {
			return problem("validation", "缺少素材信息")
		}
		project.Assets = append(project.Assets, cloneAsset(*command.Asset))
	case "update_asset":
		if command.Asset == nil {
			return problem("validation", "缺少素材信息")
		}
		index := -1
		for i, asset := range project.Assets {
			if asset.ID == command.Asset.ID {
				index = i
				break
			}
		}
		if index < 0 {
			return problem("not_found", "需要更新的素材不存在，不能改变素材标识")
		}
		before, after := project.Assets[index], cloneAsset(*command.Asset)
		if before.Kind != after.Kind {
			return problem("validation", "素材重定位不能改变原素材类型")
		}
		if !pureAssetRelink(before, after) {
			for _, s := range project.OtherSequences {
				locked := map[string]bool{}
				for _, t := range s.Tracks {
					locked[t.ID] = t.Locked
				}
				for _, c := range s.Clips {
					if locked[c.TrackID] && (c.AssetID == before.ID || c.TextStyle.FontAssetID == before.ID || (c.LUT != nil && c.LUT.AssetID == before.ID)) {
						return problem("track_locked", "其它序列的锁定轨道引用此素材；请先处理对应轨道")
					}
				}
			}
			locked := make(map[string]bool)
			for _, track := range project.Tracks {
				locked[track.ID] = track.Locked
			}
			for _, clip := range project.Clips {
				if locked[clip.TrackID] && (clip.AssetID == before.ID || clip.TextStyle.FontAssetID == before.ID || (clip.LUT != nil && clip.LUT.AssetID == before.ID)) {
					return problem("track_locked", "锁定轨道引用了此素材；只有同指纹且不改变元数据的纯重定位可直接修复，否则请先解锁引用轨道")
				}
			}
		}
		project.Assets[index] = after
	case "add_track":
		if command.Track == nil {
			return problem("validation", "缺少轨道信息")
		}
		if err := validateNewTrackName(command.Track.Name); err != nil {
			return err
		}
		position := trackInsertionIndex(project.Tracks, command.Track.Kind)
		if command.BeforeTrackID != "" {
			position = trackIndex(project, command.BeforeTrackID)
			if position < 0 || project.Tracks[position].Kind != command.Track.Kind {
				return problem("validation", "新增轨道只能插在同类型且存在的目标轨之前")
			}
		}
		project.Tracks = append(project.Tracks, Track{})
		copy(project.Tracks[position+1:], project.Tracks[position:len(project.Tracks)-1])
		project.Tracks[position] = *command.Track
	case "update_track":
		if command.Track == nil {
			return problem("validation", "缺少轨道信息")
		}
		i := trackIndex(project, command.Track.ID)
		if i < 0 {
			return problem("not_found", "目标轨道不存在")
		}
		if project.Tracks[i].Kind != command.Track.Kind {
			return problem("validation", "不能改变已有轨道的类型")
		}
		if command.Track.Name != project.Tracks[i].Name {
			if err := validateNewTrackName(command.Track.Name); err != nil {
				return err
			}
		}
		project.Tracks[i] = *command.Track
	case "delete_track":
		i := trackIndex(project, command.TrackID)
		if i < 0 {
			return problem("not_found", "目标轨道不存在")
		}
		if err := editableTrack(project, command.TrackID); err != nil {
			return err
		}
		for _, clip := range project.Clips {
			if clip.TrackID == command.TrackID {
				return problem("track_not_empty", "轨道仍有片段，不能直接删除")
			}
		}
		project.Tracks = append(project.Tracks[:i], project.Tracks[i+1:]...)
	case "add_clip":
		if command.Clip == nil {
			return problem("validation", "缺少片段信息")
		}
		if err := editableTrack(project, command.Clip.TrackID); err != nil {
			return err
		}
		if command.Clip.CaptionSource != nil {
			return problem("caption_binding", "普通添加片段不能伪造字幕源绑定")
		}
		project.Clips = append(project.Clips, cloneClip(*command.Clip))
	case "duplicate_clip":
		return duplicateClip(project, command)
	case "update_clip":
		return updateClip(project, command)
	case "delete_clip":
		i := clipIndex(project, command.ClipID)
		if i < 0 {
			return problem("not_found", "目标片段不存在")
		}
		ids := selectedIDs(project, project.Clips[i], linked(command))
		for _, clip := range project.Clips {
			if ids[clip.ID] {
				if err := editableTrack(project, clip.TrackID); err != nil {
					return err
				}
				suppressCaption(project, clip)
			}
		}
		kept := project.Clips[:0]
		for _, clip := range project.Clips {
			if !ids[clip.ID] {
				kept = append(kept, clip)
			}
		}
		project.Clips = kept
	case "split_clip":
		return splitClip(project, command)
	case "update_project":
		if strings.TrimSpace(command.Name) == "" && command.Settings == nil {
			return problem("validation", "工程名称不能为空")
		}
		if strings.TrimSpace(command.Name) != "" {
			project.Name = strings.TrimSpace(command.Name)
		}
		if command.Settings != nil {
			if err := ValidateSettings(*command.Settings); err != nil {
				return err
			}
			if len(project.Clips) > 0 && int64(project.Settings.FPSNum)*int64(command.Settings.FPSDen) != int64(command.Settings.FPSNum)*int64(project.Settings.FPSDen) {
				return problem("validation", "工程已有片段，不能静默改变工程帧率")
			}
			project.Settings = *command.Settings
		}
	default:
		return problem("unknown_command", "不支持的编辑命令："+command.Type)
	}
	return nil
}

// 已知同源的纯路径修复不改变画面/声音及时间映射，因此允许用于锁定轨道的离线素材恢复。
// 新指纹、类型、名称、时长、帧率等任一业务元数据变化都不属于这个例外。
func pureAssetRelink(before, after Asset) bool {
	if before.Fingerprint == "" || before.Fingerprint != after.Fingerprint {
		return false
	}
	before.Path, after.Path = "", ""
	before.URL, after.URL = "", ""
	before.ThumbnailURL, after.ThumbnailURL = "", ""
	before.Missing, after.Missing = false, false
	beforeFont, afterFont := before.Font, after.Font
	before.Font, after.Font = nil, nil
	beforeCount, afterCount := before.AudioSampleCount, after.AudioSampleCount
	before.AudioSampleCount, after.AudioSampleCount = nil, nil
	return before == after && ((beforeFont == nil && afterFont == nil) || (beforeFont != nil && afterFont != nil && *beforeFont == *afterFont)) && ((beforeCount == nil && afterCount == nil) || (beforeCount != nil && afterCount != nil && *beforeCount == *afterCount))
}

// 新轨道进入对应的视觉分区，但不会重新排列用户已有轨道。
func trackInsertionIndex(tracks []Track, kind string) int {
	if kind == "audio" {
		return len(tracks)
	}
	if kind == "video" {
		for i, track := range tracks {
			if track.Kind == "audio" {
				return i
			}
		}
		return len(tracks)
	}
	position := 0
	for i, track := range tracks {
		if track.Kind == kind || (kind == "text" && track.Kind == "caption") {
			position = i + 1
		}
	}
	return position
}

func updateClip(project *Project, command Command) error {
	if command.Clip == nil {
		return problem("validation", "缺少片段信息")
	}
	i := clipIndex(project, command.Clip.ID)
	if i < 0 {
		return problem("not_found", "目标片段不存在")
	}
	before, after, err := prepareClipUpdate(project, project.Clips[i], *command.Clip)
	if err != nil {
		return err
	}
	ids := selectedIDs(project, before, linked(command))
	for _, clip := range project.Clips {
		if ids[clip.ID] {
			if err := editableTrack(project, clip.TrackID); err != nil {
				return err
			}
		}
	}
	for j := range project.Clips {
		other := &project.Clips[j]
		if other.ID == before.ID {
			*other = after
			continue
		}
		if !ids[other.ID] {
			continue
		}
		otherBefore := cloneClip(*other)
		var ok bool
		if other.StartFrame, ok = addDelta(other.StartFrame, after.StartFrame, before.StartFrame); !ok {
			return problem("validation", "关联片段移动超出有效范围")
		}
		if other.DurationFrames, ok = addDelta(other.DurationFrames, after.DurationFrames, before.DurationFrames); !ok {
			return problem("validation", "关联片段长度超出有效范围")
		}
		if validKind(other.Kind, "video", "audio") {
			if other.SourceInUs, ok = linkedSourceIn(*other, before, after); !ok {
				return problem("validation", "关联片段素材入点超出有效范围")
			}
			if before.SpeedNum != after.SpeedNum || before.SpeedDen != after.SpeedDen {
				if other.SpeedNum, other.SpeedDen, ok = linkedSpeed(*other, before, after); !ok {
					return problem("validation", "关联片段变速超出有效范围")
				}
			}
			if asset, found := transcriptAsset(project, other.AssetID); found {
				var err error
				otherBefore, err = preparePhaseUpdate(otherBefore, other, asset, project.Settings)
				if err != nil {
					return err
				}
			}
			if otherBefore.AudioRange != nil || other.AudioRange != nil {
				asset, _ := transcriptAsset(project, other.AssetID)
				if err := adaptAudioUpdate(otherBefore, other, asset, project.Settings, before.Kind != "audio"); err != nil {
					return err
				}
			}
			asset, _ := transcriptAsset(project, other.AssetID)
			if err := finishPhaseUpdate(otherBefore, other, asset, project.Settings, false, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func linkedSourceIn(other, before, after Clip) (int64, bool) {
	if before.SpeedNum <= 0 || before.SpeedDen <= 0 || other.SpeedNum <= 0 || other.SpeedDen <= 0 {
		return 0, false
	}
	// 关联素材可以使用不同源速率；源入点的变化应先换算成相同的时间线时长。
	delta := new(big.Int).Sub(big.NewInt(after.SourceInUs), big.NewInt(before.SourceInUs))
	delta.Mul(delta, big.NewInt(int64(other.SpeedNum)))
	delta.Mul(delta, big.NewInt(int64(before.SpeedDen)))
	denominator := new(big.Int).Mul(big.NewInt(int64(other.SpeedDen)), big.NewInt(int64(before.SpeedNum)))
	delta.Quo(delta, denominator)
	delta.Add(delta, big.NewInt(other.SourceInUs))
	return delta.Int64(), delta.IsInt64() && delta.Sign() >= 0
}

func linkedSpeed(other, before, after Clip) (int, int, bool) {
	if before.SpeedNum <= 0 || before.SpeedDen <= 0 || after.SpeedNum <= 0 || after.SpeedDen <= 0 || other.SpeedNum <= 0 || other.SpeedDen <= 0 {
		return 0, 0, false
	}
	result := new(big.Rat).SetFrac64(int64(other.SpeedNum), int64(other.SpeedDen))
	result.Mul(result, new(big.Rat).SetFrac64(int64(after.SpeedNum), int64(after.SpeedDen)))
	result.Quo(result, new(big.Rat).SetFrac64(int64(before.SpeedNum), int64(before.SpeedDen)))
	if !result.Num().IsInt64() || !result.Denom().IsInt64() || result.Num().Int64() > 1_000_000 || result.Denom().Int64() > 1_000_000 {
		return 0, 0, false
	}
	return int(result.Num().Int64()), int(result.Denom().Int64()), true
}

func addDelta(value, after, before int64) (int64, bool) {
	if after >= before {
		delta := after - before
		if value > math.MaxInt64-delta {
			return 0, false
		}
		return value + delta, true
	}
	delta := before - after
	if value < delta {
		return 0, false
	}
	return value - delta, true
}

func splitClip(project *Project, command Command) error {
	i := clipIndex(project, command.ClipID)
	if i < 0 {
		return problem("not_found", "目标片段不存在")
	}
	target := project.Clips[i]
	if target.CaptionSource != nil {
		return problem("caption_binding", "绑定字幕需随源片段分割；手动分割请先解除绑定")
	}
	ids := selectedIDs(project, target, linked(command))
	rightGroup := ""
	if target.LinkGroup != "" && linked(command) {
		rightGroup = NewID("link")
	}
	rights := make([]Clip, 0, len(ids))
	for j := range project.Clips {
		clip := &project.Clips[j]
		if !ids[clip.ID] {
			continue
		}
		if err := editableTrack(project, clip.TrackID); err != nil {
			return err
		}
		if command.AtFrame <= clip.StartFrame || command.AtFrame >= clip.StartFrame+clip.DurationFrames {
			return problem("invalid_split", "分割点必须位于片段及其关联片段内部")
		}
		left, right, err := splitClipParts(project, *clip, command.AtFrame)
		if err != nil {
			return err
		}
		if target.LinkGroup != "" {
			if linked(command) {
				right.LinkGroup = rightGroup
			} else {
				left.LinkGroup, right.LinkGroup = "", ""
			}
		}
		*clip = left
		rights = append(rights, right)
	}
	project.Clips = append(project.Clips, rights...)
	return nil
}

func prepareClipUpdate(project *Project, before Clip, proposed Clip) (Clip, Clip, error) {
	after := cloneClip(proposed)
	if after.SpeedNum <= 0 || after.SpeedDen <= 0 || after.SpeedNum > 1_000_000 || after.SpeedDen > 1_000_000 || int64(after.SpeedNum) > 100*int64(after.SpeedDen) || int64(after.SpeedDen) > 100*int64(after.SpeedNum) {
		return Clip{}, Clip{}, problem("validation", "片段速度必须在0.01到100倍之间")
	}
	if !mediaSourceKind(after) && (before.AssetID != after.AssetID || before.Kind != after.Kind) {
		after.AudioFade = nil
		after.SourcePhase = nil
		if sameAudioRange(before.AudioRange, after.AudioRange) {
			after.AudioRange = nil
		}
	}
	explicitAudioRange := !sameAudioRange(before.AudioRange, after.AudioRange) && after.AudioRange != nil
	if asset, found := transcriptAsset(project, before.AssetID); found && mediaSourceKind(before) {
		var err error
		before, err = preparePhaseUpdate(before, &after, asset, project.Settings)
		if err != nil {
			return Clip{}, Clip{}, err
		}
	}
	if err := protectBoundCaption(before, &after); err != nil {
		return Clip{}, Clip{}, err
	}
	if mediaSourceKind(after) && (before.AudioRange != nil || after.AudioRange != nil) {
		asset, found := transcriptAsset(project, after.AssetID)
		if !found {
			return Clip{}, Clip{}, problem("audio_range", "声音源素材不存在")
		}
		if err := adaptAudioUpdate(before, &after, asset, project.Settings, before.Kind == "video"); err != nil {
			return Clip{}, Clip{}, err
		}
	}
	if asset, found := transcriptAsset(project, after.AssetID); found && mediaSourceKind(after) {
		if err := finishPhaseUpdate(before, &after, asset, project.Settings, false, explicitAudioRange); err != nil {
			return Clip{}, Clip{}, err
		}
	} else if after.SourcePhase != nil {
		return Clip{}, Clip{}, problem("source_phase", "静态图或字幕不能携带源处理配方")
	}
	if err := editableTrack(project, after.TrackID); err != nil {
		return Clip{}, Clip{}, err
	}
	return before, after, nil
}

// Shape 只读取诊断所需的规模，不克隆素材、转写与时间线正文。
func (engine *Engine) Shape() (string, int64, Settings, int, int, int) {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	p := &engine.project
	return p.ID, p.Revision, p.Settings, len(p.Clips), len(p.Assets), len(p.Tracks)
}
