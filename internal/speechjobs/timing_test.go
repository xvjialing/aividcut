package speechjobs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/generation"
	"aividcut/internal/speech"
)

const completeOriginal = `{"characters":["1","2"],"character_start_times_seconds":[0,0.05],"character_end_times_seconds":[0.05,0.1]}`
const completeNormalized = `{"characters":["十","二"],"character_start_times_seconds":[0,0.05],"character_end_times_seconds":[0.05,0.1]}`

func timedInput(id string) Input {
	input := synthesizeInput(id)
	input.Synthesize.Text, input.Synthesize.WithTiming = "12", true
	return input
}

func timedHTTPFixture(t *testing.T, original, normalized string, handler func(http.ResponseWriter, *http.Request)) (*Manager, Options, *fixtureControl, editor.Project, *atomic.Int32) {
	t.Helper()
	initial, options, control, project := fixture(t, 1_000_000)
	initial.Close()
	<-initial.Done()
	posts := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/text-to-speech/fixture-voice/with-timestamps" || r.URL.Query().Get("output_format") != "mp3_44100_128" || r.Header.Get("xi-api-key") != "fixture-only-private-key" {
			t.Error("没有使用唯一显式带时间端点或独立凭据源")
		}
		if handler != nil {
			handler(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"audio_base64":%q,"alignment":%s,"normalized_alignment":%s}`, base64.StdEncoding.EncodeToString(control.mp3), original, normalized)
	}))
	t.Cleanup(server.Close)
	options.Factory = func(secrets generation.SecretSource, files speech.MediaSource) (speech.Provider, error) {
		control.factories.Add(1)
		return speech.NewElevenLabs(speech.Config{BaseURL: server.URL, HTTPClient: server.Client(), Secrets: secrets, Files: files, AllowLocalTestHTTP: true})
	}
	manager, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	return manager, options, control, project, posts
}

func noRemoteRecovery(t *testing.T, options Options) Options {
	t.Helper()
	options.Resolve = func(context.Context, string) (Binding, error) {
		t.Error("本地恢复读取了连接或凭据")
		return Binding{}, errors.New("禁止远端恢复")
	}
	options.Factory = func(generation.SecretSource, speech.MediaSource) (speech.Provider, error) {
		t.Error("本地恢复创建了供应商适配器")
		return nil, errors.New("禁止远端恢复")
	}
	return options
}

func TestTimedTTSDurableReceiptKeepsOriginalNormalizedAndSinglePost(t *testing.T) {
	m, options, _, project, posts := timedHTTPFixture(t, completeOriginal, completeNormalized, nil)
	input := timedInput("timed-durable")
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	job := await(t, m, input.ID, Succeeded)
	if !job.TimingRequested || !job.TimingAvailable || !job.TimingComplete || !job.NormalizedTimingAvailable || !job.NormalizedTimingComplete || job.Timing == nil || job.Audio == nil || posts.Load() != 1 {
		t.Fatal("带时间任务未保留完整独立证据", job.Summary)
	}
	if strings.Join(job.Timing.Alignment.Characters, "") != "12" || strings.Join(job.Timing.NormalizedAlignment.Characters, "") != "十二" {
		t.Fatal("规范化文本被当成原文或原文被改写")
	}
	if job.AudioProbe == nil || job.AudioProbe.SampleCount != nil || len(job.Timing.Data) != 0 {
		t.Fatal("旧Probe不能伪造样本数，detail不应复制音频字节")
	}
	// 数组完整不表示文字匹配；应用仍须独立验证，此层不擅自按请求补词。
	job.Timing.Alignment.Characters[0] = "调用者改写"
	second, err := m.Get(input.ID)
	if err != nil || second.Timing.Alignment.Characters[0] != "1" {
		t.Fatal("detail别名修改了原始回执", err)
	}
	data, err := os.ReadFile(m.output(input.ID, filepath.Join("audio", "receipt.json")))
	if err != nil || bytes.Contains(data, []byte("audio_base64")) || bytes.Contains(data, []byte("fixture-only-private-key")) {
		t.Fatal("回执含音频JSON或密钥", err)
	}
	m.Close()
	<-m.Done()
	restored, err := New(noRemoteRecovery(t, options))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	job, err = restored.Get(input.ID)
	if err != nil || job.Status != Succeeded || job.Timing == nil || !job.NormalizedTimingComplete || posts.Load() != 1 {
		t.Fatal("重开没有只读恢复时间回执", err)
	}
	if _, err := restored.Submit(context.Background(), project, input); err != nil || posts.Load() != 1 {
		t.Fatal("重复ID重新提交了配音", err)
	}
}

func TestTimedTTSMissingNullMalformedAndDifferentTextRemainEvidence(t *testing.T) {
	for _, test := range []struct {
		name, original           string
		available, complete, raw bool
	}{
		{"missing", "null", false, false, false},
		{"null-time", `{"characters":["1"],"character_start_times_seconds":[null],"character_end_times_seconds":[0.1]}`, true, false, false},
		{"bad-length", `{"characters":["1"],"character_start_times_seconds":[],"character_end_times_seconds":[0.1]}`, true, false, false},
		{"bad-type", `{"characters":"1","character_start_times_seconds":[0],"character_end_times_seconds":[0.1]}`, true, false, true},
		{"different-original", `{"characters":["另","文"],"character_start_times_seconds":[0,0.05],"character_end_times_seconds":[0.05,0.1]}`, true, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, options, _, project, posts := timedHTTPFixture(t, test.original, completeNormalized, nil)
			input := timedInput("timed-" + test.name)
			if _, err := m.Submit(context.Background(), project, input); err != nil {
				t.Fatal(err)
			}
			job := await(t, m, input.ID, Succeeded)
			if job.Audio == nil || job.Timing == nil || job.TimingAvailable != test.available || job.TimingComplete != test.complete || !job.NormalizedTimingComplete || (len(job.Timing.RawAlignment) > 0) != test.raw || posts.Load() != 1 {
				t.Fatal("时间质量不能丢弃合法音频、猜时间或归并两种文本", job.Summary)
			}
			if test.name == "null-time" && job.Timing.Alignment.StartTimesSeconds[0] != nil {
				t.Fatal("null被补成0")
			}
			if !test.complete && len(job.Warnings) == 0 {
				t.Fatal("缺失/畸形时间未提示")
			}
			m.Close()
			<-m.Done()
			restored, err := New(noRemoteRecovery(t, options))
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			job, err = restored.Get(input.ID)
			if err != nil || job.Status != Succeeded || job.TimingComplete != test.complete || job.Timing == nil || posts.Load() != 1 {
				t.Fatal("原始坏时间证据未只读恢复", err)
			}
		})
	}
}

func TestTimedTTSDiskFailureAndRestartResumeNeverPostAgain(t *testing.T) {
	for _, stage := range []string{"receipt", "probe"} {
		t.Run(stage, func(t *testing.T) {
			m, options, control, project, posts := timedHTTPFixture(t, completeOriginal, completeNormalized, nil)
			writer := m.writeFile
			if stage == "receipt" {
				m.writeFile = func(path string, data []byte) error {
					if filepath.Base(path) == "receipt.json" {
						return errors.New("fixture disk full")
					}
					return writer(path, data)
				}
			} else {
				control.probeFails.Store(true)
			}
			input := timedInput("timed-save-" + stage)
			if _, err := m.Submit(context.Background(), project, input); err != nil {
				t.Fatal(err)
			}
			job := await(t, m, input.ID, Saving)
			if !job.CanResumeLocal || job.Timing == nil || !job.TimingComplete || posts.Load() != 1 {
				t.Fatal("已返回证据没有本地恢复入口", job.Summary)
			}
			if stage == "receipt" {
				m.writeFile = writer
				if _, err := m.ResumeLocal(context.Background(), input.ID); err != nil {
					t.Fatal(err)
				}
				job = await(t, m, input.ID, Succeeded)
				if job.Timing == nil || posts.Load() != 1 {
					t.Fatal("本地写盘恢复发生重POST")
				}
				return
			}
			m.Close()
			<-m.Done()
			control.probeFails.Store(false)
			restored, err := New(noRemoteRecovery(t, options))
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			if _, err := restored.ResumeLocal(context.Background(), input.ID); err != nil {
				t.Fatal(err)
			}
			job = await(t, restored, input.ID, Succeeded)
			if job.Timing == nil || !job.TimingComplete || posts.Load() != 1 {
				t.Fatal("重启探测恢复丢时间或重POST")
			}
		})
	}
}

func TestTimedTTSReceiptTamperingCannotBindOrResubmit(t *testing.T) {
	for _, target := range []string{"audio", "audio-hash", "voice", "model", "raw-timing", "raw-json", "raw-with-new-receipt-hash"} {
		t.Run(target, func(t *testing.T) {
			m, options, _, project, posts := timedHTTPFixture(t, completeOriginal, completeNormalized, nil)
			input := timedInput("timed-tamper-" + target)
			if _, err := m.Submit(context.Background(), project, input); err != nil {
				t.Fatal(err)
			}
			await(t, m, input.ID, Succeeded)
			m.Close()
			<-m.Done()
			path := m.output(input.ID, filepath.Join("audio", "receipt.json"))
			var receipt audioReceipt
			if err := readJSON(path, &receipt, 16<<20); err != nil {
				t.Fatal(err)
			}
			switch target {
			case "audio":
				data, err := os.ReadFile(m.audioPath(input.ID))
				if err != nil {
					t.Fatal(err)
				}
				data[len(data)-1] ^= 1
				if err := os.WriteFile(m.audioPath(input.ID), data, 0600); err != nil {
					t.Fatal(err)
				}
			case "audio-hash":
				receipt.SHA256 = strings.Repeat("a", 64)
			case "voice":
				receipt.Timing.VoiceID = "another-voice"
			case "model":
				receipt.Timing.ModelID = "another-model"
			case "raw-json":
				receipt.Timing.RawAlignment = json.RawMessage(`{"characters":"被改写"}`)
				receipt.Timing.Alignment = nil
			case "raw-timing", "raw-with-new-receipt-hash":
				receipt.Timing.Alignment.Characters[0] = "被改写"
				if target == "raw-with-new-receipt-hash" {
					receipt.TimingSHA256 = timingFingerprint(receipt.Timing)
				}
			}
			data, _ := json.Marshal(receipt)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			restored, err := New(noRemoteRecovery(t, options))
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			job, err := restored.Get(input.ID)
			if err != nil || job.Status != Unknown || job.Timing != nil || job.TimingComplete || job.CanResumeLocal || posts.Load() != 1 {
				t.Fatal("损坏时间/音频被当成已验证结果", job.Summary, err)
			}
			if _, err := restored.ResumeLocal(context.Background(), input.ID); err == nil {
				t.Fatal("损坏证据仍能本地接纳")
			}
			if _, err := restored.Submit(context.Background(), project, input); err != nil || posts.Load() != 1 {
				t.Fatal("损坏结果被重POST", err)
			}
		})
	}
}

func TestTimedTTSCancelDuringRequestIsUnknownWithoutReplay(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	m, options, _, project, posts := timedHTTPFixture(t, "null", "null", func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	})
	defer close(release)
	input := timedInput("timed-cancel-unknown")
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("未进入唯一POST")
	}
	if _, err := m.Cancel(input.ID); err != nil {
		t.Fatal(err)
	}
	await(t, m, input.ID, Unknown)
	m.Close()
	<-m.Done()
	restored, err := New(noRemoteRecovery(t, options))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	job, err := restored.Submit(context.Background(), project, input)
	if err != nil || job.Status != Unknown || posts.Load() != 1 {
		t.Fatal("取消未知提交被重放", err)
	}
}

func TestTimedTTSUnsupportedFailsBeforeIntentAndPlainModeUnchanged(t *testing.T) {
	m, _, control, project := fixture(t, 1_000_000)
	if _, err := m.Submit(context.Background(), project, timedInput("timed-unsupported")); err == nil || control.tts.Load() != 0 {
		t.Fatal("不支持时间的适配器静默回退")
	}
	if _, err := os.Stat(filepath.Join(m.journal, "timed-unsupported.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("不支持时间仍记录了收费提交意图")
	}
	input := synthesizeInput("plain-compatible")
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	job := await(t, m, input.ID, Succeeded)
	if job.TimingRequested || job.Timing != nil || control.tts.Load() != 1 {
		t.Fatal("默认普通配音行为变化")
	}
}

func TestTimedTTSNormalizedFailureDoesNotReplaceCompleteOriginal(t *testing.T) {
	for _, normalized := range []string{"null", `{"characters":["十"],"character_start_times_seconds":[null],"character_end_times_seconds":[0.1]}`, `{"characters":"十二"}`} {
		t.Run(normalized, func(t *testing.T) {
			m, _, _, project, posts := timedHTTPFixture(t, completeOriginal, normalized, nil)
			input := timedInput("timed-normalized-quality")
			if _, err := m.Submit(context.Background(), project, input); err != nil {
				t.Fatal(err)
			}
			job := await(t, m, input.ID, Succeeded)
			if !job.TimingComplete || job.NormalizedTimingComplete || job.Timing == nil || strings.Join(job.Timing.Alignment.Characters, "") != "12" || posts.Load() != 1 || job.NormalizedTimingReason == "" {
				t.Fatal("规范化失败改变了原文证据或冒充可用", job.Summary)
			}
		})
	}
}

func TestTTSLegacyReceiptAndAbsentSampleCountRemainCompatible(t *testing.T) {
	m, options, _, project := fixture(t, 1_000_000)
	input := synthesizeInput("legacy-plain-receipt")
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	await(t, m, input.ID, Succeeded)
	m.Close()
	<-m.Done()
	// 模拟之前版本没有独立probe证据文件、没有Timing可选字段的回执。
	if err := os.Remove(m.output(input.ID, "audio-probe.json")); err != nil {
		t.Fatal(err)
	}
	restored, err := New(noRemoteRecovery(t, options))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	job, err := restored.Get(input.ID)
	if err != nil || job.Status != Succeeded || job.TimingRequested || job.Timing != nil || job.AudioProbe != nil || job.Audio == nil {
		t.Fatal("旧配音回执被升级为时间请求或要求重新生成", err)
	}
}

func TestTimedTTSProbeEvidenceIsSeparateFromSampleCount(t *testing.T) {
	m, options, _, project, posts := timedHTTPFixture(t, completeOriginal, completeNormalized, nil)
	probe := options.Probe
	m.options.ProbeAudio = func(ctx context.Context, path string) (AudioProbe, error) {
		asset, err := probe(ctx, path)
		return AudioProbe{Asset: asset, SampleRate: 44100, Channels: 1, RawMetadata: json.RawMessage(`{"fixture":true,"format":{"duration":"0.156735"}}`)}, err
	}
	input := timedInput("timed-probe-evidence")
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	job := await(t, m, input.ID, Succeeded)
	if job.AudioProbe == nil || job.AudioProbe.SampleCount != nil || job.AudioProbe.SampleRate != 44100 || job.AudioProbe.Channels != 1 || !bytes.Contains(job.AudioProbe.RawMetadata, []byte(`"fixture":true`)) || posts.Load() != 1 {
		t.Fatal("探测证据被丢弃或按时长补造了样本数")
	}
}
