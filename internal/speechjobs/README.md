# 持久语音任务协调

本包负责源转写、配音、自定义音色的持久任务，不修改工程、转写接受版本或时间线。所有收费操作由显式 `Consent` 开始；没有真实账户验收。本轮测试使用自建静音 WAV/MP3、假 Provider 和本地探测 fixture，不读取系统凭据，不调用真实收费模型。

## 稳定接口

```go
New(Options) (*Manager, error)
(*Manager).Submit(ctx, editor.Project, Input) (Job, error)
(*Manager).Get(id) (Job, error)
(*Manager).List() []Summary
(*Manager).Cancel(id) (Job, error)
(*Manager).ResumeLocal(ctx, id) (Job, error)
(*Manager).AcknowledgeUnknown(id) (Job, error)
(*Manager).Close()
(*Manager).Done() <-chan struct{}
```

`Input` 包含 `ID/ConnectionID/Mode/Consent`，以及恰好一个对应模式的 `Transcribe/Synthesize/Clone` 请求。模式为 `transcribe/synthesize/clone`。内嵌请求 ID 空时使用任务 ID；非空时必须相同。ASR/TTS 的省略模型、粒度、输出格式会归一到 `speech` 当前默认值。克隆要求配音模型连接，且另有主体及本人/授权声明；声明不等于验证完成。

`Options` 注入 `Resolve(ctx,id)→Binding{ProviderID,ModelID,Secrets,Release}`、`Factory(secrets,files)`、`Prepare(ctx,editor.Asset,dir)` 和 `Probe(ctx,path)`。正式 Factory 默认创建 `speech.ElevenLabs`；没有明文密钥存储或外部命令行参数传密钥。绑定服务、模型和能力必须匹配请求；运行中的租约在观察者完成时释放。没有为等待本地保存长期固定密钥，`ResumeLocal` 不读取凭据或创建 Provider。

`Summary` 包含任务身份、原工程、服务/模型、模式、状态、消息、原源 ID/指纹/时长、分块计数、本地恢复/核对标记、时间、可选 `Audio/Voice` 和警告。`List` 只有这些轻量摘要，不包含识别词及请求全文。`Job` 详情再增加规范 `Input`、不含路径的 `PartState[]`、完整聚合 `Transcription` 和原始 `PartResults[]`。详情可能很大，应在需要时取得，不作为高频列表轮询。

## 去重、状态与取消

统一任务 ID 在当前数据目录内唯一。签名绑定原工程 ID、连接、规范请求，以及输入素材的 ID/类型/原指纹/时长；改名、路径重定位和其他工程修订不触发重生成。相同 ID/内容返回原记录；不同内容或损坏记录拒绝复用。损坏记录仍占住 ID，不会被当作未提交。任务元数据不包含密钥、远端预览 URL 或音频字节，原始识别词单独保存。

状态为 `queued/preparing/submitting/running/saving/succeeded/failed/unknown/cancelling/cancelled`。每次可能计费的调用，先原子保存提交意图，再调用 Provider；准备或意图保存失败时不发 POST。已受理请求中断、回执身份不符或结果不确定会进入 unknown，停止后续块，不自动重发。明确的已知拒绝为 failed；`AcknowledgeUnknown` 只记录用户已核对，不代表远端取消或允许同 ID 重交。

底层复用 `jobs.NewWithObserver` 的有界单工作队列；观察者把终态记录留在本包，短期队列清理超过100条缓存不会删除持久历史。本轮最多2000条持久任务，尚无历史归档界面。`Cancel` 停止本地排队/准备/等待，不声称取消远端收费处理；提交中取消可能保持 unknown。已经发布的成功或可恢复结果不被取消竞态覆盖。

`ResumeLocal` 仅保存已经取得的响应、重新合并原始块、重新探测已落盘 MP3，或恢复音色回执。它绝不重新 Prepare、Resolve、Factory 或 POST；只取得部分响应时可以保存现有证据，但不会继续未提交块，也不会把部分内容标成完整转写。内联内容尚未落盘便退出时，重启直接 unknown，不提供虚假恢复。

## 准备音轨和原始证据

`Preparation` 保留 `SourceAssetID/SourceFingerprint/SourceDurationUs`、实际 `AudioOffsetUs/CoverageEndUs`、`SampleRate/Channels/TimestampToleranceUs/Warnings`，以及 `Parts`。每块记录 `ID/Path/Fingerprint/OffsetUs/DurationUs/SampleCount/AudioDurationUs`。

调用方先创建受控任务父目录，传给 Prepare 的 `prepared` 目标必须尚不存在。准备器负责私有创建目标与失败清理。原源指纹必须一致；分块文件在受控目录内，经真实字节指纹检查，单块不超过64MiB。逻辑块区间从真实音轨起点连续到真实覆盖终点，不重叠，也不为视频无音轨的头尾填静音。末块物理采样时长可比逻辑长度多不超过63µs；`TimestampToleranceUs` 仅是源时间基准证据，不据此补词或伪造微秒精度。

每块使用稳定请求 ID，成功原始响应作为独立 JSON receipt 原子保存后才可提交下一块。全部原始响应与原始时间不变；合并时加真实源偏移，仅将末块物理量化尾裁回逻辑覆盖，保留警告。缺失的词/字符时间保持 nil，不按零或文本长度推造。跨块说话人标识加块前缀，不冒充已完成跨块说话人匹配。全源 ID/指纹/时长仍是原始素材，而不是提取 WAV 的身份。

只有所有原始 part receipts、聚合 JSON 与成功任务状态都可靠保存后，才清理固定的 `results/<jobID>/prepared`。删除目标从安全任务 ID 推导，不使用持久化的外部文件路径；原始用户素材、part receipts、聚合词与采样/指纹证据不会被删除。清理失败仅加 warning，真实成功不回退。Get、重开和本地重建不依赖已经清理的 WAV。

部分、失败、unknown 与尚待保存的任务暂时保留准备音轨供恢复/核查，尚无自动过期或磁盘配额。长素材在这些状态下可能占用较多磁盘，后续需另做可审阅的历史/证据清理策略；不能通过静默删除原素材或未知提交证据来腾空间。

## 配音和音色结果

配音仅保存当前受支持的 MP3，字节和身份/摘要回执在同一私有目录原子发布。之后调用注入的真实媒体 Probe，必须得到有效音频、正时长和匹配字节的 SHA-256 才标 succeeded；失败只允许本地恢复探测。输出 Asset ID 稳定为 `<jobID>-audio`，没有按文本长度猜时长或伪造对齐。

### 显式带时间配音（2026-10-25）

`Input.Synthesize.WithTiming=true` 才调用可选 `speech.TimedTTSProvider.SynthesizeWithTiming`。提交意图保存前同时核对接口和 `Capabilities.SpeechTiming`；不支持时拒绝，不回退普通端点。省略/false仍走原普通配音；同一任务ID的去重签名包含此选择，不能将旧普通请求改成一次新收费调用。

`Summary` 增加 `TimingRequested/TimingAvailable/TimingComplete/TimingReason`，以及独立的 `NormalizedTimingAvailable/NormalizedTimingComplete/NormalizedTimingReason`，JSON为camelCase。Available表示有对应原始对齐证据，即使时间缺失或结构无效；Complete仅表示该套字符与时间数组的结构完整。它不保证与请求文字匹配、落在音频真实范围内、字符有正时长或已可绑定字幕。应用必须另做这些核查，并由人明确选择原文或规范化文本；规范化文本不能悄悄替原文。

`Job.Timing` 保留原始 `TimedAudioResult`，分别返回原文/规范化对齐、nil时间及不能映射为数组时的 `RawAlignment/RawNormalizedAlignment`。合法音频遇到缺失、null或畸形时间仍保存音频并给警告，音频可成功，时间不得冒充可绑定。没有按字符比例分配时间或第二次POST。音频字节 `Data` 始终不进入JSON；详情返回独立证据副本。

`audio/receipt.json` 保存原操作ID、供应商、模型、音色、输出格式、音频字节数/SHA和两套时间证据。时间内容的SHA先随任务journal保存，再写入同一原子音频目录的receipt；读取时核对journal摘要、receipt摘要、原请求身份及音频实SHA。可检测音频/摘要/音色/模型/原始时间被单独篡改，也拒绝仅重算receipt时间摘要但与journal不符的内容。这是本地一致性校验，不是抵抗拥有全部本地文件改写权的密码学认证。旧普通配音receipts缺少新字段仍兼容。

恢复只读本机receipt和音频；缺少/损坏探测证据可重新做本地probe，不读取凭据、创建Provider或POST。响应仅在内存中但保存失败时可在当前进程恢复；退出前未落盘的响应不会被假装恢复，而是unknown占住ID。时间receipt上限16MiB；超过限制仍不会重POST。未知提交、取消期间的网络中断也保持unknown，重新打开和重复提交同ID均不重放。

`Options.ProbeAudio(ctx,path) (AudioProbe,error)` 是可选更完整的探测入口，优先于旧 `Probe`。`AudioProbe` 保留原 `Asset`、`SampleRate/Channels`、可空 `SampleCount` 和最多1MiB的 `RawMetadata` JSON；`Job.AudioProbe` 可查看本地探测证据。真实探测需由应用注入。旧Probe没有样本数，当前应用沿用它时SampleCount保持nil，绝不由MP3容器时长乘采样率推算解码样本；编码填充与解码样本的精确关系尚待真实计数源。回执本身的时间结构完整不等于采样级同步已验收。

克隆回执和用户声明都持久保存；Voice 始终是 `unverified`，供应商报告的 bool 另行保留，远端 preview URL 不保存或返回。创建 ID 不等于账户、声音或身份验证完成。

数据目录有进程锁。Close 等待队列结束最多3秒，但 Done 及目录锁一直保留到所有 worker 和观察者退出；忽略 context 的慢 Provider 结束前，第二实例不能接管。JSON、receipt 与结果使用私有文件/目录和原子替换；不承诺突然断电时所有文件系统目录项的持久性。

## 验证

fixture 覆盖真实文件读取、分块提交顺序、偏移/部分覆盖/63µs量化、缺失时间保留、并发去重、损坏 ID 占位、未知块停止后续、分阶段保存失败、本地恢复不读凭据/不重发、配音 Probe、克隆声明与未验证状态、取消竞态、慢 worker 锁生命周期、100条队列缓存外的持久历史，以及成功清理后仅靠原始 receipts 重建。

带时间任务另外以真实本地HTTP夹具验证显式端点恰好一次POST、普通模式不变、不支持预先拒绝、原文与规范化分开、缺失/null/畸形及文本不匹配原样保留、写盘失败同进程恢复、重启只读恢复、未知取消不重放、音频与时间回执篡改拒绝及旧receipt兼容。探测样本数不因夹具存在便宣称真实计数已实现。协议来源：[ElevenLabs带时间配音官方文档](https://elevenlabs.io/docs/api-reference/text-to-speech/convert-with-timestamps/)，2026-10-25核对两套alignment定义及显式 `mp3_44100_128`。

```sh
go test -race ./internal/speechjobs
go vet ./internal/speechjobs
```

这些证据不代表真实模型质量、账户权限、计费、Windows 系统凭据或完整长视频资源体验已验收。
