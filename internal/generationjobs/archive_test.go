package generationjobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/generation"
)

func archiveFixtureProvider() *fixtureProvider {
	return &fixtureProvider{submit: func(_ context.Context, r generation.Request) (generation.Task, error) {
		task := taskFor(r, generation.Succeeded, "")
		task.Outputs = []generation.Output{imageOutput()}
		return task, nil
	}}
}

func TestArchiveRestorePreservesMediaQuoteAndPaidIDAcrossRestart(t *testing.T) {
	p := archiveFixtureProvider()
	m, options := newFixture(t, t.TempDir(), p)
	project := editor.NewProject("归档媒体")
	in := inputFor("archive-original")
	in.Estimate = &EstimateInput{Currency: "CNY", Unit: "image", UnitPrice: "0.29"}
	if _, err := m.Submit(context.Background(), project, in); err != nil {
		t.Fatal(err)
	}
	job := awaitIdle(t, m, in.Request.ID, Succeeded)
	media, err := os.ReadFile(job.Assets[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(m.journal, job.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	view, err := m.Archive(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !view.CanRestore || view.Job.Estimate.Amount != "0.29" || len(m.List()) != 0 {
		t.Fatalf("归档状态不符：%+v", view)
	}
	archived, err := os.ReadFile(filepath.Join(m.archiveDir, m.archives[job.ID].name))
	if err != nil || !bytes.Equal(before, archived) {
		t.Fatal("归档改变已结束记录字节", err)
	}
	if _, err = m.Get(job.ID); err == nil {
		t.Fatal("归档记录仍出现在默认读取")
	}
	if _, err = m.Submit(context.Background(), project, in); err == nil {
		t.Fatal("归档ID重新提交")
	}
	if _, err = m.Archive(context.Background(), job.ID); err != nil {
		t.Fatal("重复归档不是幂等", err)
	}
	m.Close()
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err = restored.Submit(context.Background(), project, in); err == nil {
		t.Fatal("重启遗忘归档ID")
	}
	page, err := restored.ListArchived(context.Background(), ArchivePageInput{})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Job.Estimate.Amount != "0.29" {
		t.Fatalf("重开丢失归档：%+v / %v", page, err)
	}
	got, err := restored.RestoreArchived(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != job.ID || got.Estimate.Amount != "0.29" || got.Assets[0].Path != job.Assets[0].Path {
		t.Fatal("还原改变身份或素材位置")
	}
	if _, err = restored.RestoreArchived(context.Background(), job.ID); err != nil {
		t.Fatal("重复还原不是幂等", err)
	}
	if _, err = restored.Submit(context.Background(), project, in); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(job.Assets[0].Path)
	if err != nil || !bytes.Equal(media, after) {
		t.Fatal("媒体字节改变", err)
	}
	if p.posts.Load() != 1 {
		t.Fatalf("归档/还原导致重复调用：%d", p.posts.Load())
	}
}

func seedFailedRecords(t *testing.T, dir string, count int) {
	t.Helper()
	journal := filepath.Join(dir, "jobs")
	if err := os.MkdirAll(journal, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		r := inputFor(fmt.Sprintf("quota-%04d", i)).Request
		r.Count = 1
		now := time.Now().UTC()
		value := record{Version: 1, Job: Job{ID: r.ID, ConnectionID: "fixture-connection", ProjectID: "quota-project", Request: r, Status: Failed, CreatedAt: now, UpdatedAt: now, Assets: []generation.ImportedAsset{}}, Signature: signature("fixture-connection", "quota-project", r), Task: taskFor(r, generation.Failed, "")}
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(journal, r.ID+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestArchiveActuallyFrees2000RecordSlotAndRestoreRespectsCapacity(t *testing.T) {
	dir := t.TempDir()
	seedFailedRecords(t, dir, maxActiveRecords)
	p := archiveFixtureProvider()
	m, _ := newFixture(t, dir, p)
	project := editor.NewProject("容量")
	in := inputFor("after-full")
	if _, err := m.Submit(context.Background(), project, in); err == nil {
		t.Fatal("2000上限未拒绝新请求")
	}
	if p.posts.Load() != 0 {
		t.Fatal("容量拒绝前调用了生成")
	}
	if _, err := m.Archive(context.Background(), "quota-0000"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(context.Background(), project, in); err != nil {
		t.Fatal("归档没有释放名额", err)
	}
	awaitIdle(t, m, in.Request.ID, Succeeded)
	if _, err := m.RestoreArchived(context.Background(), "quota-0000"); err == nil {
		t.Fatal("还原突破默认区容量")
	}
	if _, err := m.Archive(context.Background(), "quota-0001"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RestoreArchived(context.Background(), "quota-0000"); err != nil {
		t.Fatal(err)
	}
	if len(m.List()) != maxActiveRecords || p.posts.Load() != 1 {
		t.Fatal("容量或提交次数异常")
	}
}

func TestArchiveKeepsUnconfirmedAndActiveRequestsVisible(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	p := &fixtureProvider{submit: func(ctx context.Context, r generation.Request) (generation.Task, error) {
		close(entered)
		select {
		case <-finish:
		case <-ctx.Done():
			return generation.Task{}, ctx.Err()
		}
		task := taskFor(r, generation.Unknown, "")
		task.SubmissionUncertain = true
		return task, &generation.Error{Code: "transport", Message: "fixture", SubmissionUncertain: true}
	}}
	m, _ := newFixture(t, t.TempDir(), p)
	in := inputFor("needs-review")
	project := editor.NewProject("核对")
	if _, err := m.Submit(context.Background(), project, in); err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := m.Archive(context.Background(), in.Request.ID); err == nil {
		t.Fatal("运行任务被隐藏")
	}
	close(finish)
	awaitIdle(t, m, in.Request.ID, Unknown)
	if _, err := m.Archive(context.Background(), in.Request.ID); err == nil {
		t.Fatal("未核对未知任务被隐藏")
	}
	if _, err := m.AcknowledgeUnknown(in.Request.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Archive(context.Background(), in.Request.ID); err != nil {
		t.Fatal(err)
	}
	got, err := m.RestoreArchived(context.Background(), in.Request.ID)
	if err != nil || got.Status != Unknown || !got.Acknowledged {
		t.Fatal("还原改变未知/核对状态", err)
	}
	if p.posts.Load() != 1 {
		t.Fatal("核对后归档重交了请求")
	}
}

func TestArchivePaginationCorruptionAndCaseConflictReserveIDs(t *testing.T) {
	dir := t.TempDir()
	seedFailedRecords(t, dir, 5)
	p := archiveFixtureProvider()
	m, options := newFixture(t, dir, p)
	for i := 0; i < 5; i++ {
		if _, err := m.Archive(context.Background(), fmt.Sprintf("quota-%04d", i)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := m.ListArchived(context.Background(), ArchivePageInput{Offset: 4, Limit: 2})
	if err != nil || page.Offset != 4 || len(page.Items) != 1 {
		t.Fatalf("分页不符：%+v %v", page, err)
	}
	last := page.Items[0].ID
	if _, err = m.RestoreArchived(context.Background(), last); err != nil {
		t.Fatal(err)
	}
	page, err = m.ListArchived(context.Background(), ArchivePageInput{Offset: 4, Limit: 2})
	if err != nil || page.Offset != 2 || len(page.Items) != 2 {
		t.Fatal("空末页没有收敛", page, err)
	}
	id := "quota-0004"
	path := filepath.Join(m.archiveDir, m.archives[id].name)
	if err = os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	m.Close()
	reopened, err := New(options)
	if err != nil {
		t.Fatal("启动不应解析全部归档内容", err)
	}
	defer reopened.Close()
	page, err = reopened.ListArchived(context.Background(), ArchivePageInput{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range page.Items {
		if row.ID == id {
			found = true
			if row.CanRestore || row.Issue == "" {
				t.Fatal("损坏记录伪装正常")
			}
		}
	}
	if !found {
		t.Fatal("损坏记录被遗忘")
	}
	if _, err = reopened.RestoreArchived(context.Background(), id); err == nil {
		t.Fatal("损坏归档可还原")
	}
	in := inputFor(id)
	if _, err = reopened.Submit(context.Background(), editor.NewProject("不应调用"), in); err == nil {
		t.Fatal("损坏归档允许重复提交")
	}
	in.Request.ID = "QUOTA-0004"
	if _, err = reopened.Submit(context.Background(), editor.NewProject("大小写"), in); err == nil {
		t.Fatal("大小写变体绕过去重")
	}
	if p.posts.Load() != 0 {
		t.Fatal("归档管理调用了提供商")
	}
}

func TestArchiveTamperAndMoveFailureKeepOriginalRecord(t *testing.T) {
	dir := t.TempDir()
	seedFailedRecords(t, dir, 1)
	m, _ := newFixture(t, dir, archiveFixtureProvider())
	id := "quota-0000"
	source := filepath.Join(m.journal, id+".json")
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(source, append(original, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Archive(context.Background(), id); err == nil {
		t.Fatal("外部修改未被核对")
	}
	if err = os.WriteFile(source, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(m.archiveDir); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(m.archiveDir, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Archive(context.Background(), id); err == nil {
		t.Fatal("无效归档目标仍成功")
	}
	if _, err = m.Get(id); err != nil {
		t.Fatal("失败后记录从默认区消失")
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("失败后记录改变", err)
	}
	if len(m.archives) != 0 {
		t.Fatal("失败后创建了归档索引")
	}
}

func TestArchiveRejectsCaseCollisionsAcrossStores(t *testing.T) {
	dir := t.TempDir()
	seedFailedRecords(t, dir, 1)
	m, options := newFixture(t, dir, archiveFixtureProvider())
	if _, err := m.Archive(context.Background(), "quota-0000"); err != nil {
		t.Fatal(err)
	}
	m.Close()
	seedFailedRecords(t, dir, 1)
	if reopened, err := New(options); err == nil {
		reopened.Close()
		t.Fatal("同一ID在两区出现仍被接受")
	}
}

func TestArchiveRejectsCancelledContextBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	seedFailedRecords(t, dir, 1)
	m, _ := newFixture(t, dir, archiveFixtureProvider())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Archive(ctx, "quota-0000"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := m.Get("quota-0000"); err != nil {
		t.Fatal(err)
	}
}

func TestArchivePersistsStartupCancelledQueueBeforeMoving(t *testing.T) {
	dir := t.TempDir()
	seedFailedRecords(t, dir, 1)
	path := filepath.Join(dir, "jobs", "quota-0000.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value record
	if err = json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	value.Job.Status = Queued
	data, _ = json.Marshal(value)
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	p := archiveFixtureProvider()
	m, _ := newFixture(t, dir, p)
	job, err := m.Get("quota-0000")
	if err != nil || job.Status != Cancelled {
		t.Fatal("启动未规范化本地队列", err)
	}
	view, err := m.Archive(context.Background(), job.ID)
	if err != nil || !view.CanRestore || view.Job.Status != Cancelled {
		t.Fatal("归档没有保存取消终态", err)
	}
	got, err := m.RestoreArchived(context.Background(), job.ID)
	if err != nil || got.Status != Cancelled || p.posts.Load() != 0 {
		t.Fatal("还原取消队列导致重交", err)
	}
}

func TestConcurrentArchiveAndRestoreMoveOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	seedFailedRecords(t, dir, 1)
	p := archiveFixtureProvider()
	m, _ := newFixture(t, dir, p)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.Archive(context.Background(), "quota-0000"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(m.archives) != 1 || len(m.entries) != 0 {
		t.Fatal("并发归档产生多份或丢失记录")
	}
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.RestoreArchived(context.Background(), "quota-0000"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(m.archives) != 0 || len(m.entries) != 1 || p.posts.Load() != 0 {
		t.Fatal("并发还原改变记录或触发生成")
	}
}

func TestArchiveIndexCapacityIsBoundedWithoutReadingBodies(t *testing.T) {
	dir := t.TempDir()
	seedFailedRecords(t, dir, 1)
	archive := filepath.Join(dir, "archive")
	if err := os.MkdirAll(archive, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxArchivedRecords; i++ {
		name := fmt.Sprintf("20260101T000000.000000000-reserved-%05d.json", i)
		if err := os.WriteFile(filepath.Join(archive, name), []byte("{"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m, _ := newFixture(t, dir, archiveFixtureProvider())
	if len(m.archives) != maxArchivedRecords {
		t.Fatal("索引数量不符")
	}
	if _, err := m.Archive(context.Background(), "quota-0000"); err == nil {
		t.Fatal("归档容量未保护")
	}
	if _, err := m.Get("quota-0000"); err != nil {
		t.Fatal("容量拒绝移除了默认任务")
	}
	page, err := m.ListArchived(context.Background(), ArchivePageInput{})
	if err != nil || len(page.Items) != 20 || page.Total != maxArchivedRecords {
		t.Fatal("分页超出上限", err)
	}
}

func TestArchivedSymlinkCannotReadOrReplaceOutsideFile(t *testing.T) {
	dir := t.TempDir()
	seedFailedRecords(t, dir, 1)
	m, _ := newFixture(t, dir, archiveFixtureProvider())
	if _, err := m.Archive(context.Background(), "quota-0000"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.archiveDir, m.archives["quota-0000"].name)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	want := []byte("outside-original")
	if err := os.WriteFile(outside, want, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skip("当前环境无法创建符号链接", err)
	}
	if _, err := m.RestoreArchived(context.Background(), "quota-0000"); err == nil {
		t.Fatal("归档符号链接仍可还原")
	}
	page, err := m.ListArchived(context.Background(), ArchivePageInput{})
	if err != nil || page.Items[0].CanRestore {
		t.Fatal("归档符号链接被当作普通记录", err)
	}
	got, err := os.ReadFile(outside)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("操作影响外部文件", err)
	}
}
