package speech

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"aividcut/internal/generation"
)

const fixtureKey = "speech-fixture-key-do-not-display"

//go:embed testdata/silence.mp3
var fixtureMP3 []byte

//go:embed testdata/clip.mp4
var fixtureVideo []byte

func fixtureWAV() []byte {
	data := make([]byte, 44+32000*2)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 16000)
	binary.LittleEndian.PutUint32(data[28:], 32000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(len(data)-44))
	return data
}
func fixtureFiles(t *testing.T) *FileSource {
	t.Helper()
	path := filepath.Join(t.TempDir(), "私有原始录音.wav")
	if err := os.WriteFile(path, fixtureWAV(), 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(fixtureWAV())
	files, err := NewFileSource([]RegisteredFile{{ID: "audio-1", Path: path, Kind: "audio", DurationUs: 2_000_000, ExpectedSHA256: hex.EncodeToString(hash[:])}, {ID: "audio-2", Path: path, Kind: "audio", DurationUs: 2_000_000}})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
func fixtureProvider(t *testing.T, handler http.HandlerFunc) (*ElevenLabs, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	provider, err := NewElevenLabs(Config{BaseURL: server.URL, HTTPClient: server.Client(), AllowLocalTestHTTP: true, WorkDir: t.TempDir(), Files: fixtureFiles(t), Secrets: generation.SecretFunc(func(_ context.Context, id string) (string, error) {
		if id != ElevenLabsID {
			t.Errorf("错误凭据作用域：%s", id)
		}
		return fixtureKey, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	return provider, server
}
func requireAuth(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Header.Get("xi-api-key") != fixtureKey || r.Header.Get("Authorization") != "" {
		t.Error("没有使用ElevenLabs认证协议")
	}
}

func TestASRMultipartPreservesWordSpacingEventAndCharacters(t *testing.T) {
	var calls atomic.Int32
	provider, _ := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		requireAuth(t, r)
		if r.Method != "POST" || r.URL.Path != "/v1/speech-to-text" {
			t.Error("错误ASR端点")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		for key, value := range map[string]string{"model_id": ScribeV2, "timestamps_granularity": "character", "diarize": "true", "tag_audio_events": "true", "webhook": "false", "use_multi_channel": "false", "no_verbatim": "false", "language_code": "zh"} {
			if r.FormValue(key) != value {
				t.Errorf("%s参数错误", key)
			}
		}
		if r.FormValue("source_url") != "" || r.FormValue("cloud_storage_url") != "" {
			t.Error("不得上传任意URL")
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		if !bytes.Equal(data, fixtureWAV()) || strings.Contains(header.Filename, "私有") || header.Header.Get("Content-Type") != "audio/wav" {
			t.Error("上传文件内容、名称脱敏或MIME无效")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"language_code":"zh","language_probability":0.98,"text":"你好 (笑)","transcription_id":"transcript-1","audio_duration_secs":2,"words":[{"text":"你好","start":0.125001,"end":0.8,"type":"word","speaker_id":"speaker_0","characters":[{"text":"你","start":0.125001,"end":0.4},{"text":"好","start":0.4,"end":0.8}]},{"text":" ","start":0.8,"end":0.8,"type":"spacing"},{"text":"(笑)","start":null,"end":null,"type":"audio_event"}]}`)
	})
	result, err := provider.Transcribe(context.Background(), TranscribeRequest{AssetID: "audio-1", Language: "zh", Granularity: "character", Diarize: true, TagAudioEvents: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Succeeded || !result.TimingComplete || result.RemoteID != "transcript-1" || result.SourceDurationUs != 2_000_000 || result.ReportedAudioDurationUs == nil || len(result.Words) != 3 || *result.Words[0].StartUs != 125001 || !result.Words[0].TimingComplete || len(result.Words[0].Characters) != 2 {
		t.Fatalf("转写映射无效：%+v", result)
	}
	if result.Words[1].Type != "spacing" || result.Words[1].TimingComplete || *result.Words[1].StartUs != *result.Words[1].EndUs || result.Words[2].StartUs != nil || result.Words[2].Type != "audio_event" {
		t.Fatal("空白/声音事件被补造或当成可绑定普通词")
	}
	if calls.Load() != 1 {
		t.Fatal("不得自动重试ASR")
	}
	if entries, _ := os.ReadDir(provider.config.WorkDir); len(entries) != 0 {
		t.Fatal("请求结束遗留上传缓冲")
	}
}

func TestASRRejectsInvalidTimestampsAndKeepsMissingTimes(t *testing.T) {
	for _, test := range []struct {
		name, response string
		invalid        bool
	}{
		{"negative", `{"text":"词","words":[{"text":"词","type":"word","start":-0.1,"end":0.3}]}`, true},
		{"reverse", `{"text":"词","words":[{"text":"词","type":"word","start":0.8,"end":0.3}]}`, true},
		{"outside-source", `{"text":"词","words":[{"text":"词","type":"word","start":0.8,"end":3}]}`, true},
		{"character-outside", `{"text":"词","words":[{"text":"词","type":"word","start":0.1,"end":0.3,"characters":[{"text":"词","start":0.1,"end":0.4}]}]}`, true},
		{"incomplete", `{"text":"词","words":[{"text":"词","type":"word","start":null,"end":null}]}`, false},
		{"degenerate", `{"text":"词","words":[{"text":"词","type":"word","start":0.3,"end":0.3}]}`, false},
		{"async-shape", `{"message":"queued","request_id":"other"}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider, _ := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				_, _ = io.WriteString(w, test.response)
			})
			result, err := provider.Transcribe(context.Background(), TranscribeRequest{AssetID: "audio-1"})
			if test.invalid {
				if err == nil || result.Status != Unknown || !result.SubmissionUncertain {
					t.Fatalf("非法已提交响应不能当成功：%+v %v", result, err)
				}
			} else if err != nil || result.TimingComplete || result.Words[0].TimingComplete || len(result.Warnings) == 0 {
				t.Fatalf("缺失/退化时间应明确不可绑定：%+v %v", result, err)
			}
		})
	}
}

func TestTTSPreservesTextAndReturnsPrivateAudio(t *testing.T) {
	provider, _ := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.Method != "POST" || r.URL.Path != "/v1/text-to-speech/voice-1" || r.URL.Query().Get("output_format") != "mp3_44100_128" {
			t.Error("错误TTS协议")
		}
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 2 || body["text"] != "今天是2026年，欢迎使用。" || body["model_id"] != MultilingualV2 {
			t.Error("没有完整保留配音原文/模型")
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("request-id", "tts-remote-1")
		_, _ = w.Write(fixtureMP3)
	})
	result, err := provider.Synthesize(context.Background(), SynthesizeRequest{VoiceID: "voice-1", Text: "今天是2026年，欢迎使用。"})
	if err != nil || result.Status != Succeeded || !bytes.Equal(result.Data, fixtureMP3) || result.RemoteID != "tts-remote-1" {
		t.Fatalf("TTS结果无效：%+v %v", result.Operation, err)
	}
	encoded, _ := json.Marshal(result)
	if bytes.Contains(encoded, []byte("Data")) || bytes.Contains(encoded, fixtureMP3[:20]) {
		t.Fatal("二进制音频不应进入普通UI JSON")
	}
	if provider.Capabilities().VoiceVerification.Supported || provider.Capabilities().RemoteCancellation.Supported || provider.Capabilities().Streaming.Supported {
		t.Fatal("不能虚报未接通能力")
	}
	capabilities, _ := json.Marshal(provider.Capabilities())
	if bytes.Contains(capabilities, []byte(`"Transcription"`)) || !bytes.Contains(capabilities, []byte(`"speechSynthesis"`)) {
		t.Fatal("能力字段需要camelCase")
	}
}

func TestVoiceListPaginationAndCloneRemainUnverified(t *testing.T) {
	var cloned atomic.Int32
	provider, _ := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.Method == http.MethodGet {
			if r.URL.Path != "/v2/voices" || r.URL.Query().Get("next_page_token") != "page +&" || r.URL.Query().Get("search") != "中文 & 测试" || r.URL.Query().Get("include_total_count") != "false" {
				t.Error("音色列表/分页协议错误")
			}
			_, _ = io.WriteString(w, `{"voices":[{"voice_id":"voice-1","name":"音色","category":"cloned","voice_verification":{"requires_verification":false,"is_verified":true},"preview_url":"https://private.example/preview?secret=signed"}],"has_more":true,"next_page_token":"page-2"}`)
			return
		}
		cloned.Add(1)
		if r.URL.Path != "/v1/voices/add" {
			t.Error("错误clone端点")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		if len(r.MultipartForm.File["files"]) != 2 || r.FormValue("name") != "我的音色" || r.FormValue("remove_background_noise") != "false" || r.FormValue("consent") != "" {
			t.Error("clone表单错误")
		}
		_, _ = io.WriteString(w, `{"voice_id":"new-voice","requires_verification":false}`)
	})
	page, err := provider.ListVoices(context.Background(), VoiceListRequest{Search: "中文 & 测试", NextPageToken: "page +&", PageSize: 10})
	if err != nil || !page.HasMore || page.NextPageToken != "page-2" || len(page.Voices) != 1 || page.Voices[0].VerificationState != "unverified" {
		t.Fatalf("音色分页无效：%+v %v", page, err)
	}
	encoded, _ := json.Marshal(page)
	if bytes.Contains(encoded, []byte("signed")) {
		t.Fatal("潜在签名预览地址不能进入普通JSON")
	}
	result, err := provider.CloneVoice(context.Background(), CloneRequest{Name: "我的音色", AssetIDs: []string{"audio-1", "audio-2"}, Consent: CloneConsent{Confirmed: true, Subject: "本人", Authority: "self"}})
	if err != nil || result.Status != Succeeded || result.Voice.ID != "new-voice" || result.Voice.VerificationState != "unverified" || result.Voice.RequiresVerification == nil || *result.Voice.RequiresVerification || cloned.Load() != 1 {
		t.Fatalf("创建音色不代表验证完成：%+v %v", result, err)
	}
}

func TestUnknownSubmissionNoRetriesAndNoSecretLeak(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusTemporaryRedirect, http.StatusTooManyRequests} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var calls atomic.Int32
			provider, _ := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "https://do-not-follow.example/")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, fixtureKey)
			})
			result, err := provider.Synthesize(context.Background(), SynthesizeRequest{VoiceID: "voice-1", Text: "测试"})
			uncertain := status != http.StatusTooManyRequests
			if err == nil || strings.Contains(err.Error(), fixtureKey) || result.SubmissionUncertain != uncertain || calls.Load() != 1 {
				t.Fatalf("失败未正确区分未知提交：%+v %v", result.Operation, err)
			}
		})
	}
}

func TestCancellationAndResponseLimit(t *testing.T) {
	arrived := make(chan struct{})
	release := make(chan struct{})
	provider, server := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(arrived)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	defer func() { close(release); server.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		result, err := provider.Synthesize(ctx, SynthesizeRequest{VoiceID: "voice-1", Text: "测试"})
		if !result.SubmissionUncertain {
			done <- errors.New("已送达后取消应为未知提交")
			return
		}
		done <- err
	}()
	<-arrived
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("取消未保留上下文状态：%v", err)
	}
	provider, _ = fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write(bytes.Repeat([]byte("x"), 100))
	})
	_, _, err := provider.request(context.Background(), http.MethodPost, "/fixture", "", nil, 0, 32)
	var responseError *Error
	if !errors.As(err, &responseError) || !responseError.SubmissionUncertain || responseError.Code != "response_limit" {
		t.Fatalf("未限制响应：%v", err)
	}
}

func TestValidationStopsBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	provider, _ := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) })
	if _, err := provider.CloneVoice(context.Background(), CloneRequest{Name: "音色", AssetIDs: []string{"audio-1"}}); err == nil {
		t.Fatal("缺明确授权不能创建音色")
	}
	if _, err := provider.Synthesize(context.Background(), SynthesizeRequest{VoiceID: "../unsafe", Text: "测试"}); err == nil {
		t.Fatal("路径型音色标识不可进入URL")
	}
	if _, err := provider.Synthesize(context.Background(), SynthesizeRequest{VoiceID: "voice", Text: "测试", ModelID: "eleven_v4"}); err == nil {
		t.Fatal("未经适配模型不能按默认参数调用")
	}
	if _, err := provider.Transcribe(context.Background(), TranscribeRequest{AssetID: "https://arbitrary.example/video.mp4"}); err == nil {
		t.Fatal("不能以URL代替已登记素材")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := provider.Synthesize(ctx, SynthesizeRequest{VoiceID: "voice", Text: "测试"})
	if !errors.Is(err, context.Canceled) || result.SubmissionUncertain || calls.Load() != 0 {
		t.Fatal("请求前取消/校验失败不应上传或变成未知提交")
	}
	for _, endpoint := range []string{"http://127.0.0.1:1234", "https://user:secret@api.elevenlabs.io", "http://example.com", "https://api.example.com?key=private"} {
		if _, err := NewElevenLabs(Config{BaseURL: endpoint}); err == nil {
			t.Errorf("不应接受端点：%s", endpoint)
		}
	}
}

func TestRegisteredFilesAreBoundedImmutableAndPrivate(t *testing.T) {
	files := fixtureFiles(t)
	input, err := files.Open(context.Background(), "audio-1")
	if err != nil {
		t.Fatal(err)
	}
	reader := input.Reader.(*snapshotReader)
	path := reader.Name()
	if err := input.Reader.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("关闭后必须清理私有快照")
	}
	if _, err := files.Open(context.Background(), "unregistered"); err == nil {
		t.Fatal("不能读取未登记文件")
	}
	registered := files.files["audio-1"]
	data := fixtureWAV()
	data[len(data)-1] = 1
	if err := os.WriteFile(registered.value.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(registered.value.Path, registered.info.ModTime(), registered.info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Open(context.Background(), "audio-1"); err == nil {
		t.Fatal("相同长度和时间也必须核查已登记指纹")
	}
	if _, err := NewFileSource([]RegisteredFile{{ID: "url", Path: "https://example.com/audio.wav", Kind: "audio"}}); err == nil {
		t.Fatal("不能登记网络URL")
	}
	if _, err := NewFileSource([]RegisteredFile{{ID: "dir", Path: t.TempDir(), Kind: "audio"}}); err == nil {
		t.Fatal("不能登记目录")
	}
}

func TestMissingCharactersDoNotInventTiming(t *testing.T) {
	text := "字"
	start, end := 0.0, 0.5
	words := []wordResponse{{timedResponse: timedResponse{Text: text, Start: &start, End: &end}, Type: "word"}}
	result := Transcription{Words: []Word{}, Warnings: []string{}}
	if err := decodeTranscript(transcriptResponse{Text: &text, Words: &words}, &result, "character"); err != nil {
		t.Fatal(err)
	}
	if result.TimingComplete || !result.Words[0].TimingComplete || len(result.Words[0].Characters) != 0 || result.SourceDurationUs != 0 {
		t.Fatal("字符缺失不能均匀分配或伪造源时长")
	}
}

func TestRegisteredFingerprintAcceptsCanonicalMediaPrefix(t *testing.T) {
	files := fixtureFiles(t)
	registered := files.files["audio-1"].value
	registered.ExpectedSHA256 = "sha256:" + registered.ExpectedSHA256
	withPrefix, err := NewFileSource([]RegisteredFile{registered})
	if err != nil {
		t.Fatal(err)
	}
	input, err := withPrefix.Open(context.Background(), registered.ID)
	if err != nil {
		t.Fatal(err)
	}
	input.Reader.Close()
	registered.ExpectedSHA256 = "sha256:" + strings.Repeat("0", 64)
	wrong, err := NewFileSource([]RegisteredFile{registered})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = wrong.Open(context.Background(), registered.ID); err == nil {
		t.Fatal("接受前缀不得跳过真实内容指纹校验")
	}
	registered.ExpectedSHA256 = "sha256:"
	if _, err := NewFileSource([]RegisteredFile{registered}); err == nil {
		t.Fatal("空的指纹前缀不能退化为跳过登记指纹验证")
	}
}

func TestRegisteredVideoCanTranscribeButCannotBecomeVoiceSample(t *testing.T) {
	var calls atomic.Int32
	provider, _ := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		f, header, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer f.Close()
		data, _ := io.ReadAll(f)
		if !bytes.Equal(data, fixtureVideo) || header.Header.Get("Content-Type") != "video/mp4" {
			t.Error("没有上传已登记视频的原始字节")
		}
		_, _ = io.WriteString(w, `{"text":"","words":[],"audio_duration_secs":0.12}`)
	})
	path := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(path, fixtureVideo, 0600); err != nil {
		t.Fatal(err)
	}
	files, err := NewFileSource([]RegisteredFile{{ID: "video-1", Path: path, Kind: "video", DurationUs: 120000}})
	if err != nil {
		t.Fatal(err)
	}
	provider.config.Files = files
	if result, err := provider.Transcribe(context.Background(), TranscribeRequest{AssetID: "video-1"}); err != nil || result.Status != Succeeded {
		t.Fatalf("合法已授权视频转写协议失败：%+v %v", result.Operation, err)
	}
	if _, err := provider.CloneVoice(context.Background(), CloneRequest{Name: "音色", AssetIDs: []string{"video-1"}, Consent: CloneConsent{Confirmed: true, Subject: "本人", Authority: "self"}}); err == nil || calls.Load() != 1 {
		t.Fatal("视频不能冒充IVC音频样本上传")
	}
}

func TestOversizedInputAndInvalidSuccessfulOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "too-large.wav")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(MaxInputBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := NewFileSource([]RegisteredFile{{ID: "large", Path: path, Kind: "audio"}}); err == nil {
		t.Fatal("不能把超过64MiB的源文件当成本轮可上传素材")
	}
	provider, _ := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"message":"`+fixtureKey+`"}`) })
	result, err := provider.Synthesize(context.Background(), SynthesizeRequest{VoiceID: "voice", Text: "测试"})
	if err == nil || result.Status != Unknown || !result.SubmissionUncertain || len(result.Data) != 0 || strings.Contains(err.Error(), fixtureKey) {
		t.Fatal("成功HTTP中的非法音频不能成为可用音频或泄露响应内容")
	}
}
