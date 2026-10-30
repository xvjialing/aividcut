# Go原生媒体领域适配

本包把受控 `editor.Project` 快照编译为MLT图，管理一个常驻worker并顺序拉取RGBA/PCM。它不修改工程、不导出视频、不负责HTTP/WebSocket权限或前端播放时钟。应用必须绑定原engine/revision，处理工程切换和关闭，并在前端更换seek epoch、清空旧声音和实施消费背压。

## API与寿命

```go
Start(ctx, Config) (*Session, error)
Inspect(ctx, Config) (Capabilities, error)
ValidateProject(editor.Project, Capabilities) error
CheckProject(ctx, Config, editor.Project) error
(*Session).Load(ctx, editor.Project) (Info, error)
(*Session).Play(ctx) error
(*Session).Pause(ctx) error
(*Session).Seek(ctx, frame) (Frame, error)
(*Session).Preview(ctx, frame) (Frame, error)
(*Session).Next(ctx) (Frame, error)
(*Session).Capabilities() Capabilities
(*Session).Info() Info
(*Session).Close() error
(*Session).Done() <-chan struct{}
EncodeFrame(Frame) ([]byte, error)
WriteFrame(io.Writer, Frame) error
```

`Config` 明确传 `WorkerPath/MLTRepository/MLTProfilesPath/MLTDataPath`，最后一项用于显式MLT_DATA（包含loader.dict等运行数据）。不在本包自动寻找Homebrew、其它应用或CLI账号。默认输出代理上限1280×720，最大1920×1080；在原Project画幅合成后缩小，Project/导出坐标与采样率不变。`Timeout` 默认15秒，最高2分钟。

`OfflineRender`仅供`internal/exporter`的私有会话，要求worker声明同名能力。它保持Project原画幅，RGBA最多33177600字节；内部读包按离线预算验证，默认读包及EncodeFrame/WebView仍保留1080上界。图窗口必须保持相同输出模式；编码/封装/发布由exporter与media承担。详见[原生导出验收](../../docs/implementation/2026-10-25-原生合成与一致导出验收.md)。

`RenderQuality`可取空/full、half、quarter，默认全精度。先验证原工程及实际字形，再从克隆派生画幅、平移、字体和固定边距，低精度字形仍真实测量；图窗口保存同一布局比例。原FPS/源配方/声音不变，不生成源代理文件。离线会话拒绝低精度，exporter入口主动重置full。固定资源与近似差异见[可调精度验收](../../docs/implementation/2026-10-25-可调预览合成精度验收.md)。

Start的ctx控制整个进程寿命；不能直接拿响应完成即取消的HTTP request context充当常驻会话寿命。各Next/Seek操作另有自己的ctx。一个请求对应一个响应，无无界帧队列；取消/超时关闭该worker与管道，不允许在可能乱序的管道继续发命令。Close在pull持锁时先中断进程和管道，不等待其长超时。Unix使用进程组结束；Windows目前仅Go编译证据，原生进程树及插件仍需实机。

ValidateProject仅做静态核心/插件/编译范围检查。应用只有 `CheckProject`（短进程Start+Load+Close，不发帧）通过后才能把当前工程标为可原生预览；Load仍重新实际测量文字及建立图。字体bbox超框时明确拒绝，避免qtext自动缩字。插件列表不是该工程所有帧或媒体效果已通过的证明。

## 帧与传输

`Frame` 包含position、width/height、fpsNum/fpsDen、sampleRate/channels/samples、audioStartSample、end，以及JSON排除的RGBA/PCM字节。packed RGBA8 stride=width×4；PCM固定48kHz stereo、小端交错float32，Samples为每声道帧数，AudioStartSample是MLT自身输出时钟anchor，不是源素材实测样本数。输出样本数不按30fps常数或asset.duration推造。

Session复用RGBA/PCM缓冲，字节只保证有效至下一次Load/Next/Seek/Close。顺序写完WebSocket再Next无需复制；调用者要长期保存则必须复制。`WriteFrame`可直接写WebSocket二进制writer，避免EncodeFrame再次分配整份RGBA包。格式为16字节 `AIV1 + 三个uint32BE长度`，再JSON/RGBA/PCM；具体上限见[worker模块说明](../../native/mlt-worker/README.md)。

Seek返回目标帧但不消耗播放游标。它用于画面，应用应丢其PCM；Play+Next从目标帧开始。End=true仍带最后一帧实际PCM，前端消费后再停。Go本包只顺序pull；2–3帧ACK窗口和AudioWorklet消费时钟由应用/前端实现，不能将此包存在当作整条UI播放体验已验收。

## 工程与源时间

仅编译本包固定的服务/属性，不接受用户XML或任意filtergraph。资源必须是当前工程可用本地普通文件；未知格式/插件或超出已验证范围的语义会明确拒绝。现支持原处理配方的源PTS/变速、用户旋转、部分LUT强度与精确AudioRange偏移，具体限制见阶段记录。本包不替代上层素材指纹接纳或工程revision作用域校验。

MLT avformat实际会将晚起音轨压到零。worker已增加FFmpeg9库首有效音频帧PTS探测，按format.start_time还原源时间原点，再在混音前的单路滤镜恢复正确前缀与精确尾；不为长静音前缀预分配整段数据。SourceIn=0/.4/.8秒、晚起.5秒、format起点3秒、实际波形相位、尾后静音及seek预览后播放已通过自建MOV回归。AudioRange采用核心 `ResolveAudioRange`，samples只使用Asset实测证据，不由容器时长反推count。

文字是实验qtext renderer：argument纯字形分支，html/resource清空；实际Qt字形边界核查阻止自动缩字。短文字alpha和字面标签/换行已实测；完整跨平台字体/色彩、任意编码器与源采样相位、预览导出一致性、GPU、长素材资源及商业分发仍未完成。

## 共享音轨与画面定位

视频派生段现以原输出窗in/out引用共享producer，素材、输入、速度、帧率及绝对放置相位组成身份。MLT重叠克隆另由原生8状态池复用；不同放置相位独立，借出帧在淘汰后仍有效。实际B帧/长GOP、CFR/VFR与720p数据见[视频验收](../../docs/implementation/2026-10-25-视频派生源与长GOP验收.md)。

大工程现采用不可变快照、活动区间树和有界图：超过128总片段时自动启用，窗口优先32片段，按同时有效数量提升至最多128，最长约8秒。原绝对帧与源配方不变，窗口替换保持下一拉取位置/playing及源缓存，新Load清空；旧worker保留128总片段限制。完整索引100000上界不是已实测容量，固定千/万片段及资源证据见[大工程验收](../../docs/implementation/2026-10-25-有界原生图与大工程验收.md)。WindowClipLimit/WindowFrames仅供开发收紧对照。

worker内自动resample服务通过现有swresample工厂创建，避免旧实现遗漏父service释放；缺少该后端不声明windowedGraph。Diagnostics的graph字段包含实际替换、XML峰值、公开可达对象的寿命标记，不进入媒体帧或MCP，也不替代RSS/整机测量。

受控音轨使用aividcut.audio_mix，要求同一48k双声道float32样本窗，跳过时间线空白，不保留跨帧混音队列。原处理范围、素材身份/指纹、速度和放置原点匹配的片段共享最多8个源处理状态，每个PCM队列上限192000采样帧；失活释放，Load清空。片段音量不改写共享PCM。

Preview仅返回真实画面、pictureOnly=true、Samples=0，后续Next才准备声音；Seek仍返回完整PCM。应用准备/暂停/定位采用Preview，旧后端保持完整真实Seek。缓存/重放及定位的实际数据见[音轨资源验收](../../docs/implementation/2026-10-25-音轨资源与纯画面定位验收.md)。

## 开发构建与验证

2026-10-27：所有编译producer通过boundedProducer显式声明in/out/length，基础画布、源、音轨载体和文字不会依赖MLT默认15000帧。原静态图与电影尾帧确有边界失败，修复后长静态图/对应源时间/整片千切片通过；[实际格式矩阵](../../docs/research/媒体格式兼容矩阵.md)按文件组合记载，不扩大到任意HDR/设备。

显式选择SDK后运行 `make native-worker`，输出 `work/bin/aividcut-mlt-worker`、`work/mlt-selected-repository`、`work/mlt-profiles`，仅全部成功才写 `.aividcut-native-worker-built`。SDK路径/工具变量、隔离构建脚本检查、真实MLT测试命令、7模块许可边界与单次资源数据都记录在[worker模块说明](../../native/mlt-worker/README.md)。脚本不安装全局依赖、不复制第三方binary，也不把开发SDK当发行包。

本轮真实selected Native `-race`套件已通过；未显式设置worker时真实用例清楚Skip。全库与selected那次17.795s/78ms、约42/21MiB的观测只有factory→inspect→close，存在顺序/缓存影响，不能代替播放负载或商业性能验收。P0尚未全部完成。

源代理通过内部Config.VisualSources映射画面路径、派生指纹和原格式OriginUs；源音频路径与工程不改。Session先验证原工程，旧worker能力缺失拒绝，图窗口持续携带映射。离线拒绝代理，exporter.Native主动清空。实际CFR/VFR/晚起/旋转/SAR、声音与资源证据见[源代理验收](../../docs/implementation/2026-10-26-源视频代理预览验收.md)。

2026-10-27新增显式工程字体：Go核对文件SHA/元信息/字形，worker的fontFiles能力、应用字体加载、实际字体表/字形运行核对与测量共用同一选择，qtext/原生导出沿用；未指定字体仍沿原默认。32份/128MiB与8个匹配实例缓存有界，复杂语言/Windows和默认跨机一致性仍待验收，见[字体验收](../../docs/implementation/2026-10-27-工程字体资源验收.md)。
