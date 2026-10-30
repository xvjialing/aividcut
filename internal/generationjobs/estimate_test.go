package generationjobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"aividcut/internal/editor"
	"aividcut/internal/generation"
)

func TestEstimateSharedDecimalCases(t *testing.T) {
	data, err := os.ReadFile("testdata/estimate-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string
		Request  generation.Request
		Input    *EstimateInput
		Expected *CostEstimate
		Error    bool
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got, err := calculateEstimate(c.Request, c.Input)
			if (err != nil) != c.Error || !c.Error && !reflect.DeepEqual(got, c.Expected) {
				t.Fatalf("得到 %+v / %v，预期 %+v / error=%v", got, err, c.Expected, c.Error)
			}
		})
	}
}

func TestEstimateKeepsLegacySignature(t *testing.T) {
	r := inputFor("legacy").Request
	data, _ := json.Marshal(struct {
		ConnectionID string
		ProjectID    string
		Request      generation.Request
	}{"connection", "project", r})
	hash := sha256.Sum256(data)
	if got := signature("connection", "project", r, nil); got != hex.EncodeToString(hash[:]) {
		t.Fatal("无估价改变了旧请求签名")
	}
}

func TestEstimatePersistsAndDuplicateNeverPostsAgain(t *testing.T) {
	p := &fixtureProvider{submit: func(_ context.Context, r generation.Request) (generation.Task, error) {
		task := taskFor(r, generation.Succeeded, "")
		task.Outputs = []generation.Output{imageOutput()}
		return task, nil
	}}
	m, options := newFixture(t, t.TempDir(), p)
	project := editor.NewProject("费用参考")
	in := inputFor("estimate-success")
	in.Request.Count = 3
	in.Estimate = &EstimateInput{Currency: "CNY", Unit: "image", UnitPrice: "0.1"}
	if _, err := m.Submit(context.Background(), project, in); err != nil {
		t.Fatal(err)
	}
	job := awaitIdle(t, m, in.Request.ID, Succeeded)
	if job.Estimate == nil || job.Estimate.Amount != "0.30" || job.Estimate.Quantity != 3 {
		t.Fatalf("估价丢失：%+v", job)
	}
	job.Estimate.Amount = "999"
	read, _ := m.Get(in.Request.ID)
	if read.Estimate.Amount != "0.30" {
		t.Fatal("读出的估价共享可变状态")
	}
	in.Estimate.UnitPrice = "0.100000"
	in.Consent = false
	if _, err := m.Submit(context.Background(), project, in); err != nil {
		t.Fatal("相同规范价应返回原任务：", err)
	}
	in.Estimate.UnitPrice = "0.2"
	if _, err := m.Submit(context.Background(), project, in); err == nil {
		t.Fatal("同一ID改价仍被接受")
	}
	m.Close()
	reopened, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	read, _ = reopened.Get(in.Request.ID)
	if read.Estimate == nil || read.Estimate.Amount != "0.30" || read.Status != Succeeded {
		t.Fatalf("重开未保留估价：%+v", read)
	}
	in.Estimate.UnitPrice = "0.10"
	if _, err = reopened.Submit(context.Background(), project, in); err != nil {
		t.Fatal(err)
	}
	if p.posts.Load() != 1 {
		t.Fatalf("重复收费调用：%d", p.posts.Load())
	}
}

func TestEstimateCorruptionOccupiesID(t *testing.T) {
	for _, mutation := range []string{"amount", "quantity", "basis", "version"} {
		t.Run(mutation, func(t *testing.T) {
			p := &fixtureProvider{submit: func(_ context.Context, r generation.Request) (generation.Task, error) {
				task := taskFor(r, generation.Succeeded, "")
				task.Outputs = []generation.Output{imageOutput()}
				return task, nil
			}}
			m, options := newFixture(t, t.TempDir(), p)
			project := editor.NewProject("损坏估价")
			in := inputFor("estimate-corrupt")
			in.Estimate = &EstimateInput{Currency: "USD", Unit: "request", UnitPrice: "0.02"}
			if _, err := m.Submit(context.Background(), project, in); err != nil {
				t.Fatal(err)
			}
			awaitIdle(t, m, in.Request.ID, Succeeded)
			m.Close()
			path := filepath.Join(options.Directory, "jobs", in.Request.ID+".json")
			data, _ := os.ReadFile(path)
			var r record
			if err := json.Unmarshal(data, &r); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "amount":
				r.Job.Estimate.Amount = "0.01"
			case "quantity":
				r.Job.Estimate.Quantity = 2
			case "basis":
				r.Job.Estimate.Basis = "provider-invoice"
			case "version":
				r.Version = 1
			}
			// 即使重算签名，金额及版本仍须由单价重新验证。
			r.Signature = signature(r.Job.ConnectionID, r.Job.ProjectID, r.Job.Request, r.Job.Estimate)
			data, _ = json.Marshal(r)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			reopened, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			job, _ := reopened.Get(in.Request.ID)
			if job.Status != Unknown || job.CanResume {
				t.Fatalf("损坏估价变为有效任务：%+v", job)
			}
			if _, err = reopened.Submit(context.Background(), project, in); err == nil {
				t.Fatal("损坏原ID允许重新提交")
			}
			if p.posts.Load() != 1 {
				t.Fatal("损坏记录导致重交")
			}
		})
	}
}
