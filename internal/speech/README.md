# 语音与自定义音色适配

核查日期：2026-10-25。关联需求 FW-R03（字幕）、FW-R04（素材分析）、FW-R05（指定/自定义音色）、FW-R11（模块化）。本包实现同步 ASR/TTS/音色协议，不修改工程、分析仓库或时间线。

本轮只使用本地 HTTP fixture 和自建小媒体样本。未读取真实密钥、未调用真实 ElevenLabs 账户、未进行收费推理，也未证明账户、套餐、地区或实际生成质量可用。能力声明表示此适配器实现的协议范围。

## 接口

```go
NewElevenLabs(Config) (*ElevenLabs, error)
NewFileSource([]RegisteredFile) (*FileSource, error)

ASRProvider.Transcribe(ctx, TranscribeRequest) (Transcription, error)
TTSProvider.Synthesize(ctx, SynthesizeRequest) (AudioResult, error)
VoiceProvider.ListVoices(ctx, VoiceListRequest) (VoicePage, error)
VoiceProvider.CloneVoice(ctx, CloneRequest) (CloneResult, error)
TimedTTSProvider.SynthesizeWithTiming(ctx, SynthesizeRequest) (TimedAudioResult, error)
```

基础 `Provider` 组合 ASR/TTS/Voice 三个接口，TimedTTSProvider 是可选扩展；通过 `Capabilities()` 返回模型、功能、格式和本轮限制。`Config.Secrets` 复用 `generation.SecretSource`，仅发起请求时按 `providerID="elevenlabs"` 取密钥，不进入工程/配方/UI JSON。`Config.Files` 为只接受 AssetID 的 `MediaSource`。BaseURL/HTTPClient 可注入，loopback HTTP 仅供显式 fixture；正式配置要求非本地 HTTPS，禁止带凭据、查询参数和自动重定向。

应用为当前已经授权的素材创建 FileSource，例如：

```go
files, err := speech.NewFileSource([]speech.RegisteredFile{{
    ID: asset.ID, Path: asset.Path, Kind: asset.Kind,
    DurationUs: asset.DurationUs, ExpectedSHA256: asset.Fingerprint,
}})
// 检查 err 后，将 files 和凭据源注入适配器。
provider, err := speech.NewElevenLabs(speech.Config{Files: files, Secrets: secrets})
```

输入请求不接受文件路径、任意 URL 或目录。登记只接受音频/视频普通文件；读取时再次核查已登记的解析路径、文件身份、尺寸、修改时间和可选 SHA256，并制作私有快照。multipart 再核查长度、摘要和媒体头部，文件名称统一为 source-N，不向云端传原路径/文件名。快照及上传缓冲在结束后清理，以磁盘缓冲限制大文件的内存峰值。

文件检查覆盖容器头部与指纹，不替代 ffprobe 解码校验。应用应先使用已有媒体模块确认视频有音轨、记录真实时长与指纹；不能把无音轨视频或不完整媒体标为已完成识别。当前支持识别 WAV、MP3、FLAC、Ogg、MP4/M4A、WebM、AVI、AIFF 容器头部。

## 已实现的 ElevenLabs 协议

| 功能 | 本轮明确范围 |
| --- | --- |
| ASR | POST `/v1/speech-to-text`，multipart 本地 file，`model_id=scribe_v2` |
| 时间戳 | word/character；保留普通词、spacing、audio_event、speaker_id、可选 logprob 和字符区间 |
| 说话人分离 | 显式 diarize；缺失 speaker_id 不补造 |
| TTS | POST `/v1/text-to-speech/{voice_id}`，JSON text/model_id，仅 `eleven_multilingual_v2` 与 `mp3_44100_128` |
| 音色列表 | GET `/v2/voices`，search/page_size/next_page_token；依据 has_more 翻页，不依赖 total_count、不自动翻页 |
| 自定义音色 | POST `/v1/voices/add`，multipart name/files；保留 voice_id/requires_verification |

ASR 固定同步、单混音输入，`webhook=false`、`use_multi_channel=false`、`no_verbatim=false`。不会额外启用改写原文、实体识别、关键字加价或说话人库。`TagAudioEvents` 是显式布尔参数，调用方希望声音事件时应传 true。TTS 不自动切换到 v4 等未经逐项适配的模型，也不通过字符比例伪造音频对齐。

`Transcription` 的时间单位为整数微秒，以源文件时间为原点，保留 AssetID/SHA256/已登记源时长。供应商报告的音频时长另存 `ReportedAudioDurationUs`，不覆盖本地时长。接到倒置、负数、超范围、乱序或超出已知源/词区间的时间戳时拒绝结果；缺失时间保留 nil，零时长 spacing 原样保留。

`Word.TimingComplete` 仅在普通词有完整正时长区间时为 true。上游只能选择 Type=word 且该值为 true 的词绑定编辑核心；spacing/audio_event 是原始证据，不能充当普通字幕词。`Transcription.TimingComplete` 表示请求粒度的普通词时间戳完整，字符粒度缺字符会为 false；普通词时间完整不代表字符时间也完整。非空原文却没有普通词时也为 false。Speaker、原话、缺失项和时间范围均不能按文本长度补造。

`AudioResult.Data` 是私有候选 MP3 字节，不进入普通 JSON；响应只做格式头部和长度检查。应用应写入受控素材目录后调用媒体 probe 获取真实时长、指纹，再通过统一编辑命令放入时间线。普通 Synthesize 的协议和行为不变，没有自动启用对齐或试听 UI。

2026-10-25 增加可选 TimedTTSProvider.SynthesizeWithTiming(ctx,SynthesizeRequest)，显式调用 `/v1/text-to-speech/{voice_id}/with-timestamps`，模型与MP3格式仍限本包已核查范围，不自动升级、回退或重试该收费端点。TimedAudioResult 嵌入 AudioResult，分别保留 Alignment 与 NormalizedAlignment 的原始字符/秒数组，二者不能互相替代。null对象和null时间项原样保留，不按字符比例补时间。TimingComplete仅表示原文数组完整；空白/标点可有零时长，绑定前还需实际音频probe核查时间范围及编辑核心校验。本轮只有本地HTTP fixture，不宣称供应商真实字级对齐质量已通过。

`SynthesizeRequest.WithTiming` 用于持久任务显式选择扩展；直接调用普通 `Synthesize` 却携带true会在POST前拒绝，防止静默忽略选择。带时间响应中合法MP3与对齐质量独立处理：缺失/null/数组乱序或长度无效不会丢弃已经返回的合法音频，也不触发重发；原始数组保留，无法映射为数组的字段原JSON保留在 `RawAlignment/RawNormalizedAlignment`，给出 `TimingReason` 与warning。`AssessTiming`/`ValidateCharacterAlignment`只检查时间结构，不比对请求文本或实际音频范围，normalized结果独立处理。音频本身不合法、完整JSON不可解析或网络中断仍为未知提交。持久回执、本地恢复与原始时间指纹由 `internal/speechjobs` 负责。

CloneRequest 必须显式包含 `Consent.Confirmed=true`、主体和 self/authorized 声明。此字段只记录用户确认，不代表身份验证。创建成功仅表示供应商返回了音色 ID，Voice.VerificationState 始终为 unverified；requires_verification=false 也不会被改写为已验证。音色列表的供应商验证 bool 另行保留，预览地址不会自动下载或进入普通 JSON。

## 资源、取消与未知提交

- 当前应用级总上传限制为 **64MiB**，IVC 为 1–5 个音频样本，合计也不超过该上限。这不是 ElevenLabs 的最终上传能力。本包自身不提取大视频；长源准备由 `internal/media` 的 PrepareSpeechAudio 提供，实际跨块流程由应用协调器另接，不能直接将任意大视频传给本包。FileSource接受sha256:前缀并仍严格校验真实内容，空前缀不能降级成跳过验证。
- TTS 文本最多 10000 Unicode 字符，不自动拆分长文；MP3 响应最多 64MiB，ASR JSON 8MiB，音色列表 4MiB，创建响应 1MiB。
- 带时间配音的JSON响应上限96MiB，解码音频仍最多64MiB，可用时间结构每套最多100000项。检查数组长度、非负有限时间、顺序与区间；畸形时间只作原始证据，不当作成功绑定依据，持久时间回执另有16MiB上限。
- 每次调用有总超时并遵守 context 取消。请求前失败不标已提交；请求发出后连接中断、取消、超时、5xx/部分异常状态、成功响应读失败/不合法等会给出 `Status=unknown` 和 `SubmissionUncertain=true`。已知请求/转写 ID 尽量保留。
- 不自动重试 POST，不设置可重放请求体，不跟随鉴权重定向。未知状态应保留原操作供核查，不能自动重做产生重复费用。取消本地等待不表示远端处理已取消；没有宣称远端取消能力。
- 错误不展示原始供应商响应或认证诊断内容，保留机器可读错误码和 HTTP 状态。音色分页 URL 参数用标准编码，VoiceID 在拼路径前校验。

## 验证

`go test ./internal/speech -race -v` 覆盖真实本地 HTTP、multipart/认证/字段、真实小 MP3 返回、授权 WAV/视频字节、词/空白/声音事件和字符映射、时间戳拒绝/缺失、分页、明确 clone 确认与未验证状态、未知提交、取消、响应/文件上限、无自动重试、路径/指纹变化和私有缓冲清理。

`testdata/silence.mp3` 与 `testdata/clip.mp4` 为本机 FFmpeg 从 0.12 秒静音/纯色输入生成的自建样本，不含任何人物声音或供应商模型输出。ffprobe 核查 MP3 为 44.1kHz、容器时长 0.156735 秒（包含编码填充）；MP4 为 H.264/AAC、音轨 16kHz、0.12 秒。运行测试不需要 FFmpeg；fixture 通过不等于实际账户的转写、发音、音色克隆或计费验证。`go vet` 与 Windows/Linux amd64 交叉编译通过，交叉编译不等于实机通过。

## 官方来源

- [创建转写](https://elevenlabs.io/docs/api-reference/speech-to-text/convert)：输入、同步方式与粒度；[官方 SDK 词模型](https://github.com/elevenlabs/elevenlabs-python/blob/main/src/elevenlabs/types/speech_to_text_word_response_model.py)、[字符模型](https://github.com/elevenlabs/elevenlabs-python/blob/main/src/elevenlabs/types/speech_to_text_character_response_model.py) 补充展开字段。
- [创建语音](https://elevenlabs.io/docs/api-reference/text-to-speech/convert)、[模型目录](https://elevenlabs.io/docs/overview/models)：本轮默认模型、输出及字数范围。
- [带时间创建语音](https://elevenlabs.io/docs/api-reference/text-to-speech/convert-with-timestamps)：显式可选端点、audio_base64和两套可空alignment。
- [音色列表](https://elevenlabs.io/docs/api-reference/voices/search)：v2 分页契约。
- [创建 IVC](https://elevenlabs.io/docs/api-reference/voices/ivc/create)、[官方 SDK multipart 实现](https://github.com/elevenlabs/elevenlabs-python/blob/main/src/elevenlabs/voices/ivc/raw_client.py)：files 字段及创建结果。

后续接入新模型、PCM、流式、声音验证、强制对齐或第二供应商时需单独核查官方协议、能力与账户限制，本轮不将其列为已完成。
