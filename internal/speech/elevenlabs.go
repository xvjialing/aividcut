package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

func (p *ElevenLabs) Capabilities() Capabilities {
	yes := Feature{Supported: true}
	return Capabilities{
		ProviderID: ElevenLabsID, ASRModels: []string{ScribeV2}, TTSModels: []string{MultilingualV2},
		TimestampGranularities: []string{"word", "character"}, OutputFormats: []string{"mp3_44100_128"},
		Transcription: yes, SpeechSynthesis: yes, VoiceListing: yes,
		SpeechTiming:        Feature{Supported: true, Reason: "仅显式SynthesizeWithTiming调用；普通配音不自动切换端点"},
		InstantCloning:      Feature{Supported: true, Reason: "仅创建IVC音色；不表示身份或权限验证完成"},
		Diarization:         Feature{Supported: true, Reason: "仅Scribe同步ASR的说话人分离"},
		CharacterTimestamps: Feature{Supported: true, Reason: "ASR字符时间戳及可选TTS原始alignment；普通配音仍无对齐结果"},
		RemoteCancellation:  Feature{Reason: "取消本地等待不等于取消远端处理；未确认远端取消协议"},
		Streaming:           Feature{Reason: "本适配器只实现同步批处理"},
		VoiceVerification:   Feature{Reason: "创建音色不代表授权/身份验证完成；仅保留供应商报告状态"},
		MaxInputBytes:       MaxInputBytes, MaxTextCharacters: 10000, MaxCloneFiles: 5,
	}
}

var _ Provider = (*ElevenLabs)(nil)

func (p *ElevenLabs) inputs(ctx context.Context, ids []string, audioOnly bool) ([]*InputFile, error) {
	if p.config.Files == nil {
		return nil, failure("file_scope", "尚未配置已授权媒体读取器")
	}
	var files []*InputFile
	var total int64
	seen := map[string]bool{}
	for _, id := range ids {
		if !safeID.MatchString(id) || seen[id] {
			closeInputs(files)
			return nil, failure("file_scope", "语音素材标识无效或重复")
		}
		seen[id] = true
		file, err := p.config.Files.Open(ctx, id)
		if err != nil {
			closeInputs(files)
			return nil, err
		}
		if file == nil || file.Reader == nil {
			closeInputs(files)
			return nil, failure("file_scope", "素材读取器未返回已授权文件")
		}
		files = append(files, file)
		if file.AssetID != id || audioOnly && (file.Kind != "audio" || !strings.HasPrefix(file.MIME, "audio/")) {
			closeInputs(files)
			return nil, failure("file_type", "音色样本必须为已授权的音频文件")
		}
		total += file.SizeBytes
		if total > MaxInputBytes {
			closeInputs(files)
			return nil, failure("input_limit", "本轮语音上传总量不能超过64MiB")
		}
	}
	return files, nil
}
func closeInputs(inputs []*InputFile) {
	for _, input := range inputs {
		if input != nil && input.Reader != nil {
			_ = input.Reader.Close()
		}
	}
}
func invalidResponse(message string) error {
	return &Error{Code: "response", Message: message, SubmissionUncertain: true}
}
func decodeJSON(data []byte, value any) error {
	if !utf8.Valid(data) {
		return invalidResponse("语音响应不是有效UTF-8，未替换或补造文字")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(value); err != nil {
		return invalidResponse("语音服务返回了无法识别的结果；请核查原请求，不要重复提交")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return invalidResponse("语音响应包含多余数据；请核查原请求")
	}
	return nil
}

func (p *ElevenLabs) Transcribe(parent context.Context, request TranscribeRequest) (Transcription, error) {
	operation, err := newOperation(request.ID, ScribeV2)
	result := Transcription{Operation: operation, AssetID: request.AssetID, Words: []Word{}, Warnings: []string{}}
	if err != nil {
		return result, err
	}
	if request.ModelID != "" && request.ModelID != ScribeV2 {
		return result, failure("model", "当前ASR适配仅支持已核查的scribe_v2")
	}
	if request.Granularity == "" {
		request.Granularity = "word"
	}
	if request.Granularity != "word" && request.Granularity != "character" {
		return result, failure("granularity", "当前ASR时间戳仅支持word或character")
	}
	if request.Language != "" && !languagePattern.MatchString(request.Language) {
		return result, failure("language", "转写语言应为ISO-639-1或ISO-639-3代码")
	}
	ctx, cancel := context.WithTimeout(parent, p.timeout)
	defer cancel()
	files, err := p.inputs(ctx, []string{request.AssetID}, false)
	if err != nil {
		return result, err
	}
	defer closeInputs(files)
	result.SourceSHA256, result.SourceDurationUs = files[0].SHA256, files[0].DurationUs
	fields := map[string]string{"model_id": ScribeV2, "timestamps_granularity": request.Granularity, "diarize": strconv.FormatBool(request.Diarize), "tag_audio_events": strconv.FormatBool(request.TagAudioEvents), "webhook": "false", "use_multi_channel": "false", "no_verbatim": "false"}
	if request.Language != "" {
		fields["language_code"] = request.Language
	}
	body, err := p.multipart(ctx, fields, "file", files)
	if err != nil {
		return result, err
	}
	defer body.Close()
	data, remoteID, err := p.request(ctx, http.MethodPost, "/v1/speech-to-text", body.contentType, body, body.size, 8<<20)
	result.RemoteID = remoteID
	if err != nil {
		result.Operation = failedOperation(result.Operation, err)
		return result, err
	}
	var raw transcriptResponse
	if err = decodeJSON(data, &raw); err == nil {
		err = decodeTranscript(raw, &result, request.Granularity)
	}
	if err != nil {
		result.Operation = failedOperation(result.Operation, err)
		return result, err
	}
	result.Status = Succeeded
	return result, nil
}

func (p *ElevenLabs) Synthesize(parent context.Context, request SynthesizeRequest) (AudioResult, error) {
	operation, err := newOperation(request.ID, MultilingualV2)
	result := AudioResult{Operation: operation, VoiceID: request.VoiceID, OutputFormat: "mp3_44100_128", MIME: "audio/mpeg"}
	if err != nil {
		return result, err
	}
	if request.WithTiming {
		return result, failure("timing", "带时间配音必须显式调用SynthesizeWithTiming；未调用普通配音端点")
	}
	if request.ModelID != "" && request.ModelID != MultilingualV2 {
		return result, failure("model", "当前TTS适配仅支持已核查的eleven_multilingual_v2")
	}
	if !safeID.MatchString(request.VoiceID) {
		return result, failure("voice", "需要有效音色标识")
	}
	if !utf8.ValidString(request.Text) || strings.TrimSpace(request.Text) == "" || utf8.RuneCountInString(request.Text) > 10000 {
		return result, failure("text", "配音文本需为非空且不超过10000字符的UTF-8文本")
	}
	if request.OutputFormat != "" && request.OutputFormat != "mp3_44100_128" {
		return result, failure("output_format", "当前仅核查mp3_44100_128输出，其他格式需单独适配")
	}
	ctx, cancel := context.WithTimeout(parent, p.timeout)
	defer cancel()
	data, _ := json.Marshal(map[string]string{"text": request.Text, "model_id": MultilingualV2})
	raw, remoteID, err := p.request(ctx, http.MethodPost, "/v1/text-to-speech/"+request.VoiceID+"?output_format=mp3_44100_128", "application/json", bytes.NewReader(data), int64(len(data)), MaxResponseBytes)
	result.RemoteID = remoteID
	if err == nil && (len(raw) < 4 || mediaMIME(raw, "audio") != "audio/mpeg") {
		err = invalidResponse("TTS成功响应不是已识别的MP3数据，未作为可用音频返回")
	}
	if err != nil {
		result.Operation = failedOperation(result.Operation, err)
		return result, err
	}
	result.Data, result.Status = raw, Succeeded
	return result, nil
}

func (p *ElevenLabs) CloneVoice(parent context.Context, request CloneRequest) (CloneResult, error) {
	operation, err := newOperation(request.ID, "")
	result := CloneResult{Operation: operation}
	if err != nil {
		return result, err
	}
	if !request.Consent.Confirmed || !utf8.ValidString(request.Consent.Subject) || strings.TrimSpace(request.Consent.Subject) == "" || utf8.RuneCountInString(request.Consent.Subject) > 200 || (request.Consent.Authority != "self" && request.Consent.Authority != "authorized") {
		return result, failure("consent_required", "创建自定义音色需要本人或被授权人的明确确认及主体说明")
	}
	if !utf8.ValidString(request.Name) || strings.TrimSpace(request.Name) == "" || utf8.RuneCountInString(request.Name) > 100 || !utf8.ValidString(request.Description) || utf8.RuneCountInString(request.Description) > 1000 {
		return result, failure("voice_metadata", "音色名称需为1–100字符，描述不超过1000字符")
	}
	if len(request.AssetIDs) < 1 || len(request.AssetIDs) > 5 {
		return result, failure("samples", "本轮自定义音色需要1–5个已授权音频样本")
	}
	ctx, cancel := context.WithTimeout(parent, p.timeout)
	defer cancel()
	files, err := p.inputs(ctx, request.AssetIDs, true)
	if err != nil {
		return result, err
	}
	defer closeInputs(files)
	fields := map[string]string{"name": strings.TrimSpace(request.Name), "remove_background_noise": "false"}
	if request.Description != "" {
		fields["description"] = request.Description
	}
	body, err := p.multipart(ctx, fields, "files", files)
	if err != nil {
		return result, err
	}
	defer body.Close()
	data, remoteID, err := p.request(ctx, http.MethodPost, "/v1/voices/add", body.contentType, body, body.size, 1<<20)
	result.RemoteID = remoteID
	if err != nil {
		result.Operation = failedOperation(result.Operation, err)
		return result, err
	}
	var raw struct {
		VoiceID  string `json:"voice_id"`
		Requires *bool  `json:"requires_verification"`
	}
	if err = decodeJSON(data, &raw); err == nil && (!safeID.MatchString(raw.VoiceID) || raw.Requires == nil) {
		err = invalidResponse("音色创建响应缺少标识或验证状态，请核查原请求")
	}
	if err != nil {
		result.Operation = failedOperation(result.Operation, err)
		return result, err
	}
	result.Status = Succeeded
	result.Voice = Voice{ID: raw.VoiceID, Name: strings.TrimSpace(request.Name), Description: request.Description, Category: "cloned", VerificationState: "unverified", RequiresVerification: raw.Requires}
	return result, nil
}

func (p *ElevenLabs) ListVoices(parent context.Context, request VoiceListRequest) (VoicePage, error) {
	if request.PageSize == 0 {
		request.PageSize = 20
	}
	if request.PageSize < 1 || request.PageSize > 100 || !utf8.ValidString(request.Search) || utf8.RuneCountInString(request.Search) > 256 || len(request.NextPageToken) > 2048 {
		return VoicePage{}, failure("voice_query", "音色查询参数超过本轮限制")
	}
	query := url.Values{"page_size": []string{strconv.Itoa(request.PageSize)}, "include_total_count": []string{"false"}}
	if request.Search != "" {
		query.Set("search", request.Search)
	}
	if request.NextPageToken != "" {
		query.Set("next_page_token", request.NextPageToken)
	}
	ctx, cancel := context.WithTimeout(parent, p.timeout)
	defer cancel()
	data, _, err := p.request(ctx, http.MethodGet, "/v2/voices?"+query.Encode(), "", nil, 0, 4<<20)
	if err != nil {
		return VoicePage{}, err
	}
	var raw struct {
		Voices  *[]voiceResponse `json:"voices"`
		HasMore *bool            `json:"has_more"`
		Next    *string          `json:"next_page_token"`
	}
	if err = decodeJSON(data, &raw); err != nil {
		return VoicePage{}, &Error{Code: "response", Message: "音色列表响应无法识别"}
	}
	if raw.Voices == nil || raw.HasMore == nil || len(*raw.Voices) > 1000 || raw.Next != nil && len(*raw.Next) > 2048 || *raw.HasMore && (raw.Next == nil || *raw.Next == "" || *raw.Next == request.NextPageToken) {
		return VoicePage{}, failure("response", "音色列表或分页状态不完整，未自动翻页")
	}
	page := VoicePage{Voices: []Voice{}, HasMore: *raw.HasMore}
	if raw.Next != nil {
		page.NextPageToken = *raw.Next
	}
	for _, voice := range *raw.Voices {
		if !safeID.MatchString(voice.ID) || !utf8.ValidString(voice.Name) {
			return VoicePage{}, failure("response", "音色列表含无效标识或文字")
		}
		page.Voices = append(page.Voices, Voice{ID: voice.ID, Name: voice.Name, Description: voice.Description, Category: voice.Category, Labels: voice.Labels, VerificationState: "unverified", RequiresVerification: voice.Verification.Requires, ProviderReportedVerified: voice.Verification.Verified, PreviewURL: voice.Preview})
	}
	return page, nil
}

type voiceResponse struct {
	ID           string            `json:"voice_id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Category     string            `json:"category"`
	Labels       map[string]string `json:"labels"`
	Preview      string            `json:"preview_url"`
	Verification struct {
		Requires *bool `json:"requires_verification"`
		Verified *bool `json:"is_verified"`
	} `json:"voice_verification"`
}
type transcriptResponse struct {
	Language    string          `json:"language_code"`
	Probability *float64        `json:"language_probability"`
	Text        *string         `json:"text"`
	Words       *[]wordResponse `json:"words"`
	ID          string          `json:"transcription_id"`
	Duration    *float64        `json:"audio_duration_secs"`
}
type timedResponse struct {
	Text  string   `json:"text"`
	Start *float64 `json:"start"`
	End   *float64 `json:"end"`
}
type wordResponse struct {
	timedResponse
	Type           string          `json:"type"`
	SpeakerID      string          `json:"speaker_id"`
	LogProbability *float64        `json:"logprob"`
	Characters     []timedResponse `json:"characters"`
}

func decodeTranscript(raw transcriptResponse, result *Transcription, granularity string) error {
	if raw.Text == nil || raw.Words == nil || !utf8.ValidString(*raw.Text) || len(*raw.Words) > 200000 || raw.Probability != nil && (math.IsNaN(*raw.Probability) || math.IsInf(*raw.Probability, 0) || *raw.Probability < 0 || *raw.Probability > 1) {
		return invalidResponse("转写结果缺少原文/词数据或置信度无效")
	}
	if raw.ID != "" {
		if !safeID.MatchString(raw.ID) {
			return invalidResponse("转写结果标识无效")
		}
		result.RemoteID = raw.ID
	}
	if raw.Duration != nil {
		duration, err := seconds(raw.Duration)
		if err != nil {
			return err
		}
		result.ReportedAudioDurationUs = duration
	}
	result.Text, result.Language, result.LanguageProbability, result.Granularity = *raw.Text, raw.Language, raw.Probability, granularity
	result.TimingComplete = true
	var lastStart int64 = -1
	wordCount := 0
	for _, word := range *raw.Words {
		if word.LogProbability != nil && (math.IsNaN(*word.LogProbability) || math.IsInf(*word.LogProbability, 0) || *word.LogProbability > 0) {
			return invalidResponse("词项置信度无效")
		}
		if word.Type != "word" && word.Type != "spacing" && word.Type != "audio_event" {
			return invalidResponse("转写包含尚未适配的词项类型，未将其当作普通字幕")
		}
		span, err := decodeSpan(word.timedResponse, result.SourceDurationUs)
		if err != nil {
			return err
		}
		if !withinReportedDuration(span, result.ReportedAudioDurationUs) {
			return invalidResponse("词时间戳超出供应商报告的音频时长")
		}
		if span.StartUs != nil {
			if *span.StartUs < lastStart {
				return invalidResponse("转写时间戳顺序无效")
			}
			lastStart = *span.StartUs
		}
		value := Word{TimedText: span, Type: word.Type, SpeakerID: word.SpeakerID, LogProbability: word.LogProbability}
		value.TimingComplete = word.Type == "word" && span.StartUs != nil && span.EndUs != nil && *span.EndUs > *span.StartUs
		if word.Type == "word" {
			wordCount++
			if strings.TrimSpace(word.Text) == "" {
				return invalidResponse("普通词项缺少可用文字")
			}
			if !value.TimingComplete {
				result.TimingComplete = false
			}
		}
		var lastCharacter int64 = -1
		for _, character := range word.Characters {
			part, err := decodeSpan(character, result.SourceDurationUs)
			if err != nil {
				return err
			}
			if !withinReportedDuration(part, result.ReportedAudioDurationUs) {
				return invalidResponse("字符时间戳超出供应商报告的音频时长")
			}
			if part.StartUs != nil && (span.StartUs != nil && *part.StartUs < *span.StartUs || *part.StartUs < lastCharacter) || part.EndUs != nil && span.EndUs != nil && *part.EndUs > *span.EndUs {
				return invalidResponse("字符时间戳超出词区间或顺序无效")
			}
			if part.StartUs != nil {
				lastCharacter = *part.StartUs
			}
			if granularity == "character" && word.Type == "word" && (part.StartUs == nil || part.EndUs == nil || *part.StartUs == *part.EndUs) {
				result.TimingComplete = false
			}
			value.Characters = append(value.Characters, part)
		}
		if granularity == "character" && word.Type == "word" && len(word.Characters) == 0 {
			result.TimingComplete = false
		}
		result.Words = append(result.Words, value)
	}
	if strings.TrimSpace(result.Text) != "" && wordCount == 0 {
		result.TimingComplete = false
	}
	if !result.TimingComplete {
		result.Warnings = append(result.Warnings, "部分词或字符时间戳缺失/退化，不能直接据此自动剪辑；未补造时间戳")
	}
	return nil
}
func withinReportedDuration(value TimedText, duration *int64) bool {
	return duration == nil || (value.StartUs == nil || *value.StartUs <= *duration) && (value.EndUs == nil || *value.EndUs <= *duration)
}
func seconds(value *float64) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > float64(math.MaxInt64)/1e6-1 {
		return nil, invalidResponse("转写时间戳超出有效范围")
	}
	converted := int64(math.Round(*value * 1e6))
	return &converted, nil
}
func decodeSpan(value timedResponse, duration int64) (TimedText, error) {
	if !utf8.ValidString(value.Text) {
		return TimedText{}, invalidResponse("转写词项包含无效文字")
	}
	start, err := seconds(value.Start)
	if err != nil {
		return TimedText{}, err
	}
	end, err := seconds(value.End)
	if err != nil {
		return TimedText{}, err
	}
	if start != nil && end != nil && *end < *start || duration > 0 && (start != nil && *start > duration || end != nil && *end > duration) {
		return TimedText{}, invalidResponse("转写时间戳为倒置区间或超出源素材时长")
	}
	return TimedText{Text: value.Text, StartUs: start, EndUs: end}, nil
}
