package workflows

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPresetPersistenceOptimisticVersionAndCopyIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	store, err := NewPresetStore(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.Save(context.Background(), PresetInput{Name: "口播保守参数", WorkflowID: "talking-head", Parameters: map[string]any{"gapThresholdMs": 1200}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != 1 || p.WorkflowVersion != "1.0.0" {
		t.Fatal("预设版本没有固定工作流兼容范围")
	}
	p.Parameters["gapThresholdMs"] = 99999
	loaded, err := store.Get(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := numeric(loaded.Parameters["gapThresholdMs"]); value != 1200 {
		t.Fatal("修改公开预设影响内部状态")
	}
	restarted, err := NewPresetStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(restarted.List()) != 1 {
		t.Fatal("重开丢失预设")
	}
	if restarted.List()[0].Parameters["rippleScope"] != "selection" {
		t.Fatal("旧预设被扩大为全轨")
	}
	all, err := restarted.Save(context.Background(), PresetInput{Name: "明确全轨闭合", WorkflowID: "talking-head", Parameters: map[string]any{"rippleScope": "all_tracks"}})
	if err != nil || all.Parameters["rippleScope"] != "all_tracks" {
		t.Fatal("全轨选项不能保存为参数预设", err)
	}
	if _, err := restarted.Save(context.Background(), PresetInput{ID: p.ID, Name: "旧版本", WorkflowID: p.WorkflowID, BaseVersion: 0}); err == nil {
		t.Fatal("旧版本覆盖预设")
	}
	next, err := restarted.Save(context.Background(), PresetInput{ID: p.ID, Name: "新参数", WorkflowID: p.WorkflowID, BaseVersion: 1, Parameters: map[string]any{"keepGapMs": 500}})
	if err != nil || next.Version != 2 {
		t.Fatal("乐观更新失败", err)
	}
	if err := restarted.Delete(context.Background(), p.ID, 1); err == nil {
		t.Fatal("旧版本删除预设")
	}
	if err := restarted.Delete(context.Background(), p.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Delete(context.Background(), "talking-head", 1); err == nil {
		t.Fatal("允许删除内置工作流")
	}
}

func TestPresetValidationAndWriteFailureKeepOriginalState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	store, err := NewPresetStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(context.Background(), PresetInput{Name: "脚本", WorkflowID: "talking-head", Parameters: map[string]any{"shell": "rm -rf"}}); err == nil {
		t.Fatal("预设接收未声明脚本字段")
	}
	if _, err := store.Save(context.Background(), PresetInput{Name: "范围", WorkflowID: "talking-head", Parameters: map[string]any{"gapThresholdMs": -1}}); err == nil {
		t.Fatal("预设未校验参数范围")
	}
	p, err := store.Save(context.Background(), PresetInput{Name: "原始", WorkflowID: "captions"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(context.Background(), PresetInput{ID: p.ID, Name: "不能发布", WorkflowID: p.WorkflowID, BaseVersion: p.Version}); err == nil {
		t.Fatal("目录目标被覆盖")
	}
	current, err := store.Get(p.ID)
	if err != nil || current.Name != "原始" || current.Version != 1 {
		t.Fatal("失败发布改变内存版本", err)
	}
}

func TestPresetCancellationAndModelCompatibility(t *testing.T) {
	store, err := NewPresetStore(filepath.Join(t.TempDir(), "p.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Save(ctx, PresetInput{Name: "取消", WorkflowID: "captions"}); err == nil {
		t.Fatal("取消仍保存")
	}
	// 素材型预设可保持未选择状态，运行时才要求当前工程输入，便于跨工程复用。
	p, err := store.Save(context.Background(), PresetInput{Name: "配音布局", WorkflowID: "voiceover", Parameters: map[string]any{"startFrame": 90}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Parameters["assetId"] != "" {
		t.Fatal("伪造默认素材")
	}
}
