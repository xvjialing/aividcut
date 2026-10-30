package generationjobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"aividcut/internal/generation"
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (m *Manager) saveLocked(e *entry) error {
	data, err := json.Marshal(e.record)
	if err != nil {
		return errors.New("无法编码生成任务状态")
	}
	if len(data) > 2<<20 {
		return errors.New("生成任务状态超过保存上限")
	}
	file, err := os.CreateTemp(m.journal, ".generation-state-*")
	if err != nil {
		return errors.New("无法保存生成任务状态，请检查应用数据目录")
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return errors.New("生成任务状态写入失败")
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return errors.New("生成任务状态同步失败")
	}
	if err = file.Close(); err != nil {
		return errors.New("生成任务状态关闭失败")
	}
	if err = os.Rename(file.Name(), filepath.Join(m.journal, e.record.Job.ID+".json")); err != nil {
		return errors.New("生成任务状态提交失败")
	}
	sum := sha256.Sum256(data)
	e.persistedSHA = hex.EncodeToString(sum[:])
	return nil
}

func (m *Manager) load() error {
	files, err := os.ReadDir(m.journal)
	if err != nil {
		return err
	}
	count := 0
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(file.Name(), ".json")
		if !safeID.MatchString(id) {
			continue
		}
		count++
		if count > maxActiveRecords {
			return errors.New("生成任务记录超过当前读取上限，请先归档历史任务")
		}
		e := &entry{}
		p := filepath.Join(m.journal, file.Name())
		if err := m.reserveID(id); err != nil {
			return err
		}
		loaded, digest, openErr := readRecord(p, id)
		e.record, e.persistedSHA = loaded, digest
		j := &e.record.Job
		if openErr != nil {
			// 损坏的计费任务也占住请求ID，避免把它当作从未提交而再次扣费。
			now := time.Now().UTC()
			e.record = record{Version: 1, Job: Job{ID: id, Status: Unknown, Message: "任务记录损坏，无法自动恢复；请核对服务记录，不要重复提交", CreatedAt: now, UpdatedAt: now, Assets: []generation.ImportedAsset{}}}
			e.corrupt = true
			m.entries[id] = e
			continue
		}
		if j.Assets == nil {
			j.Assets = []generation.ImportedAsset{}
		}
		switch j.Status {
		case Queued:
			j.Status, j.Message, j.CanResume = Cancelled, "应用退出前尚未开始提交，已取消本地排队", false
		case Submitting, Running, Importing:
			if validTask(*j, e.record.Task) && (e.record.Task.RemoteID != "" || e.record.Task.Status == generation.Succeeded && !e.record.Task.SubmissionUncertain) {
				j.Status, j.Message, j.CanResume = Waiting, "上次等待已中断，可继续查询或导入已有结果", true
			} else {
				j.Status, j.Message, j.CanResume = Unknown, "上次提交结果未知，请先核对服务记录；不会自动重新生成", false
			}
		case Waiting, Unknown, Succeeded, Failed, Cancelled:
		default:
			j.Status, j.Message, j.CanResume = Unknown, "任务状态无法识别，请核对服务记录", false
		}
		if j.CanResume && (!validTask(*j, e.record.Task) || e.record.Task.RemoteID == "" && e.record.Task.Status != generation.Succeeded) {
			j.Status, j.Message, j.CanResume = Unknown, "任务缺少一致的恢复信息，请核对服务记录", false
		}
		if j.CanResume && e.record.Task.RemoteID == "" && e.record.Task.Status == generation.Succeeded {
			assets, found, recoverErr := m.importer.RecoverLocal(context.Background(), e.record.Task)
			if recoverErr == nil && found {
				j.Assets, j.Status, j.Message, j.CanResume = assets, Succeeded, "已有本地生成结果已恢复", false
			} else {
				j.Status, j.Message, j.CanResume = Unknown, "同步生成的返回内容未落盘且无法重新查询，请先核对服务记录", false
			}
		}
		if err := m.validateAssets(j); err != nil {
			j.Status, j.Message, j.CanResume = Unknown, fmt.Sprintf("已有生成结果需要核对：%s", err.Error()), false
		}
		m.entries[id] = e
	}
	return nil
}

func (m *Manager) validateAssets(job *Job) error {
	if len(job.Assets) > 10 {
		return errors.New("结果数量无效")
	}
	base := filepath.Join(m.assets, job.ID)
	for _, asset := range job.Assets {
		if !safeID.MatchString(asset.ID) || asset.RecipeID != job.ID || !filepath.IsAbs(asset.Path) || asset.Bytes <= 0 || len(asset.SHA256) != 64 {
			return errors.New("结果身份不一致")
		}
		rel, err := filepath.Rel(base, asset.Path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return errors.New("结果不在任务目录中")
		}
		info, err := os.Lstat(asset.Path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != asset.Bytes {
			return errors.New("本地结果缺失或大小已变化")
		}
		resolved, err := filepath.EvalSymlinks(asset.Path)
		if err != nil {
			return errors.New("本地结果路径无法解析")
		}
		rel, err = filepath.Rel(base, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return errors.New("本地结果路径已离开任务目录")
		}
	}
	return nil
}
