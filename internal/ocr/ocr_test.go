package ocr

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWorkerBoundariesAndExactIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("本测试脚本仅用于POSIX进程边界，Windows当前无识别器")
	}
	path := filepath.Join(t.TempDir(), "ocr-fixture")
	good := Result{Version: 1, Engine: "apple-vision", Revision: 3, Languages: []string{"zh-Hans", "en-US"}, Lines: []Line{{Text: "本机识别", Confidence: new(0.9)}}}
	data, _ := json.Marshal(good)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s' '"+string(data)+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	worker := Worker{Path: path}
	result, err := worker.Recognize(context.Background(), []byte{0xff, 0xd8, 0xff, 0xd9})
	if err != nil || result.Lines[0].Text != "本机识别" {
		t.Fatal(result, err)
	}
	for _, input := range [][]byte{nil, []byte("not-jpeg"), make([]byte, 4<<20+1)} {
		if _, err = worker.Recognize(context.Background(), input); err == nil {
			t.Fatal("无效输入未拒绝")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = worker.Recognize(ctx, []byte{0xff, 0xd8}); err == nil {
		t.Fatal("取消仍执行")
	}
	for _, change := range []func(*Result){func(r *Result) { r.Engine = "unknown" }, func(r *Result) { r.Revision = 2 }, func(r *Result) { r.Lines[0].Confidence = new(1.1) }, func(r *Result) { r.Lines[0].Text = strings.Repeat("字", 1001) }, func(r *Result) { r.Languages = []string{"invalid"} }} {
		r := good
		r.Lines = append([]Line{}, good.Lines...)
		change(&r)
		if Validate(r) == nil {
			t.Fatal("无效识别器回复被接纳")
		}
	}
	if err = os.WriteFile(path, []byte("#!/bin/sh\nhead -c 300000 /dev/zero\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.Recognize(context.Background(), []byte{0xff, 0xd8}); err == nil {
		t.Fatal("越界输出未拒绝")
	}
	if err = os.WriteFile(path, []byte("#!/bin/sh\ncat >/dev/null\nsleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	short, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	if _, err = worker.Recognize(short, []byte{0xff, 0xd8}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("运行中的进程未被取消", err)
	}
}
