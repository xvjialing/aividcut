package application

import (
	"errors"
	"math"
	"strconv"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/inspectordrafts"
)

type InspectorDraftInput struct {
	ProjectScope
	Target string                  `json:"target"`
	Fields []inspectordrafts.Field `json:"fields"`
}
type InspectorDraftRef struct {
	ProjectScope
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}
type InspectorDraftReview struct {
	InspectorDraftRef
	Values         map[string]string `json:"values"`
	RecheckCurrent bool              `json:"recheckCurrent"`
}
type InspectorDraftPreview struct {
	ID       string                       `json:"id"`
	Rows     []inspectordrafts.Difference `json:"rows"`
	CanApply bool                         `json:"canApply"`
	Error    string                       `json:"error,omitempty"`
}
type draftPreview struct {
	scope    ProjectScope
	record   inspectordrafts.Record
	commands []editor.Command
	expires  time.Time
	applied  bool
}
type inspectorDraftState struct {
	store     *inspectordrafts.Store
	owned     map[string]inspectordrafts.Record
	previews  map[string]*draftPreview
	lastError string
}

func (s *Service) ConfigureInspectorDrafts(directory string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.drafts != nil || s.draftsClosed {
		return errors.New("属性草稿存储已经配置或关闭")
	}
	store, err := inspectordrafts.Open(directory)
	if err != nil {
		return err
	}
	s.drafts = &inspectorDraftState{store: store, owned: map[string]inspectordrafts.Record{}, previews: map[string]*draftPreview{}}
	return nil
}
func (s *Service) ListInspectorDrafts() (inspectordrafts.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.drafts == nil || s.draftsClosed {
		return inspectordrafts.Page{}, errors.New("属性草稿服务未配置")
	}
	return s.drafts.store.List()
}
func draftOwner(scope ProjectScope, target string) string {
	return scope.ProjectID + "/" + strconv.FormatUint(scope.SourceEpoch, 10) + "/" + target
}
func (s *Service) SaveInspectorDraft(input InspectorDraftInput) (*inspectordrafts.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.drafts
	if d == nil || s.draftsClosed {
		return nil, errors.New("属性草稿服务未配置")
	}
	scope := s.projectScopeLocked()
	if input.ProjectID != scope.ProjectID || input.SourceEpoch != scope.SourceEpoch || input.ProjectRevision > scope.ProjectRevision || input.ProjectRevision < 0 {
		return nil, errors.New("工程实例已变化，旧属性草稿未写入")
	}
	if err := inspectordrafts.ValidateFields(input.Fields); err != nil {
		return nil, err
	}
	p := s.engine.Snapshot()
	fields := []inspectordrafts.Field{}
	for _, field := range input.Fields {
		current, err := inspectordrafts.Value(p, input.Target, field.Key)
		if (input.Target == "" && field.Key != "projectName") || (input.Target != "" && field.Key == "projectName") {
			return nil, errors.New("字段不属于该属性目标")
		}
		valueError := err
		converted, err := inspectordrafts.Converted(field)
		if valueError != nil || err != nil || !inspectordrafts.Equal(converted, current) {
			fields = append(fields, field)
		}
	}
	key := draftOwner(scope, input.Target)
	v, exists := d.owned[key]
	if len(fields) == 0 {
		if exists {
			if err := d.store.Delete(v.ID, v.SHA256); err != nil {
				return nil, err
			}
			delete(d.owned, key)
		}
		return nil, nil
	}
	v.Fields = fields
	var base *editor.Project
	if !exists {
		v.ID = editor.NewID("draft")
		v.Target = input.Target
		v.OriginalPath = s.projectPath
		base = &p
	}
	saved, err := d.store.Save(v, base)
	if err != nil {
		d.lastError = "属性输入草稿保护失败，请及时提交并保存工程：" + err.Error()
		return nil, err
	}
	d.lastError = ""
	d.owned[key] = saved
	return &saved, nil
}

// 只清理本实例已正常提交的字段；崩溃遗留记录不会被新会话的相同文字删除。
func (s *Service) reconcileInspectorDraftsLocked(p editor.Project) {
	d := s.drafts
	if d == nil || s.draftsClosed {
		return
	}
	scope := s.projectScopeLocked()
	for key, v := range d.owned {
		if key != draftOwner(scope, v.Target) {
			continue
		}
		fields := []inspectordrafts.Field{}
		for _, f := range v.Fields {
			current, e := inspectordrafts.Value(p, v.Target, f.Key)
			value, err := inspectordrafts.Converted(f)
			if e != nil || err != nil || !inspectordrafts.Equal(value, current) {
				fields = append(fields, f)
			}
		}
		if len(fields) == len(v.Fields) {
			continue
		}
		var err error
		if len(fields) == 0 {
			err = d.store.Delete(v.ID, v.SHA256)
			if err == nil {
				delete(d.owned, key)
			}
		} else {
			v.Fields = fields
			v, err = d.store.Save(v, nil)
			if err == nil {
				d.owned[key] = v
			}
		}
		if err != nil {
			d.lastError = "工程已提交，但输入草稿清理失败，请在恢复中心核对。"
		}
	}
}
func (s *Service) DeleteInspectorDraft(input InspectorDraftRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkProjectScopeLocked(input.ProjectScope); err != nil {
		return err
	}
	if s.drafts == nil || s.draftsClosed {
		return errors.New("属性草稿服务未配置")
	}
	if err := s.drafts.store.Delete(input.ID, input.SHA256); err != nil {
		return err
	}
	for k, v := range s.drafts.owned {
		if v.ID == input.ID {
			delete(s.drafts.owned, k)
		}
	}
	return nil
}
func (s *Service) PreviewInspectorDraft(input InspectorDraftReview) (InspectorDraftPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := InspectorDraftPreview{Rows: []inspectordrafts.Difference{}}
	if err := s.checkProjectScopeLocked(input.ProjectScope); err != nil {
		return out, err
	}
	d := s.drafts
	if d == nil || s.draftsClosed {
		return out, errors.New("属性草稿服务未配置")
	}
	v, err := d.store.Load(input.ID, input.SHA256)
	if err != nil {
		return out, err
	}
	if v.ProjectID != input.ProjectID {
		return out, errors.New("请先打开草稿所属工程，或明确恢复草稿基础工程")
	}
	fields := append([]inspectordrafts.Field{}, v.Fields...)
	for k, raw := range input.Values {
		found := false
		for i := range fields {
			if fields[i].Key == k {
				fields[i].Raw = raw
				found = true
			}
		}
		if !found {
			return out, errors.New("不能追加未登记的草稿字段")
		}
	}
	p := s.engine.Snapshot()
	rows, commands, err := inspectordrafts.Preview(p, v.Target, fields, input.RecheckCurrent)
	out.Rows = rows
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}
	if len(commands) == 0 {
		return out, nil
	}
	test, err := editor.NewEngine(p)
	if err == nil {
		_, err = test.Apply(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: editor.NewID("draft-check"), Commands: commands})
	}
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}
	for id, preview := range d.previews {
		if time.Now().After(preview.expires) {
			delete(d.previews, id)
		}
	}
	if len(d.previews) >= 32 {
		d.previews = map[string]*draftPreview{}
	}
	out.ID = editor.NewID("draft-preview")
	out.CanApply = true
	d.previews[out.ID] = &draftPreview{scope: input.ProjectScope, record: v, commands: commands, expires: time.Now().Add(10 * time.Minute)}
	return out, nil
}
func (s *Service) ApplyInspectorDraft(id string) (Bootstrap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.drafts
	if d == nil || s.draftsClosed {
		return Bootstrap{}, errors.New("属性草稿服务未配置")
	}
	v := d.previews[id]
	if v == nil || time.Now().After(v.expires) {
		return Bootstrap{}, errors.New("草稿核对已失效，请重新核对")
	}
	if v.applied {
		return Bootstrap{}, errors.New("该草稿已经应用，不能重复应用")
	}
	if err := s.checkProjectScopeLocked(v.scope); err != nil {
		return Bootstrap{}, err
	}
	if _, err := d.store.Load(v.record.ID, v.record.SHA256); err != nil {
		return Bootstrap{}, err
	}
	if _, err := s.engine.Apply(editor.EditRequest{BaseRevision: v.scope.ProjectRevision, IdempotencyKey: id, Commands: v.commands}); err != nil {
		return Bootstrap{}, err
	}
	v.applied = true
	warning := ""
	if err := d.store.Delete(v.record.ID, v.record.SHA256); err != nil {
		warning = "草稿已应用且可撤销，但原输入记录清理失败，请稍后整理。"
	}
	for k, record := range d.owned {
		if record.ID == v.record.ID {
			delete(d.owned, k)
		}
	}
	b := s.bootstrapLocked()
	if warning != "" {
		b.Warnings = append(b.Warnings, warning)
	}
	return b, nil
}
func (s *Service) RestoreInspectorDraftBase(input InspectorDraftRef) (Bootstrap, error) {
	s.mu.Lock()
	r := s.recovery
	s.mu.Unlock()
	if r == nil {
		return Bootstrap{}, errors.New("请先配置工程恢复服务")
	}
	r.ops.Lock()
	defer r.ops.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkProjectScopeLocked(input.ProjectScope); err != nil {
		return Bootstrap{}, err
	}
	if s.drafts == nil || s.draftsClosed || r.closing {
		return Bootstrap{}, errors.New("草稿恢复服务未配置或关闭")
	}
	p, meta, warnings, err := s.drafts.store.Base(input.ID, input.SHA256)
	if err != nil {
		return Bootstrap{}, err
	}
	current := s.engine.Snapshot()
	revision := max(p.Revision, current.Revision)
	if revision == math.MaxInt64 {
		return Bootstrap{}, errors.New("工程修订已达到上限")
	}
	p.Revision = revision + 1
	engine, err := editor.NewEngine(p)
	if err != nil {
		return Bootstrap{}, err
	}
	if projectContentHash(current) != s.savedHash {
		id := r.activeID
		if id == "" {
			id = editor.NewID("recovery")
		}
		path := s.projectPath
		if path == "" {
			path = r.originalPath
		}
		if _, err = r.store.Write(id, current, path, true); err != nil {
			return Bootstrap{}, errors.New("无法保护当前未保存工程，已停止恢复：" + err.Error())
		}
	}
	// 已成功保护当前状态；保留输入记录，基础工程只恢复到内存，原文件不覆盖。
	r.activeID = ""
	r.checked = recoveryKey{}
	r.suppressed = recoveryKey{}
	r.protectedAt = nil
	r.originalPath = meta.OriginalPath
	s.engine = engine
	s.projectPath = ""
	s.savedHash = ""
	s.clipboardCuts = nil
	s.clipboardCutOrder = nil
	s.resetWaveforms(false)
	s.resetThumbnails(false)
	s.files.Reset()
	s.urls = map[string]mediaURL{}
	b := s.bootstrapLocked()
	b.Warnings = append(b.Warnings, warnings...)
	b.Warnings = append(b.Warnings, "已打开草稿基础工程，尚未应用输入值；请重新核对并应用，保存时另选位置。")
	return b, nil
}
