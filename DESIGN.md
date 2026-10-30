---
version: alpha
name: AIVidCut
description: 安静、精确的 AI 剪辑工作台，以素材、预览、时间线和选区上下文组织创作。
colors:
  primary: '#aec9ff'
  brand: '#aec9ff'
  onBrand: '#14233c'
  viewerText: '#a4b0bf'
  onPrimary: '#14233c'
  background: '#11151a'
  surface: '#191e25'
  raised: '#232a34'
  viewer: '#0a0c0f'
  text: '#e8edf3'
  muted: '#a4b0bf'
  faint: '#8996a6'
  border: '#343e4c'
  controlBorder: '#607087'
  success: '#9bd2b4'
  warning: '#e4c58b'
  danger: '#f1a7ac'
  selection: '#29436d'
  videoTrack: '#36547c'
  audioTrack: '#315d56'
  textTrack: '#6b543c'
  captionTrack: '#5b496e'
typography:
  sans:
    fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif'
    fontSize: '13px'
  mono:
    fontFamily: '"SFMono-Regular", Consolas, "Liberation Mono", monospace'
rounded:
  sm: '4px'
  DEFAULT: '6px'
  lg: '10px'
spacing:
  xs: '4px'
  sm: '8px'
  md: '12px'
  lg: '16px'
  xl: '24px'
components:
  button-primary:
    backgroundColor: '{colors.primary}'
    textColor: '{colors.onPrimary}'
    typography: '{typography.sans}'
    rounded: '{rounded.DEFAULT}'
    height: '30px'
  button-outline:
    backgroundColor: '{colors.raised}'
    textColor: '{colors.text}'
    rounded: '{rounded.DEFAULT}'
  button-ghost:
    textColor: '{colors.muted}'
    rounded: '{rounded.DEFAULT}'
  workspace:
    backgroundColor: '{colors.background}'
    textColor: '{colors.text}'
  panel:
    backgroundColor: '{colors.surface}'
    textColor: '{colors.text}'
  viewer:
    backgroundColor: '{colors.viewer}'
    textColor: '{colors.viewerText}'
  brand-mark:
    backgroundColor: '{colors.brand}'
    textColor: '{colors.onBrand}'
  secondary-label:
    backgroundColor: '{colors.raised}'
    textColor: '{colors.faint}'
  separator:
    backgroundColor: '{colors.border}'
  control-stroke:
    backgroundColor: '{colors.controlBorder}'
  status-success:
    backgroundColor: '{colors.success}'
  status-warning:
    backgroundColor: '{colors.warning}'
  status-error:
    backgroundColor: '{colors.surface}'
    textColor: '{colors.danger}'
  selection:
    backgroundColor: '{colors.selection}'
    textColor: '{colors.text}'
  track-video:
    backgroundColor: '{colors.videoTrack}'
    textColor: '{colors.text}'
  track-audio:
    backgroundColor: '{colors.audioTrack}'
    textColor: '{colors.text}'
  track-text:
    backgroundColor: '{colors.textTrack}'
    textColor: '{colors.text}'
  track-caption:
    backgroundColor: '{colors.captionTrack}'
    textColor: '{colors.text}'
  input:
    backgroundColor: '{colors.background}'
    textColor: '{colors.text}'
    height: '32px'
    rounded: '{rounded.DEFAULT}'
  dialog:
    backgroundColor: '{colors.surface}'
    textColor: '{colors.text}'
    rounded: '{rounded.lg}'

---

# AIVidCut 设计上下文

状态：2026-10-25 首轮实施基线。用户已授权按规划开始实现，本轮将原设计种子落实为可运行工作区；具体值对应当前源码，尚未宣称 Windows/macOS 全部实机视觉或无障碍验收完成。

## Overview

### 创作方向

像一张安静而精确的剪辑台：画面与时间线居中，工具具有清楚的层级。素材与工作区没有装饰性渐变或假缩略图，生成和自动化能力围绕当前选区进入。

- **用户与任务**：已有剪辑基础的人，导入本机素材、粗剪、添加文字、核对声音与色彩并导出。
- **界面类型**：桌面生产工具，偏高密度，不采用营销落地页或大卡片后台布局。
- **平台**：Wails 桌面为产品入口，开发 HTTP 适配仅用于联调同一 Go 服务；支持范围以阶段实测为准。
- **语言**：首轮界面为简体中文；输入法组合态不触发剪辑和发送快捷键，不据此推断地区法规。
- **识别细节**：预览下方“当前选区”上下文条，将时间线、属性与编辑动作关联；颜色和标记表达真实类型和选择关系。
- **克制**：中性石墨表面，低饱和蓝强调；预览区域安静，右侧 AI 面板可收起。
- **业务来源**：[需求](docs/requirements/背景与需求规格.md)、[工作流](docs/product/操作流程与交互原则.md)、[实施契约](docs/specs/2026-10-24-P0-P1实施契约.md)、[生成连接与任务接口](docs/specs/2026-10-25-生成连接与任务接口.md)、[本地 Agent 应用接口](docs/specs/2026-10-25-本地Agent应用接口.md)、[语音应用接口](docs/specs/2026-10-25-语音应用接口.md)、[工作流与 MCP 接口](docs/specs/2026-10-25-工作流与MCP应用接口.md)、[CLI 执行与候选替换](docs/specs/2026-10-25-CLI执行与候选替换接口.md)、[配音时间与阅读字幕](docs/specs/2026-10-25-配音时间与阅读字幕接口.md)。

### Token 所有权

采用运行时为权威、文档同步的 Model B。唯一颜色、字体、圆角、间距和反馈时长源为 `frontend/src/styles/tokens.css`，`frontend/src/styles/app.css` 通过语义变量消费；本文件镜像已实施的数值与意图。组件不各自维护另一套主题或 Tailwind 配置。

本轮以 CSS 变量与共享组件实现 shadcn 式的可组合界面，使用 Radix Dialog/Tooltip 处理可访问行为，尚未引入 Tailwind 构建依赖。未来引入工具时需适配同一变量源。

| 文档角色 | 运行时变量/实现 | 消费者 |
| --- | --- | --- |
| primary/onPrimary | `--color-accent` / `--color-on-accent` | 主要行动、焦点、播放头 |
| background/surface/raised | `--color-background` / `--color-surface` / `--color-surface-raised` | 工作区、面板、控件层级 |
| text/muted/faint | `--color-text` / `--color-muted` / `--color-faint` | 文字层级与辅助说明 |
| workspace viewer | `--color-workspace-viewer` / `--color-workspace-viewer-text` | 跟随主题的预览空态、周边和提示 |
| brand/onBrand/viewerText | `--color-brand` / `--color-on-brand` / `--color-viewer-text` | 固定品牌标记与黑色媒体画布上的提示 |
| border/controlBorder | `--color-border` / `--color-control-border` | 装饰分隔与需要辨识的控件边界 |
| video/audio/text/captionTrack | `--color-track-*` | Timeline 片段，另有图标和文字类型 |
| typography | `--font-sans` / `--font-mono` / `--text-body` | 控件、内容与等宽时间码 |
| rounded/spacing | `--radius-*` / `--space-*` | 共享控件与布局；细部 recipe 在 app.css 维护 |

## Colors

具体颜色见 frontmatter。`viewer` 保持接近黑色的安静画面背景，`surface`/`raised` 形成工具层级。强调色仅用于安全主操作、焦点、选中及播放头，警告和错误有文字，不单靠颜色区分。

正文与弱说明均以文字对比为约束；`faint` 已避开早期灰度过低的值。主要按钮的 onPrimary/primary 理论对比约 9.4:1，弱说明在 raised 表面的理论对比约 4.8:1，控件边界在 surface 上约 3.3:1；这些计算不替代完整渲染测试。

四种片段有对应背景与图标。锁轨以锁图标和纹理表达，不整体降低可读内容的不透明度。禁用操作区别于可点击状态。高对比模式保留系统颜色、滚动条和焦点。

### 客户端外观（2026-10-30）

设置按用户提供的参考图改为同窗口的完整页面，通过 macOS 系统应用名菜单“设置…”进入；工作台不再显示设置按钮。左侧是“返回应用”、“偏好 / 外观”和“连接 / MCP”导航，右侧以“主题”分组和“明暗模式”说明配合三张界面缩略图，提供浅色模式、深色模式、跟随系统。没有已实现内容的分类不展示。MCP 页面复用 MCPPanel 的状态、权限、配置查看/复制、请求核对与记录，不创建第二个连接状态所有者。

设置沿用系统字体与语义颜色，左侧240px（窄桌面200px）、正文分组最大900px。设置有独立滚动区，1100px以下将说明与缩略图上下排列。浅/深缩略图用显式 color-scheme 的 Canvas/CanvasText 系统颜色绘制，不创建第二个应用主题；选中边界、勾选标记和键盘焦点沿既有 accent。原生单选与整卡 label 保留方向键/空格行为。外观即时应用、自动保存，返回应用不回滚。

默认深色数值继续由 frontmatter 镜像；浅色覆盖由 `tokens.css` 的 `:root[data-theme='light']` 唯一维护。根节点的 `data-theme` 只表示实际浅/深色，用户的 system 偏好由 `lib/appearance.ts` 保存和解析。共享控件、滚动条、时间线和弹窗消费同一组语义变量，禁止组件各自反色或使用滤镜。

| 语义变量 | 浅色值 |
| --- | --- |
| background / surface / surface-raised / surface-hover | `#f2f5f9` / `#ffffff` / `#e7edf4` / `#dce5ef` |
| text / muted / faint | `#182330` / `#495b70` / `#566579` |
| border / border-soft / control-border | `#c5ceda` / `#dce3ec` / `#79899c` |
| accent / on-accent / selection | `#245ba8` / `#ffffff` / `#d4e3fa` |
| success / warning / danger | `#236943` / `#805300` / `#ac283d` |
| track-video / track-audio / track-text / track-caption | `#cdddf2` / `#c9e4da` / `#efdfc5` / `#e3d8ef` |
| scroll-thumb / scroll-track | `#8b9aab` / `#edf1f6` |
| overlay / clip-hover | `#17253666` / `#14233c0a` |
| settings-sidebar / settings-group | `#f8f9fb` / `#f4f5f7` |

深色设置侧栏/分组使用 `#151a20` / `#20262e`，对应 `--color-settings-sidebar` / `--color-settings-group`，仍由 tokens.css 维护。

工作区预览的空态与外围使用 `--color-workspace-viewer`，深色沿 `--color-viewer`，浅色沿 `--color-surface-raised`；空态、音频预览与缺失提示使用 `--color-workspace-viewer-text`（沿主题muted）。仅有可见画面或有效合成检查帧时，实际成片画布使用固定 `--color-viewer` 黑底；覆盖真实媒体的提示仍用 `--color-viewer-text`。空态和媒体画布不再共用固定黑底。品牌标记使用独立 brand/on-brand 变量保留现有配色，与系统图标一致。波形跟随实际主题重新绘制已读数据，不重新读取音频，不改变影片字幕或导出内容的颜色。

### MCP 设置（2026-10-30）

MCP 作为设置的连接分类，正文沿同一900px内容上限、独立滚动所有者、系统字体与全局Token。连接状态/权限、待核对请求、工具记录用现有 settings-group 表面分组并留20px间隔，12px说明支持换行；连接地址允许任意断行，避免窄窗口溢出。助手不再保留单独“工具连接”标签，使用“连接设置”直达同一分类；MCP运行时状态栏才显示只读/可编辑/待重连入口，执行专用会话直达执行记录。

设置分类使用具名button和 aria-current，切换更新页面标题并聚焦标题。MCP的凭据查看及请求核对仍复用共享Modal。启停、查看和审批等操作进行中禁用分类/返回，面板提供 aria-busy 和固定高度进度说明；读取失败可刷新，提交失败保留当前面板。具体权限、作用域和确认规则继续归原MCP契约。

### 工作台精简与设置分类（2026-10-30）

用户已授权按分析顺序实施（FW-R20）。设置分为偏好（外观）、AI与连接（模型服务、本地AI、MCP）、应用（媒体工具、诊断）、创作与资料（工作流预设、存储与恢复）。标题由 SettingsPage 的 settingsNavigation 唯一映射；不增加尚未实现分类。

- 工作台保留素材、预览、时间线、片段属性、创作参数与任务结果。安装/路径/登录/隔离诊断移入本地AI，主任务面板显示短状态与直达入口；规划和执行仍分别判断。
- ConnectionsDialog、MediaToolsDialog、ResourceMonitorDialog、SnapshotStorageDialog 通过共享 SettingsSurface 内嵌设置，独立宿主仍可使用原Modal变体。颜色与控件不另建系统；表单表面20px留白，复用settings-group与field-pair，长内容由设置正文滚动。
- ProjectMenu 用原生details/summary展开当前工程的版本、收集与恢复操作。菜单外点/ Escape关闭，动作后关闭并回到工程菜单焦点；不假冒缺少键盘实现的ARIA menu。顶部保留打开/保存/导出，移除重复助手入口；任务、MCP、诊断仅运行时保留必要提示。
- 模型服务和本地AI配置后只刷新相关元信息，不重建创作表单。API密钥默认遮盖，离开实际清空DOM输入；未保存配置离开需核对，操作忙碌时禁止离开和直接退出。SettingsFormState管理共享状态，不复制业务API。
- 工作流选择、载入、调参、影响预览和应用继续在工作区；预设管理复用WorkflowPanel的管理模式，继承当前草稿和版本基线，不提供执行按钮。版本/恢复资料整理集中设置，工程恢复本身仍归工程菜单。

## Typography

正文使用系统与中文回退字体，避免新字体下载引起布局跳动；基础正文 13px，密集元信息使用独立小字号 recipe。时间码使用等宽字体和 tabular-nums，固定数字宽度。

工作区文字为交互近似预览。字体替代、自动换行及编码输出需通过后端合成检查核对；字幕默认距底 48 工程像素，左/右文字对齐保留 32 工程像素边距，与后端基础定位保持一致。用户自定义文字颜色属于影片内容，不套用 UI 颜色限制。

## Layout

顶部工程操作与工作区工具栏；左侧素材和 LUT；中间预览、播放控制与选区条；右侧属性/生成/语音/助手/导出任务；下方四类时间线轨道。两侧可收起，时间线高度可拖动或用分隔线方向键调整。

生成在同一右侧面板中切换“创作”和“生成任务”；模型连接集中在设置的模型服务分类，以图片/视频和语音分组，复用同一表单所有者；创作面板只保留连接选择与管理入口。参数、计费核对与已保存候选使用原有控件、表面及警告 token，不引入独立主题或大卡片后台。候选区显示实际结果的授权本地 URL，没有结果时不放示例图片。

AI 助手沿用同一侧栏、字段、按钮与 Modal，展示本地 CLI 检测、剪辑规划和记录；不另外建立聊天视觉或终端模拟器。隔离检查和结构化 JSON 采用按需展开，主要摘要直接呈现片段、帧数和操作变化。记录首批显示 12 条，单份摘要首批 8 项，剩余内容由用户展开，避免大历史一次挂载全部 DOM。

助手内部以“规划、执行、工作流、工具连接”短标签切换，使用已有 PanelTabs；导航保持单行与键盘选中行为。工作流预设、证据与影响，以及 MCP 状态/待核对请求复用同一 agent-panel 表面 recipe 和 Field/Input/Select/Modal。影响与证据首批 20 条，调用审计显示最近 40 条，不引入第二套主题或终端执行界面。

语音页分为转写、配音、音色和任务；源词纠错在转写页按接受版本展示，原始识别在任务详情只读。任务列表首批 12 条且只读取摘要，显式查看才加载详细结果；词条和绑定字幕各首批 40 项。音色按明确查询和翻页读取，试听仅使用已保存音频的本地授权 URL，preload=none。长文本使用同一固定高度、可滚动且 resize-none 的控件约定。

主要桌面视区分别拥有自己的滚动容器。素材列表初始显示 40 个并显式追加，避免一次生成过多 DOM；时间线按横纵可见区和固定66px行高裁剪，标题与内容共享纵向所有者。全局类型编号、行底色和名称位置不随窗口改变；焦点与拖动对象暂时保留，定位选中片段沿用既有图标按钮。片段索引和可见刻度不驱动每帧时钟。

当前首轮最小布局宽 760px、最小内容高 620px；窗口更小时页面可滚动保留操作，不宣称已经适配手机。小桌面宽度下减少侧栏宽度与辅助信息，不移动主要导出行动。

## Elevation & Depth

层级主要由中性色表面和分隔表达。普通面板无大阴影；菜单提示、模态对话框与拖动片段可以有短暂阴影。Tooltip 层 80，对话框遮罩 90、内容 91；预览图层仅在自身 stacking context 内分层，后续原生表面仍需 Wails 实机检查。

## Shapes

共享控件圆角 6px、小型标记 4px、模态框 10px；时间线片段为 5px recipe。控件视觉尺寸稳定，忙碌不改变按钮宽度。细片段修剪把手提供精确鼠标目标，同时属性面板的帧数输入提供不依赖拖动的替代。

## Components

- 应用图标按用户要求统一复用左上角的场记板标记：浅蓝背景、深色场记板，图案来自现有 Lucide `Clapperboard`，尺寸和颜色来自 `.brand-mark` 及其 CSS 变量。`scripts/export_app_icon.mjs` 直接渲染该组件并读取现有样式，导出 `build/appicon.svg` 与 `frontend/public/favicon.svg`，对应桌面 PNG 为 `build/appicon.png`。修改品牌标记时须同步导出 PNG；Wails 运行图标和每次生成的 Mac `.icns` 共用该 PNG，禁止采用旧应用资源或另一个 A 字母标识作为来源。

- `frontend/src/components/ui.tsx`：Button、IconButton、Field、Input、Select、PanelTabs、Modal、TooltipProvider。常规按钮 30px 高，图标按钮 30px，轨道工具 24px；原生 Select 明确接受操作系统弹出窗口样式。
- `frontend/src/components/Timeline.tsx`：片段、播放头、分割入口、轨道控制、范围和缩放；高频播放头直接订阅外部时钟。
- `frontend/src/lib/use-editor.ts` / `lib/clip-selection.ts`：选区清理读取即时权威Bootstrap，仅当前接受模型可清理ID，旧模型不得清空刚构造的副本；保持删除后活动片段回退及现有视觉。
- `frontend/src/components/SelectionInspector.tsx` 与 `lib/clip-selection.ts`：时间线多选/活动片段、索引框选与整组暂态；汇总沿用共享Field/Input/Button，失败保留帧参数，锁定及同量修剪有说明。修剪复用共享Select选择边缘、整数帧Input与Button，保留失败参数，参数不作为工程草稿。分割和复制到播放头共用现有时间线图标/Tooltip，成功选中新副本，失败保留原选择；不新增主题或控件系统。
- `frontend/src/lib/use-timeline-clipboard.ts`：工程剪贴板摘要与作用域/版本守卫，Timeline沿共享IconButton/Tooltip提供复制、剪切、粘贴及数量标记；输入与其它面板保留系统行为，操作后聚焦时间线。快照内容留Go，不新增主题。
- `frontend/src/components/ClipWaveform.tsx` 与 `lib/waveform.ts`：音频及有声视频复用22px源峰值包络；继承片段文字色和轨道底色，只分配可见Canvas，后台读取不驱动播放时钟。名称与修剪入口保持原位，失败在波形位置提供重试。
- `frontend/src/components/Preview.tsx`：仅挂载当前时间活动素材，区分工作区预览、后端合成检查帧与可选的实时合成预览；未实现的 LUT 工作区效果不显示假成功。
- `frontend/src/components/ProjectVersionsDialog.tsx`：工程名称菜单进入具名版本，复用共享Modal/Field/Input/Button与replacement-dialog正文/底部滚动所有者，760px业务宽度；版本按真实时间排序，完整名称可读、操作按钮有界省略。恢复采用同窗核对与自动恢复前备份，不新增Token或第二套未保存保护；关闭回入口焦点。
- `frontend/src/components/ProjectCollectDialog.tsx`：工程名称菜单进入收集副本，复用760px共享Modal业务宽度与内部表单滚动；核对数量/大小、上级目录和副本名后显式启动，任务沿现有面板。底部使用同一dialog-actions间距/对齐，版本窗口同时补齐该共享类；没有新增色彩Token。
- `frontend/src/components/RecoveryCenterDialog.tsx`与`ProjectSnapshotList.tsx`：工程名称菜单的恢复入口与工程名count-badge异常/数量提示，760px业务Modal沿同一正文/底部滚动；与具名版本共用历史条目布局，保持全文、真实时间、修订及来源可读。启动不抢输入焦点，错误沿global-alert/同窗form-error；状态每5秒读取且单一在途，不参与播放时钟，没有新增色彩Token。
- `frontend/src/components/SnapshotStorageDialog.tsx`：版本和恢复窗口以共享业务入口切到资料管理，不嵌套Modal；760px宽、720px高上限，PanelTabs分类型/位置，历史条目沿同一ProjectSnapshotList。只读信息不猜损坏记录的修订，安全动作先聚焦；危险按钮仅在独立永久删除核对出现，原有Token和样式保持。
- `frontend/src/components/RealtimePreviewSurface.tsx`、`lib/realtime-playback.ts` 与 `playback/audio-processor.js`：按需加载 Canvas、二进制帧队列与 AudioWorklet，声音实际消费驱动外部播放头；能力和工程校验不通过时明确显示不可用，不启动替代假时钟。
- `frontend/src/lib/legacy-media.ts`：旧 HTML 媒体的真实声音区间和结束保护；保持近似预览标记，不把浏览器媒体调度称为采样精确。
- `frontend/src/components/Inspector.tsx`：选区属性与帧数替代操作，Go 核心最终验证源区间、锁轨和版本。
- 缺失素材卡与片段源区提供“重新定位素材”，沿用共享按钮与路径对话框，不增加另一套弹窗；该动作保留剪辑编排，不混同为替换内容。
- `frontend/src/components/SubtitleLibrary.tsx`：静态字幕的导入/导出、按文本筛选和时间定位；使用已有表面、文字与强调色，不把静态字幕称为语音转写。
- `frontend/src/components/GenerationPanel.tsx`：动态能力表单、当前工程参考图和计费核对，只有核对框中的明确确认提交生成。
- `frontend/src/components/ConnectionsDialog.tsx`：连接元数据、默认遮盖的密钥与显式旧凭据清理；保存状态只表示已配置，不代表账号权限已验证。
- `frontend/src/components/GenerationJobs.tsx`：独立生成任务、停止本地等待、恢复已有查询、人工核对未知记录和逐候选导入；不复用导出任务的生命周期含义。
- `frontend/src/components/AgentPanel.tsx`：本地安装/登录/隔离检测、仅供复制的登录说明、工作流前置条件、额度确认、规划摘要和显式应用。检测不等于推理，副本校验不等于时间线已修改。
- `frontend/src/components/SpeechPanel.tsx` 与 `SpeechCreationForm.tsx`：语音能力、连接、显式查询音色、上传计费确认；语音模型以独立 modelOptions 复用 ConnectionsDialog，不伪装成图片/视频能力。
- `frontend/src/components/SpeechJobs.tsx`：摘要与按需加载详情、原始词时间、试听、接纳或导入、仅本地保存恢复及未知结果核对，不展示虚构 ASR、费用或进度。
- `frontend/src/components/SpeechAudioResult.tsx`：已保存音频、两套真实时间核对、配音全部已有片段范围与字幕导入；按需加载，不自动生成或识别。
- `frontend/src/components/CaptionLayoutEditor.tsx` 与 `CaptionLayoutPanel.tsx`：统一逐词/阅读分句参数、人工修改保护范围、预览与明确应用；沿用原字段和反馈，不新增主题。
- `frontend/src/components/SourceTranscriptEditor.tsx`：稳定源词 ID/微秒/说话人的接受版本纠错，显式解除字幕绑定；复用 Field/Input/Select/Modal，原始识别不可编辑。
- `frontend/src/components/SourceThumbnail.tsx` 与 `AssetSourceBrowser.tsx`：素材卡和源画面共用可见请求、取消及加载/失败反馈；保留卡片几何，详情以五张近似时间取样和原生键盘滑条浏览。沿用背景、边框和选中变量；重试使用共享 Button，浏览不改变工程。图片解码完成才标记就绪，源画面不包含时间线效果。
- `frontend/src/components/ExportSettings.tsx` 与 `lib/export-options.ts`：导出路径继续由共享 Modal/Field/Input 管理，合成方式、画幅、编码器和范围使用共享原生 Select，自定义区间使用整数输入。错误关联具体帧字段并恢复焦点；同名输出需显式选择，重复提交由拥有者串行保护。原生系统菜单的双端展开视觉另验，不以无头截图代替。
- `frontend/src/components/Preview.tsx`：实时模式的全/半/四分之一精度沿用共享原生Select，与模式、原画幅标识同一工具栏。改变精度先核对草稿并暂停，按当前帧重新准备；新工程默认全精度。较低精度用于性能近似，字号/色彩细节用全精度核对，导出使用原画幅。
- `frontend/src/components/AgentExecutionPanel.tsx`：第一方 MCP 执行的独立诊断、整个工程权限/额度核对、阶段记录、明确取消和工具审计；原计划模式保留。
- `frontend/src/components/GenerationReplacementDialog.tsx`：本地候选替换的源区间对照、声音/LUT/速度选择、后端预览与明确应用；沿用共享 Modal 的可选业务 className。
- `frontend/src/lib/agent.ts`：可用性、应用版本边界、编辑摘要与供人复制的登录文本格式化；不执行命令，不把剪辑要求拼成 CLI 命令。
- `frontend/src/components/WorkflowPanel.tsx` 与 `lib/workflows.ts`：目录驱动参数、显式载入/保存/删除预设、前置证据、预览与单独应用；savedFrames 只表示成片帧数变化。
- `frontend/src/components/MCPPanel.tsx`：显式只读/可撤销编辑作用域、运行状态、按需查看并清空连接配置、调用审计、由人核对的生成/语音/导出请求，不替用户配置外部 CLI。
- `frontend/src/lib/use-editor.ts`：共享串行编辑、忙碌、持久错误和工程读模型；失败不清空已有工程。
- `frontend/src/App.tsx`：全局持久状态条、错误恢复和任务面板。

### 视觉与行为状态

按钮具有默认、悬停、焦点、按下、禁用及忙碌状态；错误保留在可恢复的位置。业务模态框采用 Radix 焦点陷阱、Escape 和返回焦点；未保存工程的新建、打开及关闭使用同一保护流程。普通可撤销编辑不逐条确认。

PanelTabs 有方向键/Home/End 与面板关联。素材搜索有显式清空。键盘 C 分割、Delete 删除、方向键定位及保存/撤销不在输入和 IME 组合态中触发。动效只用于 120ms 控件反馈和真实忙碌状态，减少动态偏好可关闭非必要运动。

窗口关闭先提交当前属性输入并等待 Go 编辑完成，随后按最新工程状态决定关闭或显示保存对话框。草稿非法或状态核对失败保留窗口，不能借旧的干净状态跳过输入。

Wails v3 的 macOS 隐藏内嵌标题栏由系统绘制交通灯按钮；`main.tsx` 在首屏前设置平台标记，仅 macOS 的 `.app-header` 使用 `tokens.css` 的 `--native-window-controls-inset: 88px` 左侧占位，避免覆盖品牌与操作。浏览器和 Windows 沿用普通头部间距。

新建与打开同样先核对草稿和最新工程；属性必填数字的 DOM 约束与提交校验一致。工程草稿检查限定属性区，生成/连接表单的错误不作为工程 dirty；仍会失焦当前输入以提交真实属性修改。

密钥只保留在当前连接表单，保存成功（含清理 warning）与取消均清空实际输入并恢复遮盖；不记录到浏览器持久存储、状态消息或生成请求。生成数量默认 1，确认框列出提供商、模型、模式、参数和参考素材并明确可能计费，不展示虚构价格、百分比或远端取消状态。

本地 Agent 单独确认所选 CLI、工作流、选区与工程版本，明确可能消耗账号额度。计划与应用分为两次明确操作；旧工程、旧版本、未通过核心校验和已应用历史均禁用应用。登录命令只展示，复制失败可手动选择文本；不同终端格式明确标注，不假装执行或登录成功。

MCP 默认只读且不自动启动。连接凭据只在明确查看时加载，关闭时清空实际文本和组件状态；普通状态及审计不包含 Authorization。外部收费请求必须在客户端核对精确模型、数量、上传素材/文本；克隆主体与授权由人重新填写。导出路径由人选择，外部请求不携带输出路径。

全局轻量状态轮询只在 MCP 运行时继续，只有 revision 变化才读取工程。有 Inspector 未提交草稿时暂缓自动刷新，错误恢复不能用新工程抹掉输入；明确放弃草稿并重读才重挂属性控件。拖动/修剪期间同样暂停同步和旧手势提交，提示重读后重试，避免旧 Clip 字段配上新 revision 覆盖外部编辑。此状态沿用持久警告和共享 Modal。

语音提交确认列出供应商、模型、用途、实际上传素材或配音文本；克隆还列出主体与本人/授权声明，创建结果仍清楚显示未验证。素材转写、接受源词和生成绑定字幕分开；缺失时间显示“未提供”，不填零或按文本估时。识别结果和已接受版本分开保存，纠错草稿需明确保存；版本变化保留草稿并禁提交，重新读取由用户决定。解绑使用共享 Modal，保留文本、样式与时序并可撤销。

## Do's and Don'ts

- 优先保留画面与时间线空间，选区、属性和编辑动作必须对应同一真实对象。
- 重用公共组件、设计变量与 Go 编辑命令；失败和未接入能力给明确说明。
- 不预载整条工程所有媒体，不用 React 全局状态逐帧搬运视频或传播播放头更新。
- 不以本轮静态 audit、类型检查或 CSS 数学对比冒充双端实机、完整无障碍或专业实时合成验收。


### 本轮执行与镜头替换约定

CLI 计划和直接执行分别诊断，执行仅接受 execution_ready 的第一方工具会话。直接执行默认允许可撤销编辑，但必须单次核对整个当前工程权限及 CLI 额度；选区只提供上下文，不伪装为写入边界。可改为只读，普通工具编辑不反复确认。取消、失败或中断保留已提交编辑，按批撤销；模型收费/导出仍走人工核对。

候选替换采用更宽的桌面业务 Modal：最大宽1160px，受视口约束，标题与底部操作不滚走，正文单独滚动，错误显示在操作上方。原镜头/候选保持16:9容器、各两行元信息；画面是默认静音的源区间对照，不冒充最终 LUT、画面变换、字幕或声音合成。源入点输入用秒（最多6位小数），准确转换为整数微秒；素材时长至少显示3位小数秒，工程范围仍明确显示帧。

替换先预览再应用，保留位置、时长与构图，声音、LUT与速度策略明确选择，音轨载体和来源字幕影响以服务返回说明为准。参数或工程变化清除旧预览。替换弹窗与执行面板按需加载，避免常规剪辑首次加载携带全部可选界面。

属性提交失败或外部冲突时，面板和片段导航先等待，不能卸载 Inspector 来绕过草稿保护；挂起冲突不因DOM消失自行解除。直接执行结束即使专用工具已停止，也经同一保护入口核对工程。待核对请求使用自己的 canApprove/approvalReason，不能借新工具连接给旧请求授权。


### 配音真实时间与阅读分句

配音表单显式选择“获取配音时间戳”，默认不选且只在供应商支持时可用。核对框说明一次带时间生成，不自动识别、回退或额外请求。格式显示为用户可读的 MP3 参数，协议值保持原样。

任务摘要用紧凑状态区分原文与朗读文本的时间是否可核对，不把结构齐全说成一定能绑定；详情仍由人展开。两套字符文本与可空秒数分开阅读，原文缺失不由朗读结果替代；缺时间时仍可独立导入音频，不诱发重新收费。字符时间每批40项，原始JSON只在高级展开时显示。

“逐词显示”和“阅读分句”使用共享控件，阅读默认每行18字、2行、单条4秒和停顿0.700秒；用户单位为秒/字/行，后台整数微秒。配音补字幕明确作用于素材全部上轨片段，已有音频不重复添加、不改位置。来源接纳后固定，调整分句或换字幕轨走专用预览，不能靠重复导入覆盖人工修改。

字幕来源按识别/配音以及原文/朗读文本明确标记。源词纠错的秒数输入保留精确微秒与稳定身份；阅读设置放在来源选择后、词编辑之前，旧来源默认逐词。任务的“查看或预览字幕分句”会自动展开并滚入对应设置，普通进入仍折叠；有纠错草稿则先保留，不切来源。预览显示前后条数、保护条数和原样保留的人工文字/样式，明确应用才修改，支持整组撤销。

新建字幕轨使用显式 createNewTrack；应用后从返回来源刷新目标轨，避免下一次再次新建。阅读预览及配音结果界面按需加载，保持常规剪辑工作区包体与更新负担受控。

字幕目标轨随 Undo/外部移除恢复有效选择，排版参数草稿保留并提示重新预览；界面不以“新建”外观隐藏已删除的旧目标。

### 实时合成预览与音频末端

预览区沿用现有石墨色表面、共享 Select 和状态标记，新增显式“实时合成预览（实验）”模式。选择前检查本机引擎、当前工程和 WebView 能力；准备、缓冲、暂停、结尾与不可用使用真实状态。失败保留原因和重新准备/返回工作区入口，不能在实验模式下悄悄切回浏览器假时钟。素材单独预览仍明确显示素材名称。

工程修订后的失效会话只允许更新暂停位置，保持最新明确定位供重准备使用，不复活旧播放。原生系统选择器与共享控件继续沿既有所有者；macOS保存后重开及候选播放的证据见[原生媒体验收](docs/implementation/2026-10-26-macOS原生媒体流程验收.md)，没有新增视觉Token。

实时画面与状态采用上下两行独立布局：画面占剩余空间并等比完整呈现，状态行在画面之后，不覆盖字幕或镜头下沿。较长失败说明在状态行有界滚动，重试和返回入口保持可达；画面不为普通状态叠加横条。

实验画面由二进制 RGBA 直接绘入 Canvas，音频仅由 48kHz 双声道 AudioWorklet 输出，不与旧 HTML 媒体混音。播放头订阅外部时钟，React 不逐帧搬运图像或音频。最多三组未确认帧，消费声音且画面显示或明确丢弃后才确认；欠载输出静音并冻结源时钟，定位、暂停、新 epoch 和卸载清空旧队列。引擎会话绑定工程版本，编辑后失效，继续播放需重新准备。

播放前在既有三包上限内预缓冲真实声音，目标50ms；低帧率单包足量即可启动，高帧率到三包上限或短流收到结尾即可启动，不等不存在的后续帧。真正耗尽后保持当前位置并按相同水位补足再恢复，设备等待输出的零不成为工程内容，也不推动播放头。状态行明确区分首次预缓冲与运行中补充；启动等待和运行欠载分别统计，不靠隐藏计数冒充连续播放。

音频添加走后端完整源区间接口，由核心确定真实声音长度与容纳其末端的时间线长度。已测样本数与未知值分开；不从素材微秒时长估造样本总量。旧工作区按声明的声音区间静音/停止，防止极短声音结束后自动从零重播，但 HTML 媒体调度与有理数偏移仍明确是近似预览。实验路径和双端 WebView 的实际能力、性能及合成一致性另行验证，组件测试与构建不代替实机验收。

普通导入或音频上轨返回的 warnings 沿用全局通知区域，使用现有警告色和可用键盘展开的“本次操作有 N 条提示 · 查看原因”；多文件原因有界滚动，完全相同文字去重，未保存状态不会遮住提示。提示仅属于本次返回，后续工程快照未提供或返回空数组即清除，不持久化到工程，不将声明时长标为已实测样本。

## 素材内容检索补充（2026-10-26）

素材库内容页沿用原工具密度与滚动所有者，以关键词、素材范围和来源组织只读结果。每项保留素材名、源时间及来源标签；匹配内容保持原文，不采用独立AI聊天外观。SourceRangeMedia成为候选对照与内容回看的共享区间预览，继续原Modal/Field/Select和运行时token；没有新增或复制设计token。行为归UX-CONTRACT.md维护。

Agent源画面权限采用既有checkbox和确认Modal；运行审计中的证据列表复用同一密度、边界和文件标识样式。点击后查看真实本机派生图像，没有示例图或新主题。尺寸受当前面板和Modal约束，行为由UX-CONTRACT.md说明，仍无新增主题token。

画面分析核对沿用原MCP请求Modal，分类标题表达观察、未确认推断与建议；帧证据在同一Modal内展开，避免嵌套对话框。内容检索页沿用原结果列表，显示离散取样和提交来源，操作使用共享Button，未新增颜色/字体/token。

本地镜头索引位于原素材源画面区域的details内，参数/任务/候选段复用相同控件密度。候选20段一批明确追加，选段定位原浏览滑条；没有新主题或自动生成的示例缩略图。

按文字剪辑沿用语音页details与共享控件，连续词以可换行的按下态按钮表达范围，80词显式加载、词区最大240px内滚动。实例/位置采用原生Select，最后确认才用danger动作；源试听沿用既有Modal和SourceRangeMedia。没有新增颜色、字体或主题token。

源代理沿用素材详情details与共享表单控件，后台阶段无虚构百分比。原片/两档代理整合进既有预览模式原生Select，合成精度独立，状态显示实际选择数量及原片声音/导出。标题高度可随必要换行增长；900px下模式158px、精度112px，保留约100px画面和可见焦点。尺寸为桌面工具密度几何，不新增主题token或弹出层所有者。

时间区间沿用时间线工具栏文字按钮与I/O标记，覆盖仅绘制可见范围且不拦指针。确认窗采用既有field-pair和内部有界明细滚动，安全返回与最终danger动作明确；没有新增主题token或原生控件所有者。

## 当前画面参考素材补充（2026-10-26）

预览入口沿用工具栏文字Button，取图位于生成页原details与共享字段控件中，不引入聊天式界面或新主题token。图像是真实保存结果，以原比例contain显示、最大240px；尺寸和来源文字沿用field-hint。整个创作区域由原generation-form单一滚动区承载，取图与模型form分开，宽1440与窄900视区均保留独立预览和时间线；document不随面板滚动。具体行为以UX-CONTRACT.md为准。

## 生成候选对比补充（2026-10-26）

跨任务的两个实际成品以A/B并排，是版本比较而非模型营销卡。沿用replacement-dialog的定高外框、内部滚动和原比例画面；720px以下单列，桌面900/1440实际复查。选择摘要保留模型/描述以区分同名文件，长文本可换行，不新增主题token。SavedCandidateMedia作为列表和对比的成品读取所有者；动作行以candidate-compare-actions扩展既有speech-actions的flex/wrap/4px间距，保留按钮之间的可见边界。键盘返回与错误恢复由UX-CONTRACT.md约束。

## 生成参数复用补充（2026-10-26）

复用核对以既有generation-review映射参数，沿Modal/replacement-dialog所有者，仅设760×620上限的业务几何，避免少量参数占满大屏；长内容仍内部滚动。连接沿共享原生Select，不为供应商再造独立页面；“沿用服务商默认”表达旧记录的真实缺省。角色映射统一中文，父配方在最后核对可追溯，不新增主题token。范围、异步预填、导入恢复和焦点由UX-CONTRACT.md维护。

## 生成候选上轨补充（2026-10-26）

添加窗继续replacement-dialog定高/内部滚动，宽度上限900px；开始/长度、源入点/声音使用既有field-pair，预览区域沿SourceRangeMedia最多220px画面。源参数、实际后果和最终添加分层，固定footer可安全返回；窄900视区实际验证内部body滚动而document不动。共享I/O标记保持原时间线视觉，不新造第二套标记。候选处理时列表媒体统一暂停，中文说明区分新增/叠加与其它编辑策略，无新增主题token。

## Wails正式版运行基线（2026-10-26）

模块/CLI/runtime已从beta.34同步3.0.0，项目补丁Go1.27.3；现有React、Radix和运行时CSS变量保持，未新增主题token。正式版macOS原生页面、基本文字/历史与系统保存选择器实际通过，不能替代完整媒体/Windows和视觉验收。候选MCP仅扩展原工具目录，不引入另一套编辑界面。

## 属性输入草稿补充（2026-10-27）

属性保护提示沿Inspector现有field-hint，恢复沿RecoveryCenter的760px共享Modal、ProjectSnapshotList及内部滚动。基值与当前值以可换行的朴素文字比较，原始输入可编辑，错误使用既有danger语义和可见焦点。inspector-draft-value只增加pre-wrap/overflow-wrap几何，颜色、字体、间距仍由原运行时Token负责，没有新主题或第二套控件所有者。900×720实际检查对话框760×680、document无溢出；错误/返回/取消/重新核对按UX-CONTRACT维护。

## 工程字体补充（2026-10-27）

字体是可携带的剪辑资源，素材库增加“字体”页，沿原卡片密度，以真实文件名/家族/样式区分。属性字体沿原生Select，不做独立字体商城；字体声明沿details/field-hint，font-notice与既有保留空白规则共用几何。预览加载/错误沿viewer-footnote及共享Button，字体本身仅改变成片内容，不改变应用UI字体。没有新增颜色、字体、间距或主题Token；操作/恢复/缓存语义归UX-CONTRACT。

## 资源诊断补充（2026-10-27）

任务页以共享Button进入按需加载的诊断Modal。沿replacement-dialog内部滚动，业务宽度1040px且受视口约束；CPU/RSS是工具读数，采用原metadata-list与制表数字，不做营销数字卡。进程表原生语义、20份显式追加、最多128份，独立有界滚动；未知值使用文字。阶段与记录沿原生Select，导出沿共享路径字段/原生保存器，状态沿原错误和提示区。新增resource类仅定义业务几何与密度，沿既有Token，没有新颜色或主题。

## 编码资源档位补充（2026-10-27）

导出窗口继续共享Modal/Field/原生Select，在范围之后提供“编码资源”。标准沿旧设置，较低内存的耗时/压缩取舍在原field-hint显示；不新增主题、指标卡或颜色。900×720实际窗口460×686.19、所有说明与提交可达，仍由原对话框滚动所有者负责。MCP核对在既有摘要列显示资源档位，行为归UX-CONTRACT。

## 媒体工具设置补充（2026-10-27）

任务页新增按需媒体设置，沿既有Modal/Field/Input/Button、replacement-dialog内部滚动与全局token。900px宽业务变体只扩展几何，折叠明细显示当前来源，候选输入和当前会话分开；错误关联字段并聚焦，底部操作固定可达。900×720实测窗口860×688、document无溢出，Escape后焦点回入口。业务源为[媒体设置规格](docs/specs/2026-10-27-媒体工具持久设置.md)，不增加主题/弹出层或系统文件选择器所有者。

## 口播全轨闭合补充（2026-10-27）

WorkflowPanel沿已有Field/原生Select、内联agent-plan及滚动所有者，口播的范围字段称停顿候选来源，闭合范围明确独立。全轨影响用原/新长度、原片段变更与新增存续段说明，沿既有警告样式，不增加弹层或主题。900×720的表单/菜单与内部滚动已验；业务源为[全轨口播规格](docs/specs/2026-10-27-口播粗剪全轨闭合.md)。

## 音频淡入淡出补充（2026-10-27）

Inspector声音区复用既有NumberField/Field/Input/Button与field-pair，秒数和清除沿原读模型/Undo，原曲线派生窗用field-hint说明，不新增主题/弹层。原显示基线数值及等价格式重输不制造提交/错误或舍入丢失；新输入受当前范围限制，IME确认不失焦。900px中两输入均在视区且有效，正文继续原滚动所有者。业务源：[淡化规格](docs/specs/2026-10-27-片段音频淡入淡出.md)。

## 批量声音工作流补充（2026-10-27）

新方法沿WorkflowPanel动态参数、共享Field/原生Select/Input/Button及既有预设Modal。清除时禁用非适用数值/策略并说明，省略其校验/提交，保留草稿供切回；不新增主题或弹层。默认操作范围选择所选，空选区禁用预览；内联计划列出变更/保留/跳过/实际时长。900×720正文无溢出，菜单AX展开/Escape和原滚动所有者保持。业务源：[批量声音规格](docs/specs/2026-10-27-批量声音淡化工作流.md)。

## 新建工程规格补充（2026-10-27）

新建沿App原Modal/未保存保护及Field/Input/原生Select，增加横竖方形、自定义尺寸和有理帧率。new-project-dialog仅将宽度扩展为560，沿原对话框滚动，不新建主题/弹层所有者。实际像素和分数摘要为主，错误关联首项焦点；取消与成功都回新建入口。900×720自定义错误窗560×577.04且无页面溢出，业务源：[新建规格](docs/specs/2026-10-27-新建工程规格设置.md)。

## 轨道管理补充（2026-10-27）

时间线设置沿共享IconButton/Tooltip，窗口复用Modal、Field/Input、checkbox-field及原生Select；几何沿原460宽，不改变主题/字号/滚动。静音与画面隐藏按媒体类型分开，排序是一次保存的明确选择。900×720窗460×454.89无页面溢出；命名/层级说明与取消/保存和新建/保存窗口一致，见[业务规格](docs/specs/2026-10-27-轨道管理与同类排序.md)。

## 随包媒体模式补充（2026-10-27）

沿媒体设置Modal/Field/Input/checkbox-field及当前来源details，新增显式自动模式，不改主题/滚动/字段所有者。自动禁用路径输入，手动草稿往返保留，保存失败清旧检测和旧提示。900×720窗860×688、document无溢出，操作区仍固定，见[业务规格](docs/specs/2026-10-27-随包媒体运行时发现与校验.md)。

## 目录迁移素材修复补充（2026-10-27）

素材库提供文字入口，沿共享Modal/Field/Input/Button及既有replacement-dialog宽度和内部滚动。新旧目录并排，窄窗口单列；逐项路径完整换行、只读结果每页20条，应用按钮明确本批匹配数。沿tokens.css的间距、边框、正文/等宽字体和成功色，不新增主题或颜色常量；核对可取消、应用后回工作区并一次撤销。业务源：[目录修复规格](docs/specs/2026-10-27-目录迁移批量素材修复.md)。

## 多序列补充（2026-10-27）

时间线上方使用共享PanelTabs的manual变体呈现全部序列，直接选择常用剪辑版本；方向键/Home/End只移焦，回车/空格明确激活，避免移焦时反复重建媒体会话。标签栏独立横向滚动，单项最多240px并截断，完整名称保留在可访问名称、title和序列管理内；当前项/焦点项可滚动到可见位置，管理图标保持可达。右侧面板和素材分类仍沿automatic变体。没有新增色彩或控件Token。

序列管理以760×640稳定框架承载选择/编辑/删除核对，不叠加第二层弹窗，内部沿replacement-dialog-body滚动、底部动作固定。新建复用NewProjectSettings的sequence业务变体，明确共享素材，不把创建序列写成新建工程。字段、按钮、提示及错误沿既有token；删除进入同窗核对并先聚焦返回。管理窗口的序列Select仍接受平台弹出样式。业务源：[多序列规格](docs/specs/2026-10-27-多序列工程.md)；[标签交互验收](docs/implementation/2026-10-27-序列标签与原生切换验收.md)。

源画面文字识别沿用Inspector/SourceOCRPanel的Field、input resize-none、Button与Modal，源图限制在窗口可视范围，原始置信度按需展开。仍使用当前颜色/字体/焦点和滚动所有权，不增加主题；OCR属于源素材判断，不引入聊天面板。

Windows系统OCR复用同一源画面核对窗口和控件；实际识别器/语言放在来源说明，未知置信度使用普通辅助文案，保留Apple数值明细。没有新增颜色、字体或第二套工作区。
