# 源音轨提取与分块

2026-10-25，关联 FW-R03/FW-R04/FW-R05/FW-R11/FW-R15。本模块提供批处理语音准备，不是原生实时预览，也不修改工程或提交转写。

## 接口与所有权

```go
Toolchain.PrepareSpeechAudio(ctx, asset, directory, SpeechAudioOptions) (SpeechAudioPreparation, error)
```

asset 必须是调用方已经授权、登记了真实时长及 SHA256 的本地音频/视频文件。路径必须绝对，只读取这一具体文件，不遍历输入目录；支持 sha256: 前缀或裸十六进制指纹，但始终计算真实内容核对。ffprobe 核查当前流与源时长，提取前后再完整计算指纹并检查路径解析、文件身份、尺寸和修改时间，源变化则不发布。

directory 的父目录须存在，directory 本身必须不存在。先在同父目录的私有暂存目录生成，全部核查后排他发布；不覆盖原目录、文件或符号链接。文件系统不支持排他目录重命名时，排他建立私有目标目录并逐个 O_EXCL 复制，失败/取消清理已知自有文件；发现并发替换/未知内容时保留对方结果并报告冲突，不递归删除未知目标。原生重命名是原子发布，复制 fallback 仅保证成功返回前不暴露可用清单及失败清理，不宣称原子可见。

成功返回后路径由调用方管理。已发布成功是提交点，之后的晚取消不删除成果。处理失败、取消或源指纹变化，不返回部分可上传清单。

## 覆盖与时间

Preparation 包含 SourceAssetID/SourceFingerprint/SourceDurationUs、AudioOffsetUs/CoverageEndUs、TimestampToleranceUs、Warnings、SampleRate/Channels、Parts。

- SourceDurationUs 始终是原资产真实时长，不用音轨较短的长度替代视频长度。
- Parts 仅覆盖实际音轨区间 AudioOffsetUs 到 CoverageEndUs，连续且不重叠；不为视频开头或尾部无音轨区域补长静音。较晚开始或较早结束都会在 Warnings 说明。
- Part.OffsetUs/DurationUs 是逻辑源区间；SampleCount 是真实 PCM 样本总数，AudioDurationUs 是 ffprobe 核查的物理 WAV 时长。采样量化末差不足 63µs，最后逻辑边界裁到源/已声明音轨末尾，不把物理尾差冒充源内容。
- 输入采用 copyts/start_at_zero，以素材格式起点为源时间零点，保留音轨相对起点。单次解码后的 ashowinfo 逐帧 PTS/nb_samples 与实际输出字节总数核对；源 time_base 的量化容差单独记录，不能把毫秒时间基宣称为微秒精度。
- 初版只提取第一个音轨。内部 PTS 缺口/重叠超过核定容差、时间基缺失或粗于10ms时明确失败；不压缩这些间隙，也不靠填静音假装连续。多音轨选择和内部断续区间拆分仍待后续支持。

调用方将分块内转写时间加 Part.OffsetUs，保留每块原始证据，并只在末尾采样量化差内裁到逻辑边界。缺音轨区间没有识别结果，不补词、不均匀分配时间。本包不保证固定边界不会切断词句；未用真实人声验证分块边界的识别质量，不能称无损断句。

## 资源限制

默认块长5分钟，可设置100ms–30分钟的整数毫秒；不足100ms的实际尾块并入前块。每块为单声道16kHz、16位小端 PCM WAV，严格小于64MiB。原始块数最多1024，超过后清理并报错。

默认原源时长上限6小时，可配置最高24小时；实际音轨不足100ms则拒绝，不补静音满足转写入口。源总时长限制也适用于音轨很短的长视频。6小时PCM上界约691MB，24小时约2.76GB；复制发布 fallback 可能临时占用两份，目录/磁盘不足会失败清理。

只运行一个 FFmpeg 主解码进程，顺序流式分块，不将整个长视频或整段PCM读入Go内存，也不逐块从头重新解码。Go复制缓冲64–128KiB，ffprobe JSON 4MiB上限，工具错误尾部32KiB，逐帧日志待解析行64KiB上限；输出采样总量受源时长及上述块数限制。额外两次完整源指纹读取会增加磁盘I/O。

参数数组直接启动进程，不经过shell。源输入协议仅 file/pipe，允许已列出的自包含容器，包括MOV/MP4、Matroska/WebM、WAV、MP3、ADTS AAC、FLAC、Ogg、AIFF、AVI、MPEG/MPEG-TS；不启用HLS、DASH、concat及网络读取。ADTS AAC 已由本机 `ffmpeg -h demuxer=aac` 核查并经真实合成样本提取。

Unix使用独立进程组，取消终止进程并等待管道收尾；Windows由CommandContext终止直接工具进程，尚未做Windows实机验收。FFmpeg解码/滤镜线程设为1；不宣称FFmpeg进程自身的内存有操作系统硬限额。

可选Progress回调报告真实已写SampleCount。解码结束前TotalSamples未知，保持0；不会用视频时长伪装已知音轨总量或展示假百分比。

## 验证

新测试只在显式配置 AIVIDCUT_FFMPEG/AIVIDCUT_FFPROBE 后执行，否则明确skip，不自动依赖其他产品安装目录。例如：

```sh
AIVIDCUT_FFMPEG=/absolute/ffmpeg AIVIDCUT_FFPROBE=/absolute/ffprobe go test ./internal/media -run '^TestSpeechAudio' -race -v
```

已用自建正弦/静音/纯色视频核查48kHz立体声转16kHz单声道、440/880Hz源区间、2秒视频与0.75秒短音轨、0.5秒晚起、AAC编码延迟/尾填充、Matroska毫秒时间基、ADTS AAC、采样尾差和短尾合并。实际103秒素材触发1024块上限后清理；另覆盖无音轨、内部PTS缺口、源改动、提取中/发布前/发布后取消、目标竞争和复制fallback清理。

测试不需要真实语音账户或收费调用，也不验证ASR准确率。开发工具仅通过显式路径调用，没有复制或打包第三方二进制。

## 与导出音轨的相对起点

导出原有音频链会在重建PTS时丢失素材内部的晚起间隙。现仅给该路径增加有界首帧定位：使用与实际渲染相同的输入源入点/时长，取实际16k时间基的首个解码帧；按速度换算这段真实延迟后加入已有时间线adelay，不预制整段源静音。同一源片段缓存定位结果，选中区间无音频样本时跳过该音轨。音量、混音、LUT、文字与普通零起点路径保留。

FFmpeg/ffprobe 6.1.1 本地fixture已经核查素材晚起0.5秒、源入点位于音轨前/内/后、半速/双速、导出选区及时间线位置。格式起点为3.000000秒的MOV样本，Prepare测得相对源偏移500000微秒，导出声/静音区间与同一基准一致。首帧定位不声明整段任意PTS的完整修复，不改变本模块对内部断续音轨的现有限制，也未扩展到原生实时预览。

## 官方依据

- [FFmpeg命令](https://ffmpeg.org/ffmpeg.html)：copyts/start_at_zero与进程参数。
- [FFmpeg音频滤镜](https://ffmpeg.org/ffmpeg-filters.html)：aresample、atrim、asettb、ashowinfo。
- [格式选项](https://ffmpeg.org/ffmpeg-formats.html)与[协议选项](https://ffmpeg.org/ffmpeg-protocols.html)：format_whitelist/protocol_whitelist。具体版本及输出证据仍以显式开发工具和测试为准。
