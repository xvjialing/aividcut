package generationjobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"aividcut/internal/generation"
)

const maxActiveRecords = 2000
const maxArchivedRecords = 20000
const archiveClockLayout = "20060102T150405.000000000"

var archiveFilename = regexp.MustCompile(`^([0-9]{8}T[0-9]{6}\.[0-9]{9})-([A-Za-z0-9_-]{1,128})\.json$`)

type archivedFile struct {
	name string
	at   time.Time
}
type ArchivedRecord struct {
	ID         string    `json:"id"`
	ArchivedAt time.Time `json:"archivedAt"`
	Job        *Job      `json:"job,omitempty"`
	CanRestore bool      `json:"canRestore"`
	Issue      string    `json:"issue,omitempty"`
}
type ArchivePageInput struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}
type ArchivePage struct {
	Items  []ArchivedRecord `json:"items"`
	Total  int              `json:"total"`
	Active int              `json:"active"`
	Offset int              `json:"offset"`
	Limit  int              `json:"limit"`
}

func (m *Manager) reserveID(id string) error {
	key := strings.ToLower(id)
	if _, exists := m.idOwners[key]; exists {
		return errors.New("生成记录存在重复或大小写冲突标识，请保留资料后核对")
	}
	m.idOwners[key] = id
	return nil
}

func readRecord(path, id string) (record, string, error) {
	var result record
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return result, "", errors.New("任务记录不是有效普通文件或已超过2MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return result, "", errors.New("无法读取任务记录")
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return result, "", errors.New("读取任务记录期间文件已变化")
	}
	data, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return result, "", errors.New("任务记录读取失败或过大")
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil {
		return result, digest, errors.New("任务记录格式损坏")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return result, digest, errors.New("任务记录存在额外内容")
	}
	j := result.Job
	versionValid := result.Version == 1 && j.Estimate == nil || result.Version == 2 && j.Estimate != nil
	if !versionValid || !validEstimate(j.Request, j.Estimate) || j.ID != id || j.Request.ID != id || j.ConnectionID == "" || j.ProjectID == "" || j.Request.Count < 1 || j.CreatedAt.IsZero() || result.Signature != signature(j.ConnectionID, j.ProjectID, j.Request, j.Estimate) {
		return result, digest, errors.New("任务记录身份或费用参考不一致")
	}
	return result, digest, nil
}

func (m *Manager) loadArchiveIndex() error {
	files, err := os.ReadDir(m.archiveDir)
	if err != nil {
		return err
	}
	for _, file := range files {
		match := archiveFilename.FindStringSubmatch(file.Name())
		if match == nil {
			continue
		}
		at, err := time.Parse(archiveClockLayout, match[1])
		if err != nil {
			continue
		}
		if len(m.archives) >= maxArchivedRecords {
			return errors.New("归档记录超过当前20000份上限，请保留资料后核对")
		}
		if err = m.reserveID(match[2]); err != nil {
			return err
		}
		m.archives[match[2]] = archivedFile{name: file.Name(), at: at}
	}
	return nil
}

func archiveEligible(job Job) bool {
	if job.CanResume {
		return false
	}
	return job.Status == Succeeded || job.Status == Failed || job.Status == Cancelled || job.Status == Unknown && job.Acknowledged
}

func (m *Manager) archivedViewLocked(id string, file archivedFile) ArchivedRecord {
	view := ArchivedRecord{ID: id, ArchivedAt: file.at}
	r, _, err := readRecord(filepath.Join(m.archiveDir, file.name), id)
	if err != nil {
		view.Issue = err.Error() + "；原请求标识仍保留，不会重新提交"
		return view
	}
	job := cloneJob(r.Job)
	view.Job = &job
	view.CanRestore = archiveEligible(job)
	if !view.CanRestore {
		view.Issue = "归档记录并非已核对的结束状态，保持原记录"
	}
	return view
}

func (m *Manager) ListArchived(ctx context.Context, input ArchivePageInput) (ArchivePage, error) {
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Offset < 0 || input.Limit < 1 || input.Limit > 50 {
		return ArchivePage{}, errors.New("归档分页参数无效")
	}
	if err := ctx.Err(); err != nil {
		return ArchivePage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ArchivePage{}, errors.New("生成任务服务已关闭")
	}
	ids := make([]string, 0, len(m.archives))
	for id := range m.archives {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return m.archives[ids[i]].name > m.archives[ids[j]].name })
	offset := input.Offset
	if offset >= len(ids) {
		offset = 0
		if len(ids) > 0 {
			offset = ((len(ids) - 1) / input.Limit) * input.Limit
		}
	}
	page := ArchivePage{Items: []ArchivedRecord{}, Total: len(ids), Active: len(m.entries), Offset: offset, Limit: input.Limit}
	end := offset + input.Limit
	if end > len(ids) {
		end = len(ids)
	}
	for _, id := range ids[offset:end] {
		if err := ctx.Err(); err != nil {
			return ArchivePage{}, err
		}
		page.Items = append(page.Items, m.archivedViewLocked(id, m.archives[id]))
	}
	return page, nil
}

func (m *Manager) Archive(ctx context.Context, id string) (ArchivedRecord, error) {
	if err := ctx.Err(); err != nil {
		return ArchivedRecord{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ArchivedRecord{}, errors.New("生成任务服务已关闭")
	}
	if file, ok := m.archives[id]; ok {
		return m.archivedViewLocked(id, file), nil
	}
	e, ok := m.entries[id]
	if !ok || e.corrupt || e.active || !archiveEligible(e.record.Job) {
		return ArchivedRecord{}, errors.New("只能归档已收尾且无需继续恢复的结束任务；未知请求需先核对")
	}
	if len(m.archives) >= maxArchivedRecords {
		return ArchivedRecord{}, errors.New("归档区已达到20000份上限，原记录保持")
	}
	source := filepath.Join(m.journal, id+".json")
	stored, digest, err := readRecord(source, id)
	if err != nil || digest != e.persistedSHA {
		return ArchivedRecord{}, errors.New("任务记录已变化，未归档；请保留资料并刷新核对")
	}
	// 只写回启动时规范化的任务状态；普通终态直接移动，保留完整原始记录字节。
	storedJob, storedErr := json.Marshal(stored.Job)
	currentJob, currentErr := json.Marshal(e.record.Job)
	if storedErr != nil || currentErr != nil {
		return ArchivedRecord{}, errors.New("无法核对当前任务状态")
	}
	if !bytes.Equal(storedJob, currentJob) {
		if err = m.saveLocked(e); err != nil {
			return ArchivedRecord{}, err
		}
	}
	file := archivedFile{at: time.Now().UTC()}
	file.name = file.at.Format(archiveClockLayout) + "-" + id + ".json"
	target := filepath.Join(m.archiveDir, file.name)
	if _, err = os.Lstat(target); !os.IsNotExist(err) {
		return ArchivedRecord{}, errors.New("归档目标已存在或不可核对，原记录保持")
	}
	if err = ctx.Err(); err != nil {
		return ArchivedRecord{}, err
	}
	if err = os.Rename(source, target); err != nil {
		return ArchivedRecord{}, errors.New("归档移动失败，原请求标识与任务保持")
	}
	m.archives[id] = file
	delete(m.entries, id)
	return m.archivedViewLocked(id, file), nil
}

func (m *Manager) RestoreArchived(ctx context.Context, id string) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Job{}, errors.New("生成任务服务已关闭")
	}
	if e, ok := m.entries[id]; ok {
		return cloneJob(e.record.Job), nil
	}
	file, ok := m.archives[id]
	if !ok {
		return Job{}, errors.New("找不到归档记录")
	}
	if len(m.entries) >= maxActiveRecords {
		return Job{}, errors.New("默认历史已满，请先归档其他结束任务再还原")
	}
	source := filepath.Join(m.archiveDir, file.name)
	r, digest, err := readRecord(source, id)
	if err != nil || !archiveEligible(r.Job) {
		return Job{}, errors.New("归档身份或结束状态无法确认，原记录和标识保持")
	}
	target := filepath.Join(m.journal, id+".json")
	if _, err = os.Lstat(target); !os.IsNotExist(err) {
		return Job{}, errors.New("默认区已有同名记录或不可核对，未覆盖")
	}
	if err = ctx.Err(); err != nil {
		return Job{}, err
	}
	if err = os.Rename(source, target); err != nil {
		return Job{}, errors.New("还原移动失败，归档记录保持")
	}
	e := &entry{record: r, persistedSHA: digest}
	if e.record.Job.Assets == nil {
		e.record.Job.Assets = []generation.ImportedAsset{}
	}
	if err = m.validateAssets(&e.record.Job); err != nil {
		e.record.Job.Status, e.record.Job.Message, e.record.Job.CanResume = Unknown, fmt.Sprintf("已有生成结果需要核对：%s", err.Error()), false
	}
	m.entries[id] = e
	delete(m.archives, id)
	return cloneJob(e.record.Job), nil
}
