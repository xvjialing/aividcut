package application

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/mcpserver"
	"aividcut/internal/media"
	"aividcut/internal/projectrecovery"
	"aividcut/internal/projectstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type recoveryProcessReady struct {
	Connection mcpserver.ConnectionConfig `json:"connection"`
	ProjectID  string                     `json:"projectId"`
}

func TestRecoveryProcessHelper(t *testing.T) {
	dir := os.Getenv("AIVIDCUT_RECOVERY_HELPER_DIR")
	if dir == "" {
		return
	}
	s, err := New(media.Toolchain{}, filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ConfigureRecovery(RecoveryOptions{Directory: filepath.Join(dir, "recovery")}); err != nil {
		t.Fatal(err)
	}
	b, err := s.OpenProjectPath(filepath.Join(dir, "original.aividcut"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartMCP(MCPStartInput{ProjectID: b.Project.ID, AllowEdits: true}); err != nil {
		t.Fatal(err)
	}
	connection, err := s.GetMCPConnection()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(recoveryProcessReady{Connection: connection, ProjectID: b.Project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ready"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, ".ready"), filepath.Join(dir, "ready.json")); err != nil {
		t.Fatal(err)
	}
	select {}
}

func TestRecoverySurvivesActualProcessKillAfterMCPWithoutFrontend(t *testing.T) {
	dir := t.TempDir()
	original := editor.NewProject("磁盘中的原工程")
	path := filepath.Join(dir, "original.aividcut")
	if err := projectstore.Save(path, original); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecoveryProcessHelper$")
	cmd.Env = append(os.Environ(), "AIVIDCUT_RECOVERY_HELPER_DIR="+dir)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	ended := false
	defer func() {
		if !ended {
			_ = cmd.Process.Kill()
			<-exited
		}
	}()
	deadline := time.Now().Add(15 * time.Second)
	var ready recoveryProcessReady
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			ended = true
			t.Fatalf("恢复夹具进程过早退出：%v %s", err, output.String())
		default:
		}
		if data, err := os.ReadFile(filepath.Join(dir, "ready.json")); err == nil && json.Unmarshal(data, &ready) == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ready.ProjectID == "" {
		t.Fatal("辅助进程未就绪")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "recovery-process-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ready.Connection.URL, HTTPClient: &http.Client{Transport: mcpBearerTransport{headers: ready.Connection.HTTPHeaders}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	text := editor.DefaultClip("crash-title", "track-text-1", "text")
	text.Text = "异常退出恢复"
	callApplicationMCP(t, session, "aividcut_apply_edit", map[string]any{"request": editor.EditRequest{BaseRevision: 0, IdempotencyKey: "crash-mcp-edit", Commands: []editor.Command{{Type: "update_project", Name: "进程内未保存"}, {Type: "add_clip", Clip: &text}}}}, false)
	protected := false
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			ended = true
			t.Fatalf("辅助进程意外退出：%v", err)
		default:
		}
		files, _ := filepath.Glob(filepath.Join(dir, "recovery", "recovery-*.json"))
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				continue
			}
			var value struct {
				Metadata projectrecovery.Summary `json:"metadata"`
				Project  editor.Project          `json:"project"`
			}
			if json.Unmarshal(data, &value) == nil && value.Metadata.Revision == 1 && value.Project.Name == "进程内未保存" {
				protected = true
			}
		}
		if protected {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !protected {
		t.Fatal("默认5秒周期没有捕获真实MCP提交")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := <-exited; err == nil {
		t.Fatal("强杀不应是正常退出")
	}
	ended = true
	// 没有调用辅助进程Close；重新取得文件锁证明已是独立重启。
	s := recoveryService(t, filepath.Join(dir, "recovery"))
	page, err := s.ListProjectRecoveries(RecoveryListInput{Limit: 20})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	result, err := s.RestoreProjectRecovery(RestoreRecoveryInput{ProjectScope: versionScope(s.GetBootstrap()), ID: page.Records[0].ID, SHA256: page.Records[0].SHA256})
	if err != nil {
		t.Fatal(err)
	}
	if result.Project.Name != "进程内未保存" || len(result.Project.Clips) != 1 || result.Project.Clips[0].Text != "异常退出恢复" || !result.Dirty || result.ProjectPath != "" {
		t.Fatal("强杀后状态未恢复", result)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("恢复写回原文件")
	}
}
