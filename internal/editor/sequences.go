package editor

import (
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MainSequenceID = "sequence-main"
const MaxSequences = 32

// ActiveSequenceView是供界面、预览或规划使用的只读投影，不能当作完整文件保存。
func ActiveSequenceView(p Project) Project {
	if len(p.OtherSequences) > 0 {
		p.ActiveSequenceProjection = true
	}
	p.OtherSequences = nil
	p.SequenceOrder = nil
	return p
}

func (e *Engine) SequenceIdentity() (string, string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return CurrentSequenceID(e.project), CurrentSequenceName(e.project)
}

// 根Project持有当前序列；OtherSequences只持有其它序列，不存在两份活动数据。
type Sequence struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Settings    Settings     `json:"settings"`
	Tracks      []Track      `json:"tracks"`
	Clips       []Clip       `json:"clips"`
	Transcripts []Transcript `json:"transcripts,omitempty"`
}
type SequenceSummary struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Settings       Settings `json:"settings"`
	ClipCount      int      `json:"clipCount"`
	TrackCount     int      `json:"trackCount"`
	DurationFrames int64    `json:"durationFrames"`
	Locked         bool     `json:"locked"`
	Current        bool     `json:"current"`
}

func CurrentSequenceID(p Project) string {
	if p.SequenceID == "" {
		return MainSequenceID
	}
	return p.SequenceID
}
func CurrentSequenceName(p Project) string {
	if p.SequenceName == "" {
		return "主时间线"
	}
	return p.SequenceName
}
func sequenceFromProject(p Project) Sequence {
	return Sequence{ID: CurrentSequenceID(p), Name: CurrentSequenceName(p), Settings: p.Settings, Tracks: p.Tracks, Clips: p.Clips, Transcripts: p.Transcripts}
}
func cloneSequence(s Sequence) Sequence {
	p := CloneProject(Project{Tracks: s.Tracks, Clips: s.Clips, Transcripts: s.Transcripts})
	s.Tracks, s.Clips, s.Transcripts = p.Tracks, p.Clips, p.Transcripts
	return s
}

// SequenceProject生成某个序列的工作视图，素材与画面分析仍来自同一工程。
func SequenceProject(p Project, s Sequence) Project {
	p.SequenceID, p.SequenceName = s.ID, s.Name
	p.Settings, p.Tracks, p.Clips, p.Transcripts = s.Settings, s.Tracks, s.Clips, s.Transcripts
	p.OtherSequences = nil
	p.SequenceOrder = nil
	return p
}
func AllSequences(p Project) []Sequence {
	byID := map[string]Sequence{CurrentSequenceID(p): sequenceFromProject(p)}
	for _, s := range p.OtherSequences {
		byID[s.ID] = s
	}
	order := p.SequenceOrder
	if len(order) == 0 {
		order = []string{CurrentSequenceID(p)}
	}
	out := make([]Sequence, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}
func SequenceSummaries(p Project) []SequenceSummary {
	out := make([]SequenceSummary, 0, len(p.OtherSequences)+1)
	for _, s := range AllSequences(p) {
		locked := false
		for _, t := range s.Tracks {
			locked = locked || t.Locked
		}
		view := SequenceProject(p, s)
		out = append(out, SequenceSummary{ID: s.ID, Name: s.Name, Settings: s.Settings, ClipCount: len(s.Clips), TrackCount: len(s.Tracks), DurationFrames: DurationFrames(view), Locked: locked, Current: s.ID == CurrentSequenceID(p)})
	}
	return out
}
func validSequenceName(name string) bool {
	if name != strings.TrimSpace(name) || name == "" || utf8.RuneCountInString(name) > 80 {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func validateSequenceSet(p Project) error {
	if (p.SequenceID == "") != (p.SequenceName == "") {
		return problem("sequence", "当前序列标识和名称需同时提供")
	}
	if !validSequenceName(CurrentSequenceName(p)) || len(p.OtherSequences) >= MaxSequences {
		return problem("sequence", "序列名称无效或数量超过32个")
	}
	ids := map[string]bool{CurrentSequenceID(p): true}
	names := map[string]bool{strings.ToLower(CurrentSequenceName(p)): true}
	for _, s := range p.OtherSequences {
		if strings.TrimSpace(s.ID) == "" || ids[s.ID] || !validSequenceName(s.Name) || names[strings.ToLower(s.Name)] {
			return problem("sequence", "序列标识或名称无效、重复")
		}
		ids[s.ID] = true
		names[strings.ToLower(s.Name)] = true
	}
	if len(p.SequenceOrder) == 0 {
		if len(p.OtherSequences) > 0 {
			return problem("sequence", "多序列工程缺少稳定顺序")
		}
		return nil
	}
	if len(p.SequenceOrder) != len(ids) {
		return problem("sequence", "序列顺序与内容不一致")
	}
	seen := map[string]bool{}
	for _, id := range p.SequenceOrder {
		if !ids[id] || seen[id] {
			return problem("sequence", "序列顺序包含未知或重复标识")
		}
		seen[id] = true
	}
	return nil
}
func isSequenceCommand(kind string) bool {
	return kind == "create_sequence" || kind == "duplicate_sequence" || kind == "rename_sequence" || kind == "delete_sequence"
}
func sequenceIndex(p *Project, id string) int {
	for i, s := range p.OtherSequences {
		if s.ID == id {
			return i
		}
	}
	return -1
}
func activateSequence(p *Project, id string) error {
	if id == CurrentSequenceID(*p) {
		return nil
	}
	i := sequenceIndex(p, id)
	if i < 0 {
		return problem("not_found", "需要切换的序列不存在")
	}
	before, target := sequenceFromProject(*p), p.OtherSequences[i]
	p.OtherSequences[i] = before
	p.SequenceID, p.SequenceName = target.ID, target.Name
	p.Settings, p.Tracks, p.Clips, p.Transcripts = target.Settings, target.Tracks, target.Clips, target.Transcripts
	return nil
}
func checkSequenceName(p Project, name, except string) error {
	if !validSequenceName(name) {
		return problem("sequence", "序列名称需为1到80个字符，不能包含控制字符或首尾空格")
	}
	for _, s := range AllSequences(p) {
		if s.ID != except && strings.EqualFold(s.Name, name) {
			return problem("sequence", "已有同名序列，请使用不同名称")
		}
	}
	return nil
}
func applySequenceCommand(p *Project, c Command) error {
	remaining := c
	remaining.Type, remaining.SequenceID, remaining.SourceSequenceID, remaining.Name, remaining.Settings = "", "", "", "", nil
	if !reflect.DeepEqual(remaining, Command{}) {
		return problem("sequence", "序列管理不能携带片段、轨道或其它编辑参数")
	}
	if strings.TrimSpace(c.SequenceID) == "" || len(c.SequenceID) > 128 {
		return problem("sequence", "需要有效序列标识")
	}
	if len(p.SequenceOrder) == 0 {
		p.SequenceOrder = []string{CurrentSequenceID(*p)}
	}
	switch c.Type {
	case "create_sequence", "duplicate_sequence":
		if len(p.OtherSequences)+1 >= MaxSequences {
			return problem("sequence", "一个工程最多支持32个序列")
		}
		if c.SequenceID == CurrentSequenceID(*p) || sequenceIndex(p, c.SequenceID) >= 0 {
			return problem("sequence", "新序列标识已存在")
		}
		if err := checkSequenceName(*p, c.Name, ""); err != nil {
			return err
		}
		var s Sequence
		if c.Type == "duplicate_sequence" {
			if c.Settings != nil {
				return problem("sequence", "复制序列保持原时基，不接受额外画幅参数")
			}
			if c.SourceSequenceID == CurrentSequenceID(*p) {
				s = cloneSequence(sequenceFromProject(*p))
			} else {
				index := sequenceIndex(p, c.SourceSequenceID)
				if index < 0 {
					return problem("sequence", "需要复制的序列不存在")
				}
				s = cloneSequence(p.OtherSequences[index])
			}
		} else {
			if c.SourceSequenceID != "" {
				return problem("sequence", "空序列不能指定复制来源")
			}
			settings := p.Settings
			if c.Settings != nil {
				settings = *c.Settings
			}
			blank, err := NewProjectWithSettings(c.Name, settings)
			if err != nil {
				return err
			}
			s = sequenceFromProject(blank)
		}
		s.ID, s.Name = c.SequenceID, c.Name
		p.OtherSequences = append(p.OtherSequences, s)
		p.SequenceOrder = append(p.SequenceOrder, s.ID)
		return activateSequence(p, s.ID)
	case "rename_sequence":
		if c.SourceSequenceID != "" || c.Settings != nil {
			return problem("sequence", "重命名仅修改序列名称")
		}
		if err := checkSequenceName(*p, c.Name, c.SequenceID); err != nil {
			return err
		}
		if c.SequenceID == CurrentSequenceID(*p) {
			p.SequenceID, p.SequenceName = c.SequenceID, c.Name
		} else {
			index := sequenceIndex(p, c.SequenceID)
			if index < 0 {
				return problem("not_found", "需要重命名的序列不存在")
			}
			p.OtherSequences[index].Name = c.Name
		}
	case "delete_sequence":
		if c.Name != "" || c.SourceSequenceID != "" || c.Settings != nil {
			return problem("sequence", "删除序列仅需要明确标识")
		}
		if len(p.OtherSequences) == 0 {
			return problem("sequence", "工程至少保留一个序列")
		}
		var target Sequence
		if c.SequenceID == CurrentSequenceID(*p) {
			target = sequenceFromProject(*p)
		} else {
			index := sequenceIndex(p, c.SequenceID)
			if index < 0 {
				return problem("not_found", "需要删除的序列不存在")
			}
			target = p.OtherSequences[index]
		}
		for _, t := range target.Tracks {
			if t.Locked {
				return problem("track_locked", "该序列有锁定轨道，请先切入并解锁后再删除")
			}
		}
		if c.SequenceID == CurrentSequenceID(*p) {
			for _, id := range p.SequenceOrder {
				if id != c.SequenceID {
					if err := activateSequence(p, id); err != nil {
						return err
					}
					break
				}
			}
		}
		index := sequenceIndex(p, c.SequenceID)
		p.OtherSequences = append(p.OtherSequences[:index], p.OtherSequences[index+1:]...)
		order := make([]string, 0, len(p.SequenceOrder)-1)
		for _, id := range p.SequenceOrder {
			if id != c.SequenceID {
				order = append(order, id)
			}
		}
		p.SequenceOrder = order
	default:
		return problem("sequence", "未知序列操作")
	}
	return nil
}

// ActivateSequence是导航，不占用编辑历史、也不清空Redo；修订变化阻止旧位置命令。
func (e *Engine) ActivateSequence(id string) (Project, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == CurrentSequenceID(e.project) {
		return CloneProject(e.project), nil
	}
	next := CloneProject(e.project)
	if err := activateSequence(&next, id); err != nil {
		return Project{}, err
	}
	if err := ValidateProject(next); err != nil {
		return Project{}, err
	}
	if next.Revision == int64(^uint64(0)>>1) {
		return Project{}, problem("validation", "工程修订号超出可表示范围")
	}
	next.Revision++
	e.project = next
	return CloneProject(next), nil
}

// ContentState不包含导航位置；当前序列与其它序列按稳定顺序归一后比较内容。
func ContentState(p Project) any {
	return struct {
		SchemaVersion  int
		ID, Name       string
		Assets         []Asset
		VisualAnalyses []VisualAnalysis
		Sequences      []Sequence
	}{p.SchemaVersion, p.ID, p.Name, p.Assets, p.VisualAnalyses, AllSequences(p)}
}
