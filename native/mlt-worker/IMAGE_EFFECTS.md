# 原生视觉变换与LUT混合

本模块是常驻MLT进程中的受控 `aividcut.visual` transition，不启动FFmpeg子进程，也不预渲染整段素材。每次真实pull处理当前解码帧，新的运动内容始终进入算子图；缓存只复用图与有界工作面，不以旧画面替代新帧。

## 接口与编排

2026-10-25实施补充：部分强度仍表达原画面A与LUT画面B的线性权重，但使用blend normal/all_opacity=1-intensity，替代逐像素表达式求值。8位穷举和旧worker参考最多1级舍入；固定负载和限制见[LUT混合验收](../../docs/implementation/2026-10-25-LUT固定权重混合验收.md)。0/100%分支、原算法顺序和预算不变，历史表格保留原测量日期。

`register_image_effects(mlt_repository)` 注册服务；`visual_available()` 实查buffer/scale/setsar/format/rotate/transpose/hflip/vflip/buffersink，`visual_lut_available()` 再检查lut3d/split/blend。Go的 `compileVisualTransition` 只接受已校验的Project参数和LUT本地文件，不接受外部filtergraph。文字已有qtext几何/字号时使用 `compilePreparedVisualTransition` 原尺寸RGBA合成，不再施加一次Transform。

视频通过 `source_time_decoded_frame` 借用真实AVFrame，随后clone其引用，寿命不超MLT frame所有权；同一个libavfilter图按以下顺序处理：

1. 源display matrix方向修正；SAR与颜色标签来自实际AVFrame。
2. 先在源像素格式做fit/scale，再setsar=1与RGBA转换。
3. 用户指定的中心旋转，保持透明扩展画布。
4. LUT tetrahedral插值；强度中间值按 `A*(1-i)+B*i` 混合原图与完整LUT。
5. 绕原Project中心加XY平移，按默认导出overlay的2像素色度网格落位，再做straight-alpha source-over。

这是当前FFmpeg导出链的算子顺序。视频不再先整张转RGBA后缩放，也不经过MLT loader的隐式resize/normalizer往返。图片使用实际RGBA，文字使用已绘制的RGBA。未知格式、操作失败或拿不到受控视频帧明确失败；设置 `aividcut.visual_error/visual_reason`，主进程必须检查，防止MLT自动测试卡冒充成功。

MLT `push_frame/push_service` 共用服务栈，getter须按LIFO先取B frame再取transition。该处经过真实崩溃回归修正，不能误以为两者是独立栈。

## 有界画布与缓存

- 源实际尺寸上限64Mi像素；缩放目标维度最多16384、最多40Mi像素。既检查Project参数，也检查实际解码帧，不只依赖profile声明。
- 旋转先配置但不push帧以取得完整rotw/roth几何；实际只生成Project与XY位移可能看见的中心ROI。ROI保持完整画布的奇偶与中心，平移仍按完整画布计算，再补crop原点，避免改变旋转/位置。ROI最多24Mi像素，超限明确拒绝。
- 可见ROI前裁切仅去掉最终不会显示的像素；LUT是逐点处理，仍在旋转后执行。实际PNG/视频已与完整FFmpeg链比较，不能仅凭矩阵/XML判断等价。
- 全进程LRU最多8个图，并有合计1GiB的工作面估算预算；每个transition不常驻一个独立大图。素材路径、clip ID不是图key，尺寸/格式/SAR/颜色/方向/算子相同可跨不同文件和切段复用；LUT路径、大小、完整mtime tick纳入key。
- 释放旧图后才push新帧。`estimatedBytes` 是保守工作面估算，**不是实测RSS或整个进程内存硬配额**；解码器、MLT、Qt和SDK自身内存另有成本。源码的像素/尺寸边界避免用户调大scale带来无界分配，不代表所有机器都具备同样容量。
- mtime直接安全格式化原整数rep，未强制转换到纳秒int64；兼容Apple较宽rep，并避免Windows1601 epoch转换溢出。没有在项目代码引入MSVC不支持的显式`__int128`。

4K工程、2x缩放、30度旋转加0.5 LUT已真实拉到帧（当次约778ms）；极端16x缩放在分配前拒绝。资源有界与图像正确不等于该4K组合已达实时。

## 已有真实证据

2026-10-25，同机MLT7.42.0与FFmpeg9/libavfilter12.1.102；全部素材为自建非对称RGBA、透明梯度、H264 YUV视频和cube，无人物/账户/模型调用。

`TestNativeVisualRotationAndLUTAgainstFFmpeg` 比较实际native RGBA与 `media.PreviewFrame` 的FFmpeg PNG：强度0/.25/.5/1、90/30/-45度、位移与缩放。cube使用非默认DOMAIN与强RGB交叉项，另有opaque内部关键色块，能区分tetrahedral与trilinear。图像用例MAE约1.209–1.482、95分位通道差≤3，关键色块差≤3；差异含导出YUV420量化/色度采样，不承诺逐byte相同。

`TestNativeVisualVideoSourceScaleBeforeRGBAAgainstFFmpeg` 以真实H264 YUV源缩放、旋转和LUT比较，5组（含源metadata90度后叠加用户30度旋转与0.5 LUT）MAE约1.112–1.871、95分位差≤3。源方向先于fit的永久回归也已通过。证据PNG在显式设置的 `AIVIDCUT_VISUAL_EVIDENCE` 目录；无worker/开发FFmpeg时清楚Skip，不能算真实媒体通过。

同一39片段三层动态视频工程240帧的增量诊断保留在 `work/native-visual-check/`：

| 快照 | pull p50/p95 | 超33.33ms | worker采样峰RSS |
| --- | ---: | ---: | ---: |
| 初版整张RGBA/旧文字composite | 35.20/41.61ms | 239/240 | 210518016B |
| identity优化、仍旧视频路径 | 34.76/38.37ms | 239/240 | 206929920B |
| 正式raw同图与prepared文字 | 12.409/13.550ms | 0/240 | 171196416B |

最后一组3个图建立、720次命中；视觉filter累计326ms、合成140ms（含一次Seek+240次Next的全部层）。这是一次该机器的原生pull增量对照，不是整个WebView、设备音频、GPU或所有工程的性能认证；同时源时间/解码适配也在变化，不能把全部差值归给单一小优化。Root另用不同文件/不同运动三层工程复验，结论以独立报告为准。

## 私有诊断

`image_effects_diagnostics_json()` 返回 `visual` 数值对象，builds/hits/calls/fastPathCalls/filterNs/compositeNs为当前worker进程累计；residentGraphs/estimatedBytes为当前缓存状态。filter计时只包含图取得与处理，composite只包含本层alpha叠加，不重复计背景/源解码。该op只供本地诊断命令，不加入普通Frame、App、WebSocket或MCP，也不返回路径/认证信息。

## 依赖与剩余边界

没有新增MLT外部插件模块；在已有受控repository上注册项目服务，直接链接明确SDK的libavfilter。MLT affine/qtblend模块YAML虽标LGPLv2.1，但其变换/插值链不能仅凭相同属性名替代现导出顺序；当前选同源FFmpeg算子并做实际像素对照。

开发SDK启用了哪些GPL/GPL3/FFmpeg/Qt组件，须按[模块总说明](README.md)中的实际构建证据审阅；selected symlink不构成商业许可或可搬运发行背书。Windows原生、任意HDR/高位深/完整色彩管理、完整字体字形一致性、最大工程资源、所有采样相位及GPU仍是未完成边界，不能因这组RGBA8 SDR回归通过就关闭P0。

官方实现依据：[MLT XML producer](https://github.com/mltframework/mlt/blob/v7.42.0/src/modules/xml/producer_xml.c)、[loader normalization](https://github.com/mltframework/mlt/blob/v7.42.0/src/modules/core/producer_loader.c)、[FFmpeg filters](https://ffmpeg.org/ffmpeg-filters.html)、[FFmpeg autorotate编排](https://github.com/FFmpeg/FFmpeg/blob/n9.0.2/fftools/ffmpeg_filter.c)。运行验证优先于单独属性/源码阅读。
