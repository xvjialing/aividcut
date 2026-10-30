# 字幕基础模块

本模块将静态 SRT 交换与源转写时间映射分开。它不调用 ASR 服务；持久转写、接受纠错和跟随字幕由 `editor` 核心持有，静态 SRT 导入不会自动取得源绑定。

## 公开接口

- `ParseSRT(text string) ([]Cue, error)`：解析 UTF-8 SRT，可有 BOM，支持 LF/CRLF、中文和多行文本。
- `FormatSRT(cues []Cue) (string, error)`：复制后稳定按开始/结束时间排序，保留重叠与同起点条目，重编序号。
- `CueToFrames(cue Cue, settings editor.Settings) (startFrame, durationFrames int64, err error)`。
- `FramesToCue(startFrame, durationFrames int64, text string, settings editor.Settings) (Cue, error)`。
- `ImportCommands(project editor.Project, trackID string, cues []Cue) ([]editor.Command, error)`：生成一个可由核心原子提交的命令批次。
- `ExportProjectSRT(project editor.Project, trackID string) (string, error)`：只导出字幕片段，空轨道 ID 表示所有字幕轨。
- `MapSourceWords(project editor.Project, assetID string, words []Word) ([]MappedWord, error)`：纯函数映射源词到有效素材片段的工程时间。
- `SourceWordFrames(project editor.Project, clipID string, words []editor.SourceWord) ([]BoundWord, error)`：带稳定词 ID 和可选说话人信息的纯帧映射，复用核心 `MapWordToClip`。持久绑定使用核心的 `accept_transcript/correct_transcript/generate_bound_captions` 命令，详见 `internal/editor/README.md`。

`Cue` 使用 `StartMS/EndMS`（JSON：`startMs/endMs`），区间为左闭右开。`Word` 与 `MappedWord` 使用微秒 `StartUs/EndUs`。

## 取整和边界

毫秒与有理数工程帧互换均四舍五入，恰好半帧向后。两端独立采用同一个规则，因此相邻字幕共享的边界不会变成不同帧。取整后零帧的字幕返回错误，不偷偷延长；按帧导出后再次导入可保持原帧边界。

源词映射使用精确有理数计算源窗口和正向速度。词区间与片段源区间求交后再映射到工程时间，最后按同一四舍五入规则量化为微秒。`Partial` 指明边界裁切，`SourceStartUs/SourceEndUs` 保留原始词范围供追溯；不足一个可表示微秒的边缘不生成零时长结果。

`SourceWordFrames` 与持久绑定采用单独的逐词帧规则：起点向下、终点向上取整，限制在源片段边界，保留每个可见词。由此产生的重叠使用分轨处理；它不改变 SRT 原有的共享边界四舍五入规则，也不表示已完成最终字幕断句排版。

同一素材在工程中使用多次会生成多次映射。该纯函数不决定可听轨道，包含静音片段的源时间关系；由调用者选择素材/轨道/片段范围，再决定是否形成字幕卡。

## 导入与导出行为

指定字幕轨时检查锁定和同轨冲突；空轨道 ID 时复用空、未锁定且未隐藏的字幕轨，并为重叠字幕增加轨道。新增 caption/text/video/audio 轨道由工程核心放入相应视觉分区，不重排已有轨道。重叠字幕会按行数和字号上移；大量同时字幕仍需用户检查画布布局。

SRT 序号需要唯一正整数，可不连续；输出重新编号。拒绝倒置/零时长、不正确时间格式、重复序号、无文本、文本内空行或歧义的嵌入序号/时间结构。当前仅支持纯 SRT 时间行，不接受位置扩展；样式标签保留为文本，不在本模块解释。静态隐藏不等于删除，导出源由 `trackID` 明确选择。

SRT 文本限制 32MiB、100000 条；源词限制 1000000 条、文本合计 32MiB。映射结果和区间扫描设上限，复杂源可按轨道/片段分批处理。导入命令还受核心的单事务命令上限约束，不会部分提交。

## 验证

测试覆盖 SRT 往返、BOM/换行/多行、同起点和重叠、非法区间、29.97fps、半帧取整、多轮帧往返、变速/移动/修剪、边界裁切、重复素材、锁轨、自动分轨及整体撤销。

`go test -count=1 ./internal/editor ./internal/captions ./internal/projectstore`

`go vet ./internal/editor ./internal/captions ./internal/projectstore`
