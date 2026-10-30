# 常驻 MLT 媒体进程

旋转、任意LUT强度、同源视频视觉链与资源限额的新增证据见 [原生视觉模块](IMAGE_EFFECTS.md)；具体实现状态以该模块的真实回归和剩余边界为准。

2026-10-25 开发中的P0桥接。此进程从MLT直接拉取图像和音频，不启动melt/SDL窗口；显示层仍是WebView的Canvas与AudioWorklet，不能称作GPU或原生view已通过。自建素材的常驻拉帧、混音、像素及源时间回归已通过，范围见下文；编译/插件枚举不代替播放验收，P0仍未全部完成。

## 构建

```sh
AIVIDCUT_MLT_SDK=/absolute/sdk/mlt \
AIVIDCUT_FFMPEG_SDK=/absolute/sdk/ffmpeg \
AIVIDCUT_QT_SDK=/absolute/sdk/qt \
make native-worker
```

脚本要求C++17、CMake 3.24+、pkg-config、MLT7开发包及兼容的FFmpeg libavformat/libavcodec/libavutil开发包。本机真实构建使用MLT7.42.0、Qt6.12.0、FFmpeg9的libavformat/libavcodec63.1.102、libavutil61.1.102。脚本采用 `cmake --fresh`，避免切换SDK后继续使用旧缓存；源工程手动CMake最低版本仍为3.20。

`AIVIDCUT_MLT_SDK` 指向含 `lib/pkgconfig` 的明确SDK。也可改用显式 `PKG_CONFIG_PATH`，FFmpeg/Qt已在该工具链搜索路径内时不必额外设置对应SDK变量。可选 `AIVIDCUT_CMAKE`、`AIVIDCUT_PKG_CONFIG` 用于明确工具可执行路径；Qt可沿用 `CMAKE_PREFIX_PATH`。脚本不安装系统依赖，不自动调用brew，不猜Homebrew或竞品位置，不执行SDK提供的shell配置。

构建入口为 `scripts/build_native_worker.sh`，只编译本项目worker。成功后固定输出：

| 路径 | 内容 |
| --- | --- |
| `work/bin/aividcut-mlt-worker` | 本项目Release worker；Windows工具链可能为`.exe` |
| `work/mlt-selected-repository/` | 由明确SDK `moduledir` 派生的7个模块symlink：core/avformat/qt6/xml/plus/normalize/resample |
| `work/mlt-profiles` | 指向该SDK `mltdatadir/profiles` 的symlink |
| `work/bin/.aividcut-native-worker-built` | 全部步骤成功后才写的开发完成标记 |

构建失败不写成功标记。选定repository或profiles存在未知内容、实体模块文件/目录时拒绝覆盖；SDK缺模块时保留旧repository，标记无效。`scripts/prepare_native_repository.sh` 可单独重建开发链接，但它会使整体成功标记失效，之后应重新运行 `make native-worker`。链接替换仅在 `work/` 下进行，不复制第三方binary、不改SDK，不把这份开发仓库当作商业发行许可清单。

开发启动脚本只有在marker、worker、repository、profiles全都存在时才可填补空的开发环境值；用户显式设置优先。运行时仍使用明确的 `AIVIDCUT_MLT_WORKER`、`AIVIDCUT_MLT_REPOSITORY`、`AIVIDCUT_MLT_PROFILES`，没有把SDK搜索逻辑塞入 `playback.Config`。正式发行仍需独立提供worker、MLT库、数据和插件路径。

`internal/playback.Config` 只接收显式WorkerPath/MLTRepository/MLTProfilesPath。worker子进程不继承任意MLT/Qt/账户环境；固定Qt offscreen，不创建额外可见窗口。上游qt模块在需要时可自行创建QApplication。没有外部用户XML入口；只有Go受控Project编译器能构建固定producer/filter/transition图和当前工程已登记的本地素材路径。

## 控制和传输

stdin是最多4MiB的内部JSON lines，平坦字段，只接受inspect/measure_text/load/load_window/play/pause/seek/preview/next/diagnostics/close。Go顺序互斥发起，一次请求恰好一个响应；不存在无限帧队列。所有MLT对象只由worker主线程操作。暂停与seek由调用者串行控制，阻塞解码遇到取消/超时时关闭整个worker，不能复用可能乱序的管道。

stdout每包为16字节头：`AIV1`、metadata字节数(uint32BE)、RGBA字节数(uint32BE)、PCM字节数(uint32BE)，再依次JSON/RGBA/PCM。metadata上限128KiB、PCM上限1MiB；默认预览RGBA上限1920×1080×4，内部离线模式最多3840×2160×4。MLT与插件普通stdout重定向stderr，协议使用专用wire；Go只保留32KiB stderr尾，避免插件日志污染或无限增长。

RGBA使用MLT `mlt_image_rgba`，packed stride=width×4。先按原Project画幅合成，再缩小预览，预览尺寸不改变Project/导出坐标。PCM请求 `mlt_audio_f32le`，48kHz stereo交错小端float32；Samples为MLT实际返回的每声道帧数。AudioStartSample来自MLT自身 `mlt_audio_calculate_samples_to_position`，不以素材容器duration推算源实测样本。视频PTS为position×fpsDen/fpsNum。最后一帧End=true，客户端必须消耗该帧PCM后停。

Seek返回目标帧和真实PCM，不消耗播放游标；Preview仅返回画面，Samples=0、pictureOnly=true，避免定位重放未消费声音。应用准备/暂停/定位采用Preview，Play+Next从目标帧准备完整PCM；Next推进一帧。暂停/定位更换epoch并清空旧音频，前端确认真实消费后才释放2–3帧背压窗口。

## 当前边界

视频状态按原配方与绝对放置相位共享，最多8个；帧通过独立AVFrame引用和共享对象持有回调寿命，淘汰先释放解码资源，RGBA后备路径保留源时间基。相同原输出窗的MLT克隆可复用，不同放置独立，Load清空。当前数据见[视频长GOP验收](../../docs/implementation/2026-10-25-视频派生源与长GOP验收.md)。

- 单图最多128个相交片段，生产优先32，较大工程通过保留全局时钟/源缓存的load_window替换；24小时、最多4K原画素数、2–120fps。输出代理默认1280×720最大1920×1080。总索引与同时解码分别约束，见[大工程验收](../../docs/implementation/2026-10-25-有界原生图与大工程验收.md)；完整硬件/自然素材容量仍须实测。
- 图使用colour基底、受控source_time/source_audio源、playlist和自有aividcut.visual/audio_mix。每份源先实际验证编码器读取，不能回退测试卡冒充成功。qtext支持与真实字体测量关联；旋转、部分LUT、源PTS及0.125–8倍速已有真实回归，超范围仍拒绝。
- `SourcePhase`保留原完整处理输入，派生段仅裁输出窗；`ResolveSourceAudioPhase`沿用真实源半开端点和有理偏移。音轨先完整重采样/atempo再裁输出，5777/48000音尾、多轨混音、非帧入点、负子帧偏移、剪切与变速都有真实回归。MP3设置包时间基，AAC保留开头priming包；不按帧补音尾。
- 插件存在只证明可建图。已通过的普通文字alpha、LUT像素和源时间夹具不能扩张成完整色彩/字体、变速、导出一致性或商业可用性已验收。
- qtext只设置纯文字argument，明确清空html/resource；同版上游实际代码在此分支以QPainterPath绘制字形，不自动识别HTML。`<b>字</b>`等保持字面，换行保留。qtext会在路径超框时自动缩字，因此Load在建图前用相同Qt6字体/字重/像素大小、实际QFontMetrics测量每段文本，超框拒绝；不是按rune数量估字宽。`CheckProject(ctx,Config,Project)`执行短worker测量/建图后Close，应用只有CheckProject通过才能报告工程可原生预览；Start.Load仍二次核查。缺Qt6测量SDK则Text.Supported=false。当前文字仍是实验renderer，不能作为原生与导出全部字形一致性的证明。
- 视频解码器惰开、失活释放，音频最多8个共享处理状态，每个PCM队列上限192000采样帧；视觉缓存另有工作面预算。长素材视频、整机资源、GPU和多解码器仍需验收。默认全库含无关大型模块，SDK下载量不代表发行体积。音轨实测见[阶段记录](../../docs/implementation/2026-10-25-音轨资源与纯画面定位验收.md)。
- Go复用RGBA/PCM缓冲，字节有效至下一次Load/Next/Seek/Preview/Close。根应用顺序写WebSocket后再pull可复用，长期缓存者需复制。`playback.WriteFrame`可直接写二进制writer，避免再次复制整RGBA。
- Unix取消按进程组结束。worker本身不启动外部进程；Windows测试二进制编译不能代替Windows MLT/插件、Job Object及真实资源验收。

## 晚起音轨的实际修复

真实夹具发现MLT avformat将首音轨晚起0.5秒的MOV声音提前到源0播放，不能直接接受其压零后的声音时钟。`audio_probe.cpp` 现通过同机FFmpeg9库打开已登记本地文件，选择首音轨，读取首个有效解码音频帧PTS/时基，处理有效skip_samples并减去format.start_time原点；不拿container.duration或普通声明时长推算起点。

自有逐路滤镜在首有效音频前返回静音而不消费后面的声音，跨首有效帧仅保留有界PCM余量；不会为长前缀预分配一整段静音或提前缓存整段声音。seek不连续时清空余量，源末端仍在混音前单独门控。正常0起点语义保留，不能影响另一条音轨。

自建MOV（PCM音轨晚起0.5秒）已验证SourceIn=0/.4/.8秒：实际声源分别保留.5/.1/0秒前缀，660Hz波形源相位正确、实际音轨尾后静音，seek预览再播放无重复/错位。第二份format.start_time=3秒的相同MOV也通过。此证据只覆盖所测格式与源时基，不冒充VFR、所有压缩编码器、任意源采样率相位或所有音画起点组合已完成。

## 真实验证与资源观测

2026-10-25，selected开发repository完整 `go test -race ./internal/playback -run '^TestNative' -count=1 -v` 通过（当次3.722秒），使用自建PNG、WAV、MOV和明确配置的FFmpeg。验证包含：

- 同一常驻进程load/play/pause/seek/End/close；两层图像层级、fit黑边、位移/缩放按原Project坐标的像素。
- 29.97fps的实际帧Samples与AudioStartSample连续，seek不消耗播放游标，末帧PCM保留。
- 5777个真实48kHz源样本半开音尾；第二音轨继续混音，未在总混音后剪断所有声音。
- 全强度3D LUT实际改变红/绿像素；文字alpha实际改变画面，未仅验证元数据。
- `<b>字</b>`保持字面、换行保留；同为30字符的窄`i`可容纳、宽`W`实际超框被拒，未按rune数量估字宽。
- 上述晚起MOV、裁入声源、波形相位、format非零原点与seek恢复。

协议夹具另验证尺寸/PCM长度与非有限值、阻塞pull取消、Close不等待长pull预算、stderr有界。没有显式worker时，真实MLT测试明确Skip，不算播放通过。Windows Go测试二进制交叉编译不等于原生MLT实机通过。

补齐开发脚本并等价整理JSON parse括号/可读性后，`make native-worker`实际构建成功且无原逻辑括号警告；使用新 `work/bin` worker的完整 `go test -race ./internal/playback -count=1` 再次通过（4.066秒），四项隔离构建脚本检查通过。

同日对worker仅factory初始化→inspect→close作过一次有界对照，30秒上限，单独查询该worker PID的RSS；结果在 `work/mlt-runtime-evidence/full.json` 与 `selected.json`：

| SDK插件仓库 | 当次总耗时 | 采样峰值RSS | producer/filter枚举 |
| --- | ---: | ---: | ---: |
| 全库 | 17795ms | 44007424 bytes（约42MiB） | 31 / 439 |
| 7模块selected | 78ms | 22216704 bytes（约21MiB） | 26 / 354 |

两者均实际返回AIV1、MLT7.42.0及available=true，未超时。full先、selected后，缓存/运行顺序会影响结果；RSS为约30ms间隔加查询成本的采样峰值。这不是受控性能基准，也不是播放负载、应用总内存或产品包体积；不能据此宣传固定加速倍数。此前全库受10秒调用预算限制多次未能返回，超过默认15秒预算的本次full观测也是开发明确选择selected的原因。

## 许可与发行实际证据

2026-10-25读取本机Homebrew下载缓存中7.42.0 arm64_tahoe瓶内 `.brew/mlt.rb`、COPYING和插件YAML。配方标记LGPL-2.1-only，但实际构建开关为 `GPL=ON`、`GPL3=ON`、`MOD_QT6=ON`、`MOD_MOVIT=OFF`、`RELOCATABLE=OFF`。qtext/qtblend插件YAML标LGPLv2.1。当前开发构建包含可选GPL/GPL3模块，没有Movit GPU，并不是可直接复制分发的可搬运工具链。

商业发行需要依据实际启用和打包的每个插件、FFmpeg/Qt等依赖重新确定构建和许可清单；此处不以MLT总标签推导整包的许可结论。本项目不将Homebrew/竞品二进制复制进发行目录。

官方依据：[MLT框架](https://www.mltframework.org/docs/framework/)、[qtext](https://www.mltframework.org/plugins/FilterQtext/)、[composite](https://www.mltframework.org/plugins/TransitionComposite/)、[LUT滤镜](https://www.mltframework.org/plugins/FilterAvfilter-lut3d/)、[Qt模块实际代码](https://github.com/mltframework/mlt/blob/v7.42.0/src/modules/qt/common.cpp)。

## 验证命令

```sh
go test -race ./internal/playback -v
AIVIDCUT_MLT_WORKER="$PWD/work/bin/aividcut-mlt-worker" \
AIVIDCUT_MLT_REPOSITORY="$PWD/work/mlt-selected-repository" \
AIVIDCUT_MLT_PROFILES="$PWD/work/mlt-profiles" \
AIVIDCUT_FFMPEG=/absolute/sdk/ffmpeg/bin/ffmpeg \
go test -race ./internal/playback -run '^TestNative' -count=1 -v
python3 scripts/test_native_worker_build.py
```

构建脚本的隔离夹具使用临时假SDK/假CMake与真实pkg-config，不编译或安装第三方工具；检查带空格路径、切换SDK、缺模块保留旧repository、未知内容保护及失败不留成功marker。媒体夹具无真实账号、收费模型或第三方人物素材。

2026-10-26：source_time支持受控source_origin_us覆盖，seek与锚点使用同一原格式时钟，配方键包含起点及是否覆盖；sourceProxies能力显式声明，旧worker不假装支持。原音轨/采样处理不改。已重建普通/ASan/UBSan worker并完成固定实际对照，未启用LSAN；详见[代理验收](../../docs/implementation/2026-10-26-源视频代理预览验收.md)。

## 2026-10-27 逐路音频淡化

新增audioFades能力。source_audio在本路新输出缓冲中按全局真实采样位置施加线性权重，再经过固定音量和原混音；共享源PCM不变。包络使用虚拟采样起止位置，裁窗不重新淡入，Seek/图窗口沿同一映射，旧worker明确拒绝有声淡化。没有新解码依赖或持久PCM缓存，真实权重/同源独立轨/分割/Seek及窗口已验，详见[淡化验收](../../docs/implementation/2026-10-27-片段音频淡入淡出验收.md)。

## 2026-10-27 发行输入核对

本机worker直接链接包外MLT/FFmpeg/Qt，开发repository/profiles仍是外部SDK链接。`make release-audit`按显式配置记录实际二进制、普通导入和路径，`make release-check`保留未通过的发行门槛；不会复制/安装SDK或替换编码器。当前FFmpeg的GPL配置和MLT模块许可须按实际组合复核，不能用顶层COPYING或进程隔离代替分发判断。真实报告与来源见[发行检查验收](../../docs/implementation/2026-10-27-发行产物与媒体依赖检查验收.md)。
