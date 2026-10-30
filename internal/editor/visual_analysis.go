package editor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

type SourceFrameEvidence struct {
	ID                  string `json:"id"`
	AssetID             string `json:"assetId"`
	AssetName           string `json:"assetName"`
	SourceFingerprint   string `json:"sourceFingerprint"`
	RequestedPositionUs int64  `json:"requestedPositionUs"`
	PositionUs          int64  `json:"positionUs"`
	ImageSHA256         string `json:"imageSha256"`
	Width               int    `json:"width"`
	Height              int    `json:"height"`
	Profile             string `json:"profile"`
	RecipeVersion       string `json:"recipeVersion"`
}

func SourceFrameEvidenceID(value SourceFrameEvidence) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s", value.AssetID, value.SourceFingerprint, value.RecipeVersion, value.PositionUs, value.ImageSHA256))))
}
func validSourceFrameEvidence(value SourceFrameEvidence) bool {
	hash, err := hex.DecodeString(value.ImageSHA256)
	source, sourceErr := hex.DecodeString(strings.TrimPrefix(value.SourceFingerprint, "sha256:"))
	width, height := 960, 540
	if value.Profile == "overview" {
		width, height = 320, 180
	}
	return err == nil && len(hash) == 32 && sourceErr == nil && len(source) == 32 && strings.HasPrefix(value.SourceFingerprint, "sha256:") && value.ID == SourceFrameEvidenceID(value) && value.AssetID != "" && len(value.AssetID) <= 256 && utf8.ValidString(value.AssetName) && len(value.AssetName) <= 1024 && value.RequestedPositionUs >= 0 && value.RequestedPositionUs < 86400_000_000 && value.PositionUs == value.RequestedPositionUs && (value.Profile == "detail" || value.Profile == "overview") && value.RecipeVersion == value.Profile+"-jpeg-v1" && value.Width > 0 && value.Height > 0 && value.Width <= width && value.Height <= height
}

func ValidateSourceFrameEvidence(value SourceFrameEvidence) error {
	if !validSourceFrameEvidence(value) {
		return problem("visual_evidence", "源画面证据的身份或图像元数据无效")
	}
	return nil
}

type VisualNote struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Text        string   `json:"text"`
	Tags        []string `json:"tags,omitempty"`
	EvidenceIDs []string `json:"evidenceIds"`
}
type VisualAnalysis struct {
	ID                string                `json:"id"`
	AssetID           string                `json:"assetId"`
	SourceKind        string                `json:"sourceKind"`
	SourceFingerprint string                `json:"sourceFingerprint"`
	SourceDurationUs  int64                 `json:"sourceDurationUs"`
	Producer          string                `json:"producer"`
	ExecutionID       string                `json:"executionId,omitempty"`
	MethodVersion     string                `json:"methodVersion"`
	Coverage          string                `json:"coverage"`
	Frames            []SourceFrameEvidence `json:"frames"`
	Notes             []VisualNote          `json:"notes"`
	AcceptedAt        time.Time             `json:"acceptedAt"`
	Stale             bool                  `json:"stale,omitempty"`
}

func CloneVisualAnalysis(value VisualAnalysis) VisualAnalysis {
	value.Frames = append([]SourceFrameEvidence{}, value.Frames...)
	value.Notes = append([]VisualNote{}, value.Notes...)
	for i := range value.Notes {
		value.Notes[i].Tags = append([]string{}, value.Notes[i].Tags...)
		value.Notes[i].EvidenceIDs = append([]string{}, value.Notes[i].EvidenceIDs...)
	}
	return value
}
func validVisualMethod(producer, method string) bool {
	switch producer {
	case "apple-vision":
		return method == "apple-vision-ocr-v1"
	case "windows-media-ocr":
		return method == "windows-media-ocr-v1"
	case "codex", "claude", "external-mcp":
		return method == "visual-notes-v1"
	}
	return false
}
func ValidateVisualAnalysis(value VisualAnalysis) error {
	if !validTranscriptID(value.ID) || !validTranscriptID(value.AssetID) || !validKind(value.SourceKind, "image", "video") || !validVisualMethod(value.Producer, value.MethodVersion) || value.Coverage != "sampled_frames" || len(value.Frames) < 1 || len(value.Frames) > 20 || len(value.Notes) < 1 || len(value.Notes) > 50 {
		return problem("visual_analysis", "画面分析身份、范围或数量无效")
	}
	if value.SourceKind == "image" && value.SourceDurationUs != 0 || value.SourceKind == "video" && value.SourceDurationUs <= 0 {
		return problem("visual_analysis", "画面分析源时长无效")
	}
	frames := map[string]bool{}
	for _, frame := range value.Frames {
		if ValidateSourceFrameEvidence(frame) != nil || frames[frame.ID] || frame.AssetID != value.AssetID || frame.SourceFingerprint != value.SourceFingerprint || value.SourceKind == "image" && frame.PositionUs != 0 || value.SourceKind == "video" && frame.PositionUs >= value.SourceDurationUs {
			return problem("visual_evidence", "画面证据不属于该分析来源")
		}
		frames[frame.ID] = true
	}
	notes := map[string]bool{}
	size := 0
	for _, note := range value.Notes {
		if !validTranscriptID(note.ID) || notes[note.ID] || !validKind(note.Kind, "observation", "inference", "suggestion") || !validTranscriptText(note.Text, 4096, true) || utf8.RuneCountInString(note.Text) > 1000 || len(note.Tags) > 8 || len(note.EvidenceIDs) < 1 || len(note.EvidenceIDs) > 20 {
			return problem("visual_note", "分析文字、类别或引用数量无效")
		}
		notes[note.ID] = true
		size += len(note.Text)
		tags := map[string]bool{}
		for _, tag := range note.Tags {
			if !validTranscriptText(tag, 160, true) || utf8.RuneCountInString(tag) > 40 || tags[tag] {
				return problem("visual_note", "分析标签无效或重复")
			}
			tags[tag] = true
			size += len(tag)
		}
		seen := map[string]bool{}
		for _, id := range note.EvidenceIDs {
			if !frames[id] || seen[id] {
				return problem("visual_evidence", "分析引用了未提供或重复的证据")
			}
			seen[id] = true
		}
	}
	if size > 64<<10 {
		return problem("visual_analysis", "单份画面分析文字超过64KiB")
	}
	return nil
}
func validateVisualAnalyses(project Project) error {
	if len(project.VisualAnalyses) > 1000 {
		return problem("visual_limit", "工程画面分析超过1000份")
	}
	known := map[string]bool{}
	total := 0
	for _, value := range project.VisualAnalyses {
		if known[value.ID] || value.AcceptedAt.IsZero() {
			return problem("visual_analysis", "分析记录重复或缺少接纳时间")
		}
		known[value.ID] = true
		if err := ValidateVisualAnalysis(value); err != nil {
			return err
		}
		for _, note := range value.Notes {
			total += len(note.Text)
			for _, tag := range note.Tags {
				total += len(tag)
			}
		}
		if !value.Stale {
			a, ok := visualSource(project, value.AssetID)
			if !ok || a.Fingerprint != value.SourceFingerprint || a.Kind != value.SourceKind || a.DurationUs != value.SourceDurationUs {
				return problem("visual_stale", "分析源已经变化但未标记失效")
			}
		}
	}
	if total > 8<<20 {
		return problem("visual_limit", "工程画面分析文字超过8MiB")
	}
	return nil
}
func visualSource(project Project, id string) (Asset, bool) {
	for _, a := range project.Assets {
		if a.ID == id {
			return a, true
		}
	}
	return Asset{}, false
}
func refreshVisualAnalyses(project *Project) {
	for i := range project.VisualAnalyses {
		v := &project.VisualAnalyses[i]
		a, ok := visualSource(*project, v.AssetID)
		v.Stale = !ok || a.Fingerprint != v.SourceFingerprint || a.Kind != v.SourceKind || a.DurationUs != v.SourceDurationUs
	}
}
func applyVisualCommand(project *Project, command Command) error {
	expected := Command{Type: command.Type, VisualAnalysis: command.VisualAnalysis, VisualAnalysisID: command.VisualAnalysisID}
	if !reflect.DeepEqual(expected, command) {
		return problem("visual_analysis", "分析命令不能混入剪辑参数")
	}
	switch command.Type {
	case "accept_visual_analysis":
		if command.VisualAnalysis == nil {
			return problem("visual_analysis", "缺少画面分析")
		}
		value := CloneVisualAnalysis(*command.VisualAnalysis)
		for _, old := range project.VisualAnalyses {
			if old.ID == value.ID {
				return problem("visual_analysis", "分析标识已存在")
			}
		}
		if err := ValidateVisualAnalysis(value); err != nil {
			return err
		}
		a, ok := visualSource(*project, value.AssetID)
		if !ok || a.Missing || a.Kind != value.SourceKind || a.Fingerprint != value.SourceFingerprint || a.DurationUs != value.SourceDurationUs || value.Stale {
			return problem("visual_stale", "接纳分析前源已变化或缺失")
		}
		value.AcceptedAt = time.Now().UTC()
		project.VisualAnalyses = append(project.VisualAnalyses, value)
	case "delete_visual_analysis":
		for i, v := range project.VisualAnalyses {
			if v.ID == command.VisualAnalysisID {
				project.VisualAnalyses = append(project.VisualAnalyses[:i], project.VisualAnalyses[i+1:]...)
				return nil
			}
		}
		return problem("visual_analysis", "分析记录不存在")
	}
	return nil
}

// UpgradeProject只做已知的无损v1–v6→v7迁移，不静默接纳旧版本中的新字段。
func UpgradeProject(project Project) (Project, error) {
	if project.SchemaVersion >= 1 && project.SchemaVersion < 7 {
		for _, v := range project.VisualAnalyses {
			if v.Producer == "windows-media-ocr" || v.MethodVersion == "windows-media-ocr-v1" {
				return Project{}, problem("unsupported_schema", "旧版本工程不能夹带v7 Windows OCR来源")
			}
		}
	}

	if project.SchemaVersion >= 1 && project.SchemaVersion < 6 {
		for _, analysis := range project.VisualAnalyses {
			if analysis.Producer == "apple-vision" || analysis.MethodVersion == "apple-vision-ocr-v1" {
				return Project{}, problem("unsupported_schema", "旧版本工程不能夹带v6本地OCR来源")
			}
		}
	}
	if project.SchemaVersion >= 1 && project.SchemaVersion < 5 && (project.ActiveSequenceProjection || project.SequenceID != "" || project.SequenceName != "" || len(project.SequenceOrder) > 0 || len(project.OtherSequences) > 0) {
		return Project{}, problem("unsupported_schema", "旧版本工程不能夹带v5多序列字段")
	}
	if project.SchemaVersion >= 1 && project.SchemaVersion < 4 {
		for _, c := range project.Clips {
			if c.AudioFade != nil {
				return Project{}, problem("unsupported_schema", "旧版本工程不能夹带v4音频淡化字段")
			}
		}
	}
	if project.SchemaVersion == 1 || project.SchemaVersion == 2 {
		for _, a := range project.Assets {
			if a.Kind == "font" || a.Font != nil {
				return Project{}, problem("unsupported_schema", "旧版工程不能包含v3字体资源")
			}
		}
		for _, c := range project.Clips {
			if c.TextStyle.FontAssetID != "" {
				return Project{}, problem("unsupported_schema", "旧版工程不能包含v3字体引用")
			}
		}
	}
	if project.SchemaVersion == 1 {
		if len(project.VisualAnalyses) > 0 {
			return Project{}, problem("unsupported_schema", "旧版本工程不能包含新画面分析字段")
		}
		project.SchemaVersion = SchemaVersion
	}
	if project.SchemaVersion == 2 {
		project.SchemaVersion = SchemaVersion
	}
	if project.SchemaVersion == 3 {
		project.SchemaVersion = SchemaVersion
	}
	if project.SchemaVersion == 4 {
		project.SchemaVersion = SchemaVersion
	}
	if project.SchemaVersion == 5 || project.SchemaVersion == 6 {
		project.SchemaVersion = SchemaVersion
	}
	if err := ValidateProject(project); err != nil {
		return Project{}, err
	}
	return project, nil
}

// VisualAnalysisContext为轮询核对提供轻量视图，不复制完整分析/转写数组。
func (engine *Engine) VisualAnalysisContext(analysisID, assetID string) (string, int64, bool, Asset, bool) {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	present := false
	for _, entry := range engine.project.VisualAnalyses {
		if entry.ID == analysisID {
			present = true
			break
		}
	}
	asset, exists := visualSource(engine.project, assetID)
	return engine.project.ID, engine.project.Revision, present, cloneAsset(asset), exists
}
