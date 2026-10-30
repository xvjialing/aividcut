//go:build darwin && cgo

package processstats

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestActualOwnProcessAndChild(t *testing.T) {
	if os.Getenv("AIVIDCUT_RESOURCE_CHILD") == "1" {
		buffer := make([]byte, 24<<20)
		for i := 0; i < len(buffer); i += 4096 {
			buffer[i] = 1
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			for i := 0; i < len(buffer); i += 4096 {
				buffer[i]++
			}
		}
		runtime.KeepAlive(buffer)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestActualOwnProcessAndChild")
	cmd.Env = append(os.Environ(), "AIVIDCUT_RESOURCE_CHILD=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	collector := New()
	first, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Processes) < 2 {
		t.Fatal("真实子进程未采到")
	}
	time.Sleep(100 * time.Millisecond)
	second, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range second.Processes {
		if p.PID == cmd.Process.Pid {
			found = p.RSSBytes != nil && *p.RSSBytes > 16<<20 && p.CPUPercent != nil && *p.CPUPercent > 0
		}
	}
	if !found {
		t.Fatal("真实子进程内存/CPU未采到", second)
	}
}

func TestActualCPUCounterMatchesPOSIXTime(t *testing.T) {
	c := New()
	before, err := c.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var first, last unix.Rusage
	unix.Getrusage(unix.RUSAGE_SELF, &first)
	deadline := time.Now().Add(150 * time.Millisecond)
	var number uint64
	for time.Now().Before(deadline) {
		for i := 0; i < 100000; i++ {
			number = number*3 + uint64(i)
		}
	}
	runtime.KeepAlive(number)
	unix.Getrusage(unix.RUSAGE_SELF, &last)
	after, err := c.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	delta := *after.Processes[0].CPUTimeNS - *before.Processes[0].CPUTimeNS
	reference := (last.Utime.Nano() + last.Stime.Nano()) - (first.Utime.Nano() + first.Stime.Nano())
	ratio := float64(delta) / float64(reference)
	if reference < 50000000 || ratio < 0.85 || ratio > 1.15 {
		t.Fatal("原生CPU计数单位与POSIX时间不一致", delta, reference, ratio)
	}
}
