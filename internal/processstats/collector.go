// Package processstats 只观测本应用及已确认的后代，不接受前端指定PID。
package processstats

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const MaxProcesses = 128
const MaxEnumerated = 65536

type Identity struct {
	PID       int
	PPID      int
	Name      string
	StartedNS int64
}
type Counters struct {
	Identity
	RSSBytes     *uint64
	PrivateBytes *uint64
	PrivateKind  string
	CPUTimeNS    *uint64
	Issue        string
}
type Process struct {
	PID          int      `json:"pid"`
	PPID         int      `json:"ppid"`
	Name         string   `json:"name"`
	Identity     string   `json:"identity"`
	Role         string   `json:"role"`
	Association  string   `json:"association"`
	RSSBytes     *uint64  `json:"rssBytes"`
	PrivateBytes *uint64  `json:"privateBytes"`
	PrivateKind  string   `json:"privateKind,omitempty"`
	CPUTimeNS    *uint64  `json:"cpuTimeNs"`
	CPUPercent   *float64 `json:"cpuPercent"`
	Issue        string   `json:"issue,omitempty"`
}
type Snapshot struct {
	At            time.Time `json:"at"`
	QueryMs       float64   `json:"queryMs"`
	WindowMs      *float64  `json:"windowMs"`
	RootPID       int       `json:"rootPid"`
	Processes     []Process `json:"processes"`
	KnownRSSBytes uint64    `json:"knownRssBytes"`
	RSSBytes      *uint64   `json:"rssBytes"`
	CPUPercent    *float64  `json:"cpuPercent"`
	MissingRSS    int       `json:"missingRss"`
	MissingCPU    int       `json:"missingCpu"`
	Partial       bool      `json:"partial"`
	Issues        []string  `json:"issues"`
	Uncovered     []string  `json:"uncovered"`
	OS            string    `json:"os"`
	Architecture  string    `json:"architecture"`
	LogicalCPUs   int       `json:"logicalCpus"`
}
type Reader interface {
	Enumerate(context.Context) ([]Identity, error)
	Read(context.Context, Identity) (Counters, error)
}
type previous struct {
	started int64
	cpu     *uint64
	role    string
}
type Collector struct {
	mu           sync.Mutex
	root         int
	reader       Reader
	previous     map[int]previous
	at           time.Time
	rootIdentity int64
	now          func() time.Time
}

func New() *Collector { return newCollector(os.Getpid(), platformReader{}) }
func newCollector(root int, reader Reader) *Collector {
	return &Collector{root: root, reader: reader, previous: map[int]previous{}, now: time.Now}
}
func role(name string) string {
	name = strings.ToLower(filepath.Base(name))
	name = strings.TrimSuffix(name, ".exe")
	switch {
	case strings.Contains(name, "aividcut-mlt") || name == "aividcutmedia":
		return "media"
	case name == "ffmpeg" || name == "ffprobe":
		return "encoding"
	case name == "codex" || name == "claude":
		return "agent"
	case strings.Contains(name, "webview") || strings.Contains(name, "webkit"):
		return "webview"
	default:
		return "helper"
	}
}
func (c *Collector) Collect(ctx context.Context) (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	queryStarted := time.Now()
	started := c.now()
	out := Snapshot{At: started.UTC(), RootPID: c.root, Processes: []Process{}, Issues: []string{}, Uncovered: []string{"GPU与显存未提供", "独立启动的CLI/模型不属于本应用进程树"}, OS: runtime.GOOS, Architecture: runtime.GOARCH, LogicalCPUs: runtime.NumCPU()}
	if runtime.GOOS == "darwin" {
		out.Uncovered = append(out.Uncovered, "独立WebKit XPC不以名称猜测归属，当前未计入")
	}
	nodes, err := c.reader.Enumerate(ctx)
	if err != nil {
		return out, err
	}
	if len(nodes) > MaxEnumerated {
		return out, errors.New("进程枚举超过65536条，未扩大观测范围")
	}
	byPID := map[int]Identity{}
	children := map[int][]int{}
	for _, n := range nodes {
		if n.PID <= 0 || n.PPID < 0 {
			continue
		}
		if _, exists := byPID[n.PID]; exists {
			return out, errors.New("进程枚举身份重复，未使用不一致快照")
		}
		byPID[n.PID] = n
		children[n.PPID] = append(children[n.PPID], n.PID)
	}
	root, ok := byPID[c.root]
	if !ok {
		return out, errors.New("当前应用进程已不在枚举中")
	}
	rootCounters, err := c.reader.Read(ctx, root)
	if err != nil || rootCounters.StartedNS <= 0 {
		return out, errors.New("无法确认当前应用进程身份")
	}
	if c.rootIdentity != 0 && c.rootIdentity != rootCounters.StartedNS {
		return out, errors.New("根进程身份已经变化，已停止采集")
	}
	c.rootIdentity = rootCounters.StartedNS
	candidates := []int{c.root}
	seen := map[int]bool{c.root: true}
	for i := 0; i < len(candidates); i++ {
		for _, pid := range children[candidates[i]] {
			if !seen[pid] {
				seen[pid] = true
				candidates = append(candidates, pid)
			}
		}
	}
	for pid := range c.previous {
		if _, ok := byPID[pid]; ok && !seen[pid] {
			seen[pid] = true
			candidates = append(candidates, pid)
		}
	}
	if len(candidates) > MaxProcesses {
		out.Partial = true
		out.Issues = append(out.Issues, "已确认进程超过128份，明细受限")
		candidates = candidates[:MaxProcesses]
	}
	next := map[int]previous{}
	var cpuTotal float64
	rssComplete, cpuComplete := true, true
	elapsed := started.Sub(c.at)
	if !c.at.IsZero() && elapsed > 0 {
		ms := float64(elapsed) / float64(time.Millisecond)
		out.WindowMs = &ms
	}
	for _, pid := range candidates {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		node := byPID[pid]
		values := rootCounters
		if pid != c.root {
			values, err = c.reader.Read(ctx, node)
		}
		if err != nil || values.StartedNS <= 0 || values.PID != pid {
			if old, ok := c.previous[pid]; ok {
				next[pid] = previous{started: old.started, role: old.role}
			}
			out.Partial = true
			out.Issues = append(out.Issues, fmt.Sprintf("PID %d在读取时退出或无法确认身份", pid))
			continue
		}
		old, wasKnown := c.previous[pid]
		if node.StartedNS > 0 && values.StartedNS != node.StartedNS || values.StartedNS > started.UnixNano() {
			out.Partial = true
			out.Issues = append(out.Issues, fmt.Sprintf("PID %d在观测期间改变身份，下次再确认", pid))
			continue
		}
		association := "descendant"
		if pid == c.root {
			association = "root"
		} else if !isDescendant(pid, c.root, byPID) {
			if !wasKnown || old.started != values.StartedNS {
				continue
			}
			association = "observed_child"
		}
		label := role(values.Name)
		if pid == c.root {
			label = "application"
		} else if label == "helper" {
			if parent, ok := next[values.PPID]; ok && parent.role != "application" && parent.role != "helper" {
				label = parent.role
			}
		}
		row := Process{PID: pid, PPID: values.PPID, Name: values.Name, Identity: fmt.Sprintf("%d:%d", pid, values.StartedNS), Role: label, Association: association, RSSBytes: values.RSSBytes, PrivateBytes: values.PrivateBytes, PrivateKind: values.PrivateKind, CPUTimeNS: values.CPUTimeNS, Issue: values.Issue}
		if wasKnown && old.started == values.StartedNS && old.cpu != nil && values.CPUTimeNS != nil && *values.CPUTimeNS >= *old.cpu && out.WindowMs != nil && elapsed >= time.Millisecond {
			percent := float64(*values.CPUTimeNS-*old.cpu) / float64(elapsed) * 100
			if !math.IsInf(percent, 0) && !math.IsNaN(percent) {
				row.CPUPercent = &percent
			}
		}
		if row.RSSBytes != nil {
			if *row.RSSBytes > 1<<60 || out.KnownRSSBytes > math.MaxUint64-*row.RSSBytes {
				return out, errors.New("进程内存计数超出可验证范围")
			}
			out.KnownRSSBytes += *row.RSSBytes
		} else {
			rssComplete = false
			out.MissingRSS++
		}
		if row.CPUPercent != nil {
			cpuTotal += *row.CPUPercent
		} else {
			cpuComplete = false
			out.MissingCPU++
		}
		if row.Issue != "" {
			out.Partial = true
		}
		out.Processes = append(out.Processes, row)
		next[pid] = previous{started: values.StartedNS, cpu: copyUint(values.CPUTimeNS), role: label}
	}
	if len(out.Processes) == 0 {
		return out, errors.New("没有读取到可确认身份的应用进程")
	}
	if rssComplete && !out.Partial {
		total := out.KnownRSSBytes
		out.RSSBytes = &total
	}
	if cpuComplete && !out.Partial {
		out.CPUPercent = &cpuTotal
	}
	sort.SliceStable(out.Processes, func(i, j int) bool {
		if out.Processes[i].PID == c.root {
			return true
		}
		if out.Processes[j].PID == c.root {
			return false
		}
		return out.Processes[i].PID < out.Processes[j].PID
	})
	c.previous = next
	c.at = started
	out.QueryMs = float64(time.Since(queryStarted)) / float64(time.Millisecond)
	return out, nil
}
func copyUint(v *uint64) *uint64 {
	if v == nil {
		return nil
	}
	copy := *v
	return &copy
}
func isDescendant(pid, root int, nodes map[int]Identity) bool {
	seen := map[int]bool{}
	for pid > 0 && !seen[pid] {
		if pid == root {
			return true
		}
		seen[pid] = true
		n, ok := nodes[pid]
		if !ok {
			return false
		}
		pid = n.PPID
	}
	return false
}
