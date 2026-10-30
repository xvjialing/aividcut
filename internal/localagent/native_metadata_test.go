package localagent

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// 此可选检查只运行版本、帮助、认证状态和有效配置诊断，不调用 Plan 或模型。
func TestNativeCodexMetadataOnly(t *testing.T) {
	if os.Getenv("AIVIDCUT_LOCALAGENT_METADATA_TEST") != "1" {
		t.Skip("未启用本机CLI只读检测；可设置 AIVIDCUT_LOCALAGENT_METADATA_TEST=1")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("本机未安装Codex")
	}
	planner, err := NewPlanner(Config{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	info := planner.Inspect(ctx, Codex)
	t.Logf("本机只读结果：版本=%s，登录=%s，隔离状态=%s", info.Version, info.AuthState, info.Isolation.Status)
	for _, check := range info.Isolation.Checks {
		t.Logf("%s: %s — %s", check.ID, check.Status, check.Reason)
	}
	if !info.Installed || info.Version == "" {
		t.Fatal("本机CLI元数据读取失败")
	}
	if info.Isolation.Supported && (info.AuthState != "signed_in" || info.Isolation.Status != "ready") {
		t.Fatal("隔离结果与登录状态不一致")
	}
}

func TestNativeCodexExecutionMetadataOnly(t *testing.T) {
	if os.Getenv("AIVIDCUT_LOCALAGENT_METADATA_TEST") != "1" {
		t.Skip("未启用本机执行契约元数据检查")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("未安装Codex")
	}
	planner, err := NewPlanner(Config{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	info := planner.InspectExecution(ctx, Codex)
	t.Logf("真实CLI执行配置：version=%s auth=%s status=%s", info.Version, info.AuthState, info.Isolation.Status)
	verified := false
	for _, check := range info.Isolation.Checks {
		t.Logf("%s: %s — %s", check.ID, check.Status, check.Reason)
		if check.ID == "execution_effective_config" && check.Status == "pass" {
			verified = true
		}
	}
	if info.Version == "0.164.0" && !verified {
		t.Fatal("固定版本执行模式没有取得单一MCP有效配置证据")
	}
}
