# 原生拉帧诊断

本命令只读打开一个实际 `.aividcut` 工程，在明确配置的 MLT worker 上加载、定位并连续拉取 RGBA/PCM。它不启动 WebView 或音频设备，不修改工程、素材或撤销，不调用模型/CLI。

```sh
export AIVIDCUT_MLT_WORKER=/明确开发路径/aividcut-mlt-worker
export AIVIDCUT_MLT_REPOSITORY=/明确开发路径/mlt-selected-repository
export AIVIDCUT_MLT_PROFILES=/明确SDK路径/profiles
export AIVIDCUT_MLT_DATA=/明确SDK路径/data
go run ./cmd/preview-benchmark \
  -project /明确工程路径/example.aividcut \
  -out work/preview-benchmark/example.json \
  -frames 300 -from 0 -width 1280 -height 720 \
  -sample-interval 100ms -timeout 3m
```

实际帧序、PCM字节数和样本锚必须连续；否则以失败退出并尽量保留诊断原因，不把错误媒体计入成绩。`-frames` 最多100000，达到工程末端会停止。`-from` 单独测一次定位；可用 `-seek-frames 0,5000,5000,2,0` 在同一进程内按原顺序定位，最多128项，保留重复点以观察处理缓存命中。记录每次成功返回的实际帧/样本锚、RGBA/PCM CRC和耗时，并计算成功请求的p50/p95/max；失败停止并保留Failure。列表执行后回到from，再连续拉帧；单点SeekMs是这次回到from的调用，不能将其称为冷启动。

JSON 保留每帧 pullMs，另给最近秩 p50/p95、最大值、超出工程单帧时间预算的数量，加载与定位独立计时，并记录工程与worker SHA-256。actualImageCrc32/actualAudioCrc32消费实际RGBA/PCM字节，便于同输入版本对照；CRC不证明语义、色彩或音色质量。只计时 Session.Next，不包含 Canvas、传输、音频设备或整个客户端调度。支持新worker时，另读内部视觉算子计时/缓存计数；未知或旧worker明确说明诊断不可用，不编造零值。

同时记录`decodedNonzeroChannelSamples`（所有通道的非零float32采样数量）和`decodedPeakAmplitude`（实际绝对幅值峰）。NaN/Inf及不完整PCM明确失败，零值仍可作为合法静音；不能只看非空字节包就宣称已产生声音。读取/CRC验证开销仍由诊断Go进程承担，不含在单次Next计时中。

资源子报告现在为`schemaVersion=2`，复用客户端`internal/processstats`：macOS使用libproc，Windows使用Toolhelp/GetProcessTimes/GetProcessMemoryInfo。其它平台或未启用相应原生读取的构建会报告失败原因与null，不保留旧ps回退。原来的Go/worker峰值字段名保持，原始累计CPU改为明确的`cpuTimeNs`纳秒，不再输出旧`cpuTimeMs`；窗口CPU沿共享采集器计算，单CPU满载为100%，多核可超过100%。

只读取已确认的本进程及后代，记录出生身份、归属、RSS、CPU、平台私有指标与覆盖缺项，不接受任意PID输入或记录命令参数/环境/路径。worker绑定首次观测的出生身份，不能用同PID的新进程抬高旧worker峰值；未知指标为null。`confirmedTreeSampledPeakRssBytes`仅更新完整观测，原始`knownRssBytes`不是缺失进程的总量，RSS共享页也可能重复；不能把树合计当独占物理内存。

在启动worker前开始采集，启动/加载/定位/拉帧失败仍尽量保留报告；定时查询仍会遗漏短进程或瞬时峰值。收到Ctrl+C/终止信号或总预算到期时，通过ctx关闭worker和采样，失败报告保留原因。最多保留10000条样本、累计20000行原始进程明细，超过后继续更新观测峰值并累计`samplesDropped`；失败/部分观测另计，不以有界存储冒充完整时间序列。Windows本轮只有编译与交接，原生资源数据仍待实机。

工程多序列的报告包含当前sequenceId/sequenceName/sequences，片段和轨道只计当前序列，文件SHA仍覆盖完整工程。MLT_DATA与profiles/repository均显式传给worker，适用于已核验的相同随包SDK。Windows新交接包附两项诊断exe和[scripts/measure_windows_media.ps1](../../scripts/measure_windows_media.ps1)，先核验SDK，再生成编号视频/切片工程或读取明确工程，保存原始JSON/日志；不安装工具或联网。用法和实际证据见[本阶段规格](../../docs/specs/2026-10-27-跨平台拉帧资源诊断.md)。

`-picture-seeks`使定位列表只取画面，保留前后音频处理计数以核对未重放；随后仍以完整Seek回到from。音频准备的耗时不因此消失，不能用画面定位数据冒充声音启动延迟。

连续 pull 主要表示该固定输入的原生吞吐；没有按设备声音时钟播放，也没有 UI/模型/CLI 并发，不能据此承诺整机实时帧率。比较前后实现必须固定素材、工程、代理尺寸、SDK、机器与运行方式，并保留原始 JSON 和构建/输入指纹；应报告冷/热、缓存、资源采样和其他任务的影响。

工程与源文件不会被诊断报告覆盖，包括现存路径指向同一文件的情况。报告使用同目录临时文件写入并提交；已有明确 `-out` 诊断报告可替换。

`-window-clips 32`、`-window-frames 120`为开发用的收紧预算对照，0使用生产选择；显式值不会提高128硬上界。graph诊断记录实际替换、XML峰值及寿命标记。总工程较大时默认采用有界图，比较不同预算应使用同一工程、worker和程序，顺序运行以免负载相互干扰。

## Windows交接工具构建

在项目根目录使用go.mod指定的工具链，将下例`/new/package/tools`替换为新的交接目录；客户端/SDK仍沿scripts/build.sh构建装配。以下两个控制台程序和核验器不能加windowsgui链接标记，否则无法正常保留命令行输出。

```sh
mkdir -p /new/package/tools
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags='-s -w' -o /new/package/tools/preview-benchmark.exe ./cmd/preview-benchmark
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags='-s -w' -o /new/package/tools/preview-video-fixture.exe ./cmd/preview-video-fixture
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags='-s -w' -o /new/package/tools/media-runtime-manifest.exe ./cmd/media-runtime-manifest
cp scripts/measure_windows_media.ps1 /new/package/tools/
```

Windows收件机器直接使用编译好的工具，无需Go。解压后在PowerShell进入包根运行：

```powershell
& .\tools\measure_windows_media.ps1 -PackageRoot (Get-Location).Path -OutputDirectory 'C:\AIVidCut-check\first-run'
```

输出父目录需存在，first-run自身需不存在。完整五分钟负载可增加`-Seconds 300 -Clips 1000 -Width 640 -Height 360 -Frames 100000`；使用自己的工程传`-Project`。实际帧和资源JSON随日志一起保留，不能将跨编译成功作为该命令已在Windows执行的证据。
