package releaseaudit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Probe struct {
	ArtifactSHA256 string `json:"artifactSHA256"`
	Version        string `json:"version"`
	Configuration  string `json:"configuration"`
	GPL            bool   `json:"gpl"`
	Nonfree        bool   `json:"nonfree"`
}
type probeOutput struct {
	buffer   bytes.Buffer
	cancel   context.CancelFunc
	exceeded bool
}

func (w *probeOutput) Write(p []byte) (int, error) {
	if w.buffer.Len()+len(p) > 65536 {
		w.exceeded = true
		w.cancel()
		return 0, errors.New("版本输出超过64KiB")
	}
	return w.buffer.Write(p)
}
func probeVersion(ctx context.Context, path, role, expected string) (*Probe, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out := &probeOutput{cancel: cancel}
	cmd := exec.CommandContext(probeCtx, path, "-version")
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = 200 * time.Millisecond
	if e := cmd.Run(); e != nil {
		if out.exceeded {
			return nil, errors.New("版本输出超过64KiB")
		}
		return nil, errors.New("版本进程失败或超时；未记录原始输出")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, maxFileBytes+1))
	if e != nil || n > maxFileBytes || hex.EncodeToString(h.Sum(nil)) != expected {
		return nil, errors.New("版本探测期间二进制变化")
	}
	p := &Probe{ArtifactSHA256: expected}
	for _, line := range strings.Split(out.buffer.String(), "\n") {
		if strings.HasPrefix(line, role+" version ") {
			p.Version = line
		}
		if strings.HasPrefix(line, "configuration: ") {
			p.Configuration = strings.TrimPrefix(line, "configuration: ")
		}
	}
	if p.Version == "" || p.Configuration == "" {
		return nil, errors.New("缺少可核对的FFmpeg版本或构建配置")
	}
	for _, flag := range strings.Fields(p.Configuration) {
		flag = strings.Trim(flag, "'\"")
		if flag == "--enable-gpl" {
			p.GPL = true
		}
		if flag == "--enable-nonfree" {
			p.Nonfree = true
		}
	}
	return p, nil
}
