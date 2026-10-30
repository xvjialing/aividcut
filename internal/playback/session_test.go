package playback

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// 夹具执行真实子进程及管道；不替代MLT真实帧验收。
func TestPlaybackProtocolFixture(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "playback-helper" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request map[string]any
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		id := request["requestId"]
		op := request["op"]
		var body []byte
		switch op {
		case "inspect":
			fmt.Fprint(os.Stderr, strings.Repeat("diagnostic", 20000))
			body, _ = json.Marshal(map[string]any{"requestId": id, "type": "capabilities", "capabilities": Capabilities{Available: true, Protocol: Protocol, MLTVersion: "fixture", Producers: []string{"fixture"}}})
		case "next":
			if len(os.Args) < 3 || os.WriteFile(os.Args[len(os.Args)-2], []byte("next received\n"), 0600) != nil {
				os.Exit(2)
			}
			for {
				time.Sleep(time.Second)
			}
		case "load":
			body, _ = json.Marshal(map[string]any{"requestId": id, "type": "loaded", "info": Info{DurationFrames: int64(request["durationFrames"].(float64)), Width: int(request["width"].(float64)), Height: int(request["height"].(float64)), FPSNum: int(request["fpsNum"].(float64)), FPSDen: int(request["fpsDen"].(float64)), SampleRate: 48000, Channels: 2}})
		case "close":
			body, _ = json.Marshal(map[string]any{"requestId": id, "type": "closed"})
		default:
			body, _ = json.Marshal(map[string]any{"requestId": id, "type": "error", "error": "fixture unsupported"})
		}
		header := make([]byte, 16)
		copy(header, Protocol)
		binary.BigEndian.PutUint32(header[4:], uint32(len(body)))
		_, _ = os.Stdout.Write(header)
		_, _ = os.Stdout.Write(body)
		if op == "close" {
			os.Exit(0)
		}
	}
	os.Exit(0)
}

func fixtureExecutable(t *testing.T) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("此shell夹具只在Unix运行，Windows原生/进程树仍需实机")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	directory := t.TempDir()
	path, pullStarted := filepath.Join(directory, "worker.sh"), filepath.Join(directory, "pull-started")
	script := "#!/bin/sh\nexec " + quote(binary) + " -test.run=^TestPlaybackProtocolFixture$ -- " + quote(pullStarted) + " playback-helper\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path, pullStarted
}

func startFixtureSession(t *testing.T) (*Session, string) {
	t.Helper()
	worker, pullStarted := fixtureExecutable(t)
	// 全量-race并发会延迟测试子进程加载及inspect；启动使用独立的充足预算。
	// 真正取消/关闭响应仍由下方原有的2秒断言约束，不靠这个预算判定通过。
	session, err := Start(context.Background(), Config{WorkerPath: worker, Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal("协议夹具启动失败", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, pullStarted
}

func awaitFixtureBlockedPull(t *testing.T, session *Session, pullStarted string, returned <-chan error) {
	t.Helper()
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	for {
		if _, err := os.Stat(pullStarted); err == nil {
			return
		}
		select {
		case err := <-returned:
			t.Fatalf("夹具未进入阻塞pull就返回：%v", err)
		case <-session.Done():
			t.Fatal("夹具未进入阻塞pull就退出")
		case <-timeout.C:
			t.Fatal("夹具未确认收到next，不能把未调度的调用当取消回归")
		case <-poll.C:
		}
	}
}

func TestCancelledPullKillsPersistentWorkerAndCannotReusePipe(t *testing.T) {
	session, pullStarted := startFixtureSession(t)
	stderr := session.command.Stderr.(*boundedStderr)
	stderr.mu.Lock()
	size := len(stderr.data)
	stderr.mu.Unlock()
	if size > 32<<10 {
		t.Fatal("stderr无界增长")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan error, 1)
	go func() { _, err := session.Next(ctx); returned <- err }()
	awaitFixtureBlockedPull(t, session, pullStarted, returned)
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("阻塞pull未保留取消语义", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("取消未中止阻塞子进程")
	}
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("进程未退出")
	}
	if _, err := session.Seek(context.Background(), 0); err == nil {
		t.Fatal("取消后重复使用已乱序管道")
	}
}

func TestCloseInterruptsPullWithoutWaitingForItsLongTimeout(t *testing.T) {
	session, pullStarted := startFixtureSession(t)
	// 启动完成后才给被测pull设置长预算；Close必须独立于该预算立即中止进程。
	session.mu.Lock()
	session.config.Timeout = time.Minute
	session.mu.Unlock()
	returned := make(chan error, 1)
	go func() { _, err := session.Next(context.Background()); returned <- err }()
	awaitFixtureBlockedPull(t, session, pullStarted, returned)
	started := time.Now()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("Close等待了阻塞pull的长超时")
	}
	select {
	case err := <-returned:
		if err == nil {
			t.Fatal("关闭后pull假成功")
		}
	case <-time.After(time.Second):
		t.Fatal("关闭后pull未结束")
	}
}
