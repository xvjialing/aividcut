package application

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/media"
	"aividcut/internal/projectstore"
)

func newRelinkService(t *testing.T) *Service {
	t.Helper()
	service, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	return service
}

func copyRelinkFile(t *testing.T, source, name string) string {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRelinkRealMovedFileRestoresLockedSourceAndPersists(t *testing.T) {
	s := newRelinkService(t)
	original := fixtureImage(t)
	imported, err := s.ImportPaths(context.Background(), []string{original})
	if err != nil {
		t.Fatal(err)
	}
	asset := imported.Project.Assets[0]
	clip := editor.DefaultClip("used-image", "track-video-1", "image")
	clip.AssetID = asset.ID
	track := imported.Project.Tracks[2]
	track.Locked = true
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: imported.Project.Revision, IdempotencyKey: "place-and-lock", Commands: []editor.Command{{Type: "add_clip", Clip: &clip}, {Type: "update_track", Track: &track}}}); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(t.TempDir(), "离线工程.aividcut")
	if _, err := s.SaveProjectTo(projectPath); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "重新命名的相同图像.png")
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	opened, err := s.OpenProjectPath(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if !opened.Project.Assets[0].Missing {
		t.Fatal("没有识别已搬走的素材")
	}
	old := s.engine.Snapshot()
	restored, err := s.RelinkAsset(context.Background(), asset.ID, moved)
	if err != nil {
		t.Fatal(err)
	}
	updated := restored.Project.Assets[0]
	if updated.ID != asset.ID || updated.Name != asset.Name || updated.Fingerprint != asset.Fingerprint || updated.Path != moved || updated.Missing || !restored.Dirty || !restored.CanUndo || !restored.Project.Tracks[2].Locked || !reflect.DeepEqual(restored.Project.Clips, old.Clips) {
		t.Fatalf("重定位改变了身份、编排或元数据：%+v", restored)
	}
	response, err := http.Get(updated.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("重定位没有更新受限素材地址")
	}
	undone, err := s.Undo()
	if err != nil || undone.Project.Assets[0].Path != original || !undone.Project.Assets[0].Missing {
		t.Fatal("重定位不能撤销到缺失状态", err)
	}
	if _, err := s.Redo(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProject(); err != nil {
		t.Fatal(err)
	}
	reopened := newRelinkService(t)
	state, err := reopened.OpenProjectPath(projectPath)
	if err != nil || state.Project.Assets[0].Path != moved || state.Project.Assets[0].Missing || state.Dirty {
		t.Fatalf("保存重开丢失重定位结果：%v", err)
	}
}

func TestRelinkFingerprintMismatchAndCancelledReadLeaveProjectUnchanged(t *testing.T) {
	s := newRelinkService(t)
	path := fixtureImage(t)
	state, err := s.ImportPaths(context.Background(), []string{path})
	if err != nil {
		t.Fatal(err)
	}
	different := copyRelinkFile(t, path, "内容不同.png")
	file, _ := os.OpenFile(different, os.O_APPEND|os.O_WRONLY, 0600)
	file.WriteString("different-content")
	file.Close()
	before := s.engine.Snapshot()
	if _, err := s.RelinkAsset(context.Background(), state.Project.Assets[0].ID, different); err == nil || !strings.Contains(err.Error(), "指纹") {
		t.Fatalf("不同内容冒充重定位：%v", err)
	}
	if !reflect.DeepEqual(before, s.engine.Snapshot()) {
		t.Fatal("指纹失败后仍修改工程")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.RelinkAsset(ctx, state.Project.Assets[0].ID, path); !errors.Is(err, context.Canceled) {
		t.Fatal("忽略了重定位取消", err)
	}
	if !reflect.DeepEqual(before, s.engine.Snapshot()) {
		t.Fatal("取消后仍修改工程")
	}
}

func legacyAudioProject(t *testing.T, s *Service, locked bool) editor.Asset {
	t.Helper()
	p := editor.NewProject("无指纹旧工程")
	asset := editor.Asset{ID: "legacy-audio", Name: "保留的声音名称", Kind: "audio", Path: filepath.Join(t.TempDir(), "missing.wav"), DurationUs: 20_000_000, HasAudio: true, Missing: true}
	p.Assets = []editor.Asset{asset}
	clip := editor.DefaultClip("legacy-speech", "track-audio-1", "audio")
	clip.AssetID, clip.DurationFrames = asset.ID, 30
	p.Clips = []editor.Clip{clip}
	p.Tracks[3].Locked = locked
	path := filepath.Join(t.TempDir(), "legacy.aividcut")
	if err := projectstore.Save(path, p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenProjectPath(path); err != nil {
		t.Fatal(err)
	}
	return asset
}

// 无外部工具时使用真实 PCM WAV 内容及确定的探测 fixture 验证应用边界，不冒充 ffprobe 实机验收。
func relinkWAV(t *testing.T, durationUs int64) string {
	t.Helper()
	size := int(durationUs * 16000 / 1_000_000)
	data := make([]byte, 44+size)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(36+size))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 8000)
	binary.LittleEndian.PutUint32(data[28:], 16000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(size))
	path := filepath.Join(t.TempDir(), "实际声音.wav")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func wavProbeFixture(ctx context.Context, path string) (editor.Asset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return editor.Asset{}, err
	}
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return editor.Asset{}, errors.New("不是 WAV fixture")
	}
	rate := binary.LittleEndian.Uint32(data[28:])
	size := binary.LittleEndian.Uint32(data[40:])
	if rate == 0 {
		return editor.Asset{}, errors.New("WAV fixture 字节率无效")
	}
	hash, err := fingerprintRelinkFile(ctx, path)
	if err != nil {
		return editor.Asset{}, err
	}
	return editor.Asset{ID: "probe-new-id", Name: filepath.Base(path), Kind: "audio", Path: path, DurationUs: int64(size) * 1_000_000 / int64(rate), HasAudio: true, Fingerprint: hash}, nil
}

func TestLegacyRelinkChecksActualTypeSourceRangeAndLockedReferences(t *testing.T) {
	s := newRelinkService(t)
	asset := legacyAudioProject(t, s, false)
	before := s.engine.Snapshot()
	if _, err := s.RelinkAsset(context.Background(), asset.ID, fixtureImage(t)); err == nil || !strings.Contains(err.Error(), "类型") {
		t.Fatalf("旧工程允许不同媒体类型：%v", err)
	}
	if _, err := s.relinkAssetWithProbe(context.Background(), asset.ID, relinkWAV(t, 500_000), wavProbeFixture); err == nil || !strings.Contains(err.Error(), "范围") {
		t.Fatalf("短源素材没有检查已有片段：%v", err)
	}
	if !reflect.DeepEqual(before, s.engine.Snapshot()) {
		t.Fatal("失败重定位部分提交")
	}
	locked := before.Tracks[3]
	locked.Locked = true
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: before.Revision, IdempotencyKey: "lock-legacy", Commands: []editor.Command{{Type: "update_track", Track: &locked}}}); err != nil {
		t.Fatal(err)
	}
	good := relinkWAV(t, 2_000_000)
	if _, err := s.relinkAssetWithProbe(context.Background(), asset.ID, good, wavProbeFixture); err == nil || !strings.Contains(err.Error(), "锁定") {
		t.Fatalf("旧无指纹源绕过锁轨：%v", err)
	}
	locked.Locked = false
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: s.engine.Snapshot().Revision, IdempotencyKey: "unlock-legacy", Commands: []editor.Command{{Type: "update_track", Track: &locked}}}); err != nil {
		t.Fatal(err)
	}
	state, err := s.relinkAssetWithProbe(context.Background(), asset.ID, good, wavProbeFixture)
	if err != nil || state.Project.Assets[0].ID != asset.ID || state.Project.Assets[0].Name != asset.Name || state.Project.Assets[0].Fingerprint == "" || state.Project.Assets[0].DurationUs != 2_000_000 {
		t.Fatalf("旧工程恢复后元数据不完整：%v", err)
	}
}

func TestRelinkLUTValidatesCubeAndKnownHashWorksOnLockedDependency(t *testing.T) {
	s := newRelinkService(t)
	cube := "LUT_3D_SIZE 2\n0 0 0\n0 0 1\n0 1 0\n0 1 1\n1 0 0\n1 0 1\n1 1 0\n1 1 1\n"
	old := filepath.Join(t.TempDir(), "风格.cube")
	os.WriteFile(old, []byte(cube), 0600)
	state, err := s.ImportPaths(context.Background(), []string{old, fixtureImage(t)})
	if err != nil {
		t.Fatal(err)
	}
	p := state.Project
	hash, _ := fingerprintRelinkFile(context.Background(), old)
	p.Assets[0].Fingerprint = hash
	clip := editor.DefaultClip("graded", "track-video-1", "image")
	clip.AssetID = p.Assets[1].ID
	clip.LUT = &editor.LUT{AssetID: p.Assets[0].ID, Intensity: .6}
	p.Clips = []editor.Clip{clip}
	p.Tracks[2].Locked = true
	path := filepath.Join(t.TempDir(), "调色.aividcut")
	if err := projectstore.Save(path, p); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "same-look.cube")
	if err := os.Rename(old, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenProjectPath(path); err != nil {
		t.Fatal(err)
	}
	restored, err := s.RelinkAsset(context.Background(), p.Assets[0].ID, moved)
	if err != nil || restored.Project.Assets[0].Path != moved || restored.Project.Assets[0].Fingerprint != hash || !restored.Project.Tracks[2].Locked {
		t.Fatalf("锁定调色依赖无法纯重定位：%v", err)
	}
	invalid := filepath.Join(t.TempDir(), "invalid.cube")
	os.WriteFile(invalid, []byte("LUT_3D_SIZE 2\n0 0 0"), 0600)
	if _, err := s.RelinkAsset(context.Background(), p.Assets[0].ID, invalid); err == nil {
		t.Fatal("无效 Cube 被重定位接受")
	}
}

type relinkOutcome struct {
	state Bootstrap
	err   error
}

func pausedRelink(t *testing.T, s *Service, assetID, path string, actual relinkProbeFunc) (func(), <-chan relinkOutcome) {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan relinkOutcome, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	go func() {
		state, err := s.relinkAssetWithProbe(context.Background(), assetID, path, func(ctx context.Context, path string) (editor.Asset, error) {
			value, err := actual(ctx, path)
			if err != nil {
				return value, err
			}
			close(entered)
			<-release
			return value, nil
		})
		result <- relinkOutcome{state, err}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("重定位没有进入可控读取阶段")
	}
	return unblock, result
}

func awaitRelink(t *testing.T, result <-chan relinkOutcome) relinkOutcome {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("重定位没有完成")
		return relinkOutcome{}
	}
}

func TestRelinkRejectsProjectReopenEvenWhenProjectIDIsSame(t *testing.T) {
	s := newRelinkService(t)
	source := fixtureImage(t)
	state, err := s.ImportPaths(context.Background(), []string{source})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "工程.aividcut")
	if _, err := s.SaveProjectTo(path); err != nil {
		t.Fatal(err)
	}
	release, result := pausedRelink(t, s, state.Project.Assets[0].ID, copyRelinkFile(t, source, "moved.png"), s.tools.Probe)
	reopened, err := s.OpenProjectPath(path)
	if err != nil {
		t.Fatal(err)
	}
	release()
	value := awaitRelink(t, result)
	if value.err == nil || !strings.Contains(value.err.Error(), "切换") {
		t.Fatalf("相同ID的新工程实例被旧读取结果覆盖：%v", value.err)
	}
	if !reflect.DeepEqual(s.GetBootstrap().Project, reopened.Project) {
		t.Fatal("拒绝过期结果后仍修改重开的工程")
	}
}

func TestRelinkCommitsAtLatestRevisionAndPreservesAdditiveImport(t *testing.T) {
	s := newRelinkService(t)
	source := fixtureImage(t)
	state, err := s.ImportPaths(context.Background(), []string{source})
	if err != nil {
		t.Fatal(err)
	}
	moved := copyRelinkFile(t, source, "moved.png")
	release, result := pausedRelink(t, s, state.Project.Assets[0].ID, moved, s.tools.Probe)
	added, err := s.ImportPaths(context.Background(), []string{fixtureImage(t)})
	if err != nil {
		t.Fatal(err)
	}
	release()
	value := awaitRelink(t, result)
	if value.err != nil || len(value.state.Project.Assets) != 2 || value.state.Project.Revision != added.Project.Revision+1 || value.state.Project.Assets[0].Path != moved {
		t.Fatalf("同工程追加素材后丢失变更或错误冲突：%v", value.err)
	}
}

func TestRelinkValidatesReferencesAddedWhileReading(t *testing.T) {
	s := newRelinkService(t)
	asset := legacyAudioProject(t, s, false)
	release, result := pausedRelink(t, s, asset.ID, relinkWAV(t, 2_000_000), wavProbeFixture)
	p := s.engine.Snapshot()
	later := editor.DefaultClip("added-reference", "track-audio-1", "audio")
	later.AssetID, later.StartFrame, later.DurationFrames, later.SourceInUs = asset.ID, 60, 30, 10_000_000
	current, err := s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "new-source-use", Commands: []editor.Command{{Type: "add_clip", Clip: &later}}})
	if err != nil {
		t.Fatal(err)
	}
	release()
	value := awaitRelink(t, result)
	if value.err == nil {
		t.Fatal("重定位只校验读取前的引用，没有检查新增源区间")
	}
	if !reflect.DeepEqual(s.GetBootstrap().Project, current.Project) {
		t.Fatal("重定位失败覆盖了读取期间新增片段")
	}
}

func TestRelinkRejectsConcurrentAssetUpdate(t *testing.T) {
	s := newRelinkService(t)
	source := fixtureImage(t)
	state, err := s.ImportPaths(context.Background(), []string{source})
	if err != nil {
		t.Fatal(err)
	}
	release, result := pausedRelink(t, s, state.Project.Assets[0].ID, copyRelinkFile(t, source, "first.png"), s.tools.Probe)
	winner := copyRelinkFile(t, source, "winner.png")
	current, err := s.RelinkAsset(context.Background(), state.Project.Assets[0].ID, winner)
	if err != nil {
		t.Fatal(err)
	}
	release()
	value := awaitRelink(t, result)
	if value.err == nil || !strings.Contains(value.err.Error(), "已被修改") {
		t.Fatalf("旧读取覆盖了另一次重定位：%v", value.err)
	}
	if s.GetBootstrap().Project.Assets[0].Path != current.Project.Assets[0].Path {
		t.Fatal("并发重定位结果被覆盖")
	}
}

func TestPublicAssetEditsStayBehindValidatedBoundary(t *testing.T) {
	s := newRelinkService(t)
	for _, kind := range []string{"add_asset", "update_asset"} {
		_, err := s.ApplyEdit(editor.EditRequest{BaseRevision: 0, IdempotencyKey: fmt.Sprintf("unsafe-%s", kind), Commands: []editor.Command{{Type: kind, Asset: &editor.Asset{ID: "unsafe", Path: "/unvalidated"}}}})
		if err == nil {
			t.Fatal("公共编辑入口允许未验证素材路径")
		}
	}
}
