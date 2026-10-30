package mediaruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateManifestValidatesAndDoesNotOverwrite(t *testing.T) {
	root, spec := fixture(t)
	os.Remove(filepath.Join(root, Filename))
	spec.Payloads = nil
	r, e := CreateManifest(context.Background(), root, spec)
	if e != nil || r.Files != 5 || r.Fingerprint == "" {
		t.Fatal(r, e)
	}
	before, _ := os.ReadFile(filepath.Join(root, Filename))
	if _, e = CreateManifest(context.Background(), root, spec); e == nil {
		t.Fatal("不应覆盖已有清单")
	}
	after, _ := os.ReadFile(filepath.Join(root, Filename))
	if string(before) != string(after) {
		t.Fatal("已有清单被改写")
	}
}
func TestCreateManifestFailureLeavesPayloadAndNoPartialManifest(t *testing.T) {
	root, spec := fixture(t)
	os.Remove(filepath.Join(root, Filename))
	spec.Payloads = nil
	spec.Paths.Worker = "../worker"
	if _, e := CreateManifest(context.Background(), root, spec); e == nil {
		t.Fatal("无效入口不应发布")
	}
	if _, e := os.Stat(filepath.Join(root, Filename)); !os.IsNotExist(e) {
		t.Fatal("失败不应留下半成品清单")
	}
	if _, e := os.Stat(filepath.Join(root, "bin/worker")); e != nil {
		t.Fatal("失败改写了payload")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := CreateManifest(ctx, root, spec); e == nil {
		t.Fatal("取消未阻止生成")
	}
}

func TestManifestV2CarriesMLTDataAndKeepsV1Strict(t *testing.T) {
	root, spec := fixture(t)
	os.Remove(filepath.Join(root, Filename))
	spec.Payloads = nil
	spec.SchemaVersion = 2
	spec.Paths.Data = "profiles"
	r, e := CreateManifest(context.Background(), root, spec)
	if e != nil || r.Paths.Data != filepath.Join(root, "profiles") {
		t.Fatal(r, e)
	}
	data, _ := os.ReadFile(filepath.Join(root, Filename))
	var m Manifest
	if e = json.Unmarshal(data, &m); e != nil {
		t.Fatal(e)
	}
	m.SchemaVersion = 1
	write(t, root, m)
	if _, e = Load(context.Background(), filepath.Join(root, Filename), m.OS, m.Arch); e == nil {
		t.Fatal("旧格式不能夹带新数据目录语义")
	}
}
