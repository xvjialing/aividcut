package ocr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsCapabilityRequiresIdentityAndExactAdjacentWorker(t *testing.T) {
	root := t.TempDir()
	helper := filepath.Join(root, "AIVidCutOCR.exe")
	if err := os.WriteFile(helper, []byte("仅发现测试，不执行"), 0600); err != nil {
		t.Fatal(err)
	}
	capability, _ := discoverFor("windows", filepath.Join(root, "AIVidCut.exe"), "", false)
	if capability.Available || !strings.Contains(capability.Reason, "MSIX") {
		t.Fatal("无身份误报可用", capability)
	}
	capability, worker := discoverFor("windows", filepath.Join(root, "AIVidCut.exe"), "", true)
	if !capability.Available || worker.Path != helper || worker.Engine != "windows-media-ocr" {
		t.Fatal("相邻发现错误", capability, worker)
	}
	capability, _ = discoverFor("windows", filepath.Join(t.TempDir(), "AIVidCut.exe"), "", true)
	if capability.Available {
		t.Fatal("缺少进程仍可用")
	}
	capability, _ = discoverFor("windows", filepath.Join(root, "AIVidCut.exe"), "relative.exe", true)
	if capability.Available {
		t.Fatal("相对覆盖路径被允许")
	}
}
func TestWindowsResultUnknownConfidenceAndSourceCannotMasquerade(t *testing.T) {
	result := Result{Version: 1, Engine: "windows-media-ocr", Revision: 0, Languages: []string{"zh-Hans-CN"}, Lines: []Line{{Text: "系统识别结果"}}}
	if err := Validate(result); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	if !strings.Contains(string(data), `"confidence":null`) {
		t.Fatal("未知置信度被当作零")
	}
	producer, method, err := SourceIdentity(result.Engine)
	if err != nil || producer != "windows-media-ocr" || method != "windows-media-ocr-v1" {
		t.Fatal("Windows来源被伪装")
	}
	result.Lines[0].Confidence = new(0.0)
	if Validate(result) == nil {
		t.Fatal("Windows凭空给出置信度")
	}
	result.Lines[0].Confidence = nil
	result.Revision = 1
	if Validate(result) == nil {
		t.Fatal("虚构系统模型修订")
	}
	if workerError([]byte(`{"errorCode":"no_language"}`)) == "" || workerError([]byte(`{"errorCode":"unknown","detail":"不能展示未验证消息"}`)) != "" {
		t.Fatal("错误映射不符")
	}
}
func TestWorkerChecksEngineAndReportsNoLanguage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("测试脚本用于POSIX宿主，Windows实际验收交接")
	}
	helper := filepath.Join(t.TempDir(), "worker")
	script := "#!/bin/sh\nprintf '%s' '{\"version\":1,\"engine\":\"windows-media-ocr\",\"revision\":0,\"languages\":[\"en-US\"],\"lines\":[{\"text\":\"text\",\"confidence\":null}],\"truncated\":false}'\n"
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	jpeg := []byte{0xff, 0xd8}
	if _, err := (Worker{Path: helper}).Recognize(context.Background(), jpeg); err == nil {
		t.Fatal("Apple进程接受了Windows身份")
	}
	if _, err := (Worker{Path: helper, Engine: "windows-media-ocr"}).Recognize(context.Background(), jpeg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf '%s' '{\"errorCode\":\"no_language\"}'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := (Worker{Path: helper, Engine: "windows-media-ocr"}).Recognize(context.Background(), jpeg); err == nil || !strings.Contains(err.Error(), "语言") {
		t.Fatal("缺语言未提示实际原因", err)
	}
}
