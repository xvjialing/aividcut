package speech

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTimedTTSExplicitEndpointPreservesBothAlignments(t *testing.T) {
	var calls atomic.Int32
	provider, _ := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		requireAuth(t, r)
		if r.URL.Path != "/v1/text-to-speech/voice-1/with-timestamps" || r.URL.Query().Get("output_format") != "mp3_44100_128" {
			t.Error("带时间配音没有使用显式端点")
		}
		var request map[string]string
		if json.NewDecoder(r.Body).Decode(&request) != nil || len(request) != 2 || request["text"] != "12" || request["model_id"] != MultilingualV2 {
			t.Error("带时间配音请求被改写")
		}
		_, _ = io.WriteString(w, `{"audio_base64":"`+base64.StdEncoding.EncodeToString(fixtureMP3)+`","alignment":{"characters":["1","2"],"character_start_times_seconds":[0,0.05],"character_end_times_seconds":[0.05,0.1]},"normalized_alignment":{"characters":["十","二"],"character_start_times_seconds":[0,0.05],"character_end_times_seconds":[0.05,0.1]}}`)
	})
	result, err := provider.SynthesizeWithTiming(context.Background(), SynthesizeRequest{VoiceID: "voice-1", Text: "12"})
	if err != nil || result.Status != Succeeded || !result.TimingComplete || !bytes.Equal(result.Data, fixtureMP3) || result.Alignment == nil || result.NormalizedAlignment == nil || strings.Join(result.Alignment.Characters, "") != "12" || strings.Join(result.NormalizedAlignment.Characters, "") != "十二" || calls.Load() != 1 {
		t.Fatalf("原始/规范化对齐没有分别保留：%+v %v", result.Operation, err)
	}
	if !provider.Capabilities().SpeechTiming.Supported {
		t.Fatal("显式协议能力未声明")
	}
}

func TestTimedTTSMissingAlignmentAndNullTimesAreNotInvented(t *testing.T) {
	for _, alignment := range []string{"null", `{"characters":["字"],"character_start_times_seconds":[null],"character_end_times_seconds":[0.1]}`} {
		provider, _ := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"audio_base64":"`+base64.StdEncoding.EncodeToString(fixtureMP3)+`","alignment":`+alignment+`,"normalized_alignment":{"characters":["字"],"character_start_times_seconds":[0],"character_end_times_seconds":[0.1]}}`)
		})
		result, err := provider.SynthesizeWithTiming(context.Background(), SynthesizeRequest{VoiceID: "voice", Text: "字"})
		if err != nil || result.Status != Succeeded || result.TimingComplete || len(result.Warnings) == 0 || result.NormalizedAlignment == nil {
			t.Fatalf("缺时间仍应保留音频但明确不能绑字：%+v %v", result.Operation, err)
		}
		if alignment == "null" {
			if result.Alignment != nil {
				t.Fatal("不能用normalized结果替代缺失原文对齐")
			}
		} else if result.Alignment.StartTimesSeconds[0] != nil {
			t.Fatal("缺失开始时间被补造")
		}
	}
}

func TestTimedTTSMalformedTimingPreservesAudioAndNeverRetries(t *testing.T) {
	for _, alignment := range []string{
		`{"characters":["字"],"character_start_times_seconds":[],"character_end_times_seconds":[0.1]}`,
		`{"characters":["字"],"character_start_times_seconds":[0.1],"character_end_times_seconds":[0]}`,
		`{"characters":["字"],"character_start_times_seconds":[-1],"character_end_times_seconds":[0.1]}`,
		`{"characters":"字","character_start_times_seconds":[0],"character_end_times_seconds":[0.1]}`,
	} {
		var calls atomic.Int32
		provider, _ := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			_, _ = io.WriteString(w, `{"audio_base64":"`+base64.StdEncoding.EncodeToString(fixtureMP3)+`","alignment":`+alignment+`}`)
		})
		result, err := provider.SynthesizeWithTiming(context.Background(), SynthesizeRequest{VoiceID: "voice", Text: "字"})
		if err != nil || result.Status != Succeeded || result.SubmissionUncertain || !bytes.Equal(result.Data, fixtureMP3) || result.TimingComplete || len(result.Warnings) == 0 || calls.Load() != 1 {
			t.Fatal("坏时间证据不能丢掉已返回音频、伪造可绑定或自动重试")
		}
	}
	provider, _ := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"audio_base64":"not-base64"}`)
	})
	if result, err := provider.SynthesizeWithTiming(context.Background(), SynthesizeRequest{VoiceID: "voice", Text: "字"}); err == nil || result.Status != Unknown {
		t.Fatal("不合法音频必须明确未知提交")
	}
}

func TestPlainTTSCannotSilentlyIgnoreTimingOptIn(t *testing.T) {
	var calls atomic.Int32
	provider, _ := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	_, err := provider.Synthesize(context.Background(), SynthesizeRequest{VoiceID: "voice", Text: "字", WithTiming: true})
	if err == nil || calls.Load() != 0 {
		t.Fatal("显式时间请求不能被普通端点静默忽略")
	}
}
