package processstats

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func ptr(v uint64) *uint64 { return &v }

func TestBundledWorkerRolesUseExplicitBinaryNames(t *testing.T) {
	for name, expected := range map[string]string{
		"AIVidCutMedia": "media", "aividcut-mlt-worker.exe": "media",
		"AIVidCut.exe": "helper", "SomeAIVidCutMediaHelper": "helper",
		"msedgewebview2.exe": "webview", "ffmpeg.exe": "encoding",
	} {
		if role(name) != expected {
			t.Fatalf("%s的角色没有对应随包程序：%s", name, role(name))
		}
	}
}

type fakeReader struct {
	nodes   []Identity
	values  map[int]Counters
	failure bool
}

func (f *fakeReader) Enumerate(context.Context) ([]Identity, error) {
	if f.failure {
		return nil, errors.New("权限模拟")
	}
	return f.nodes, nil
}
func (f *fakeReader) Read(_ context.Context, n Identity) (Counters, error) {
	value, ok := f.values[n.PID]
	if !ok {
		return Counters{}, errors.New("已退出")
	}
	return value, nil
}
func fakeFixture() (*Collector, *fakeReader, *time.Time) {
	at := time.Now()
	root := Identity{PID: 10, PPID: 1, Name: "app", StartedNS: 1}
	child := Identity{PID: 11, PPID: 10, Name: "ffmpeg", StartedNS: 2}
	grand := Identity{PID: 12, PPID: 11, Name: "helper", StartedNS: 3}
	other := Identity{PID: 20, PPID: 1, Name: "codex", StartedNS: 4}
	f := &fakeReader{nodes: []Identity{root, child, grand, other}, values: map[int]Counters{}}
	for _, n := range f.nodes {
		f.values[n.PID] = Counters{Identity: n, RSSBytes: ptr(uint64(n.PID)), CPUTimeNS: ptr(1000000000)}
	}
	c := newCollector(10, f)
	c.now = func() time.Time { return at }
	return c, f, &at
}
func TestOnlyTreeCountersAndCPUWindow(t *testing.T) {
	c, f, at := fakeFixture()
	first, err := c.Collect(context.Background())
	if err != nil || len(first.Processes) != 3 || first.CPUPercent != nil || first.RSSBytes == nil || *first.RSSBytes != 33 {
		t.Fatal(first, err)
	}
	if first.Processes[2].Role != "encoding" {
		t.Fatal("后代没有继承已确认类别")
	}
	*at = at.Add(time.Second)
	for _, pid := range []int{10, 11, 12} {
		v := f.values[pid]
		v.CPUTimeNS = ptr(1500000000)
		f.values[pid] = v
	}
	second, err := c.Collect(context.Background())
	if err != nil || second.CPUPercent == nil || *second.CPUPercent != 150 || second.WindowMs == nil || *second.WindowMs != 1000 {
		t.Fatal(second, err)
	}
}
func TestObservedOrphanRetainedButPIDReuseRejected(t *testing.T) {
	c, f, at := fakeFixture()
	c.Collect(context.Background())
	*at = at.Add(time.Second)
	for i, n := range f.nodes {
		if n.PID == 11 {
			n.PPID = 1
			f.nodes[i] = n
			v := f.values[11]
			v.PPID = 1
			f.values[11] = v
		}
	}
	value, err := c.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range value.Processes {
		if p.PID == 11 {
			found = p.Association == "observed_child"
		}
	}
	if !found {
		t.Fatal("曾确认的孤儿进程丢失")
	}
	*at = at.Add(time.Second)
	for i, n := range f.nodes {
		if n.PID == 11 {
			n.StartedNS = 999
			f.nodes[i] = n
			v := f.values[11]
			v.StartedNS = 999
			f.values[11] = v
		}
	}
	value, err = c.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range value.Processes {
		if p.PID == 11 {
			t.Fatal("PID复用被归入旧应用")
		}
	}
}
func TestMissingMetricsAndCounterResetAreNotZero(t *testing.T) {
	c, f, at := fakeFixture()
	v := f.values[11]
	v.RSSBytes = nil
	v.Issue = "不可读取"
	f.values[11] = v
	first, err := c.Collect(context.Background())
	if err != nil || first.RSSBytes != nil || first.MissingRSS != 1 || !first.Partial {
		t.Fatal(first, err)
	}
	*at = at.Add(time.Second)
	v.CPUTimeNS = ptr(1)
	f.values[11] = v
	second, err := c.Collect(context.Background())
	if err != nil || second.CPUPercent != nil {
		t.Fatal(second, err)
	}
	for _, p := range second.Processes {
		if p.PID == 11 && p.CPUPercent != nil {
			t.Fatal("倒退CPU计数变为有效占用")
		}
	}
	f.failure = true
	if _, err = c.Collect(context.Background()); err == nil {
		t.Fatal("枚举失败仍冒充完整样本")
	}
}
func TestRootReuseDuplicateAndBounds(t *testing.T) {
	c, f, _ := fakeFixture()
	c.Collect(context.Background())
	v := f.values[10]
	v.StartedNS = 99
	f.values[10] = v
	if _, err := c.Collect(context.Background()); err == nil {
		t.Fatal("根身份改变仍采样")
	}
	c, f, _ = fakeFixture()
	f.nodes = append(f.nodes, f.nodes[0])
	if _, err := c.Collect(context.Background()); err == nil {
		t.Fatal("重复PID被接纳")
	}
	c, f, _ = fakeFixture()
	for i := 100; i < 240; i++ {
		n := Identity{PID: i, PPID: 10, Name: strings.Repeat("x", 8), StartedNS: int64(i)}
		f.nodes = append(f.nodes, n)
		f.values[i] = Counters{Identity: n, RSSBytes: ptr(1), CPUTimeNS: ptr(1)}
	}
	value, err := c.Collect(context.Background())
	if err != nil || len(value.Processes) != MaxProcesses || !value.Partial || value.RSSBytes != nil {
		t.Fatal(value, err)
	}
}
