---
name: talking-head
description: 根据当前源指纹匹配的已接受词时间，预览口播词间停顿压缩；保护词边缘、句末与说话人切换，不把词间空隙当成静音证明。
version: 1.0.0
---

# 口播词间停顿粗剪

先通过客户端读取当前工程及选区，调用 `aividcut_list_workflows` 检查此工作流的前置与参数。需要当前源指纹、时长一致且未陈旧的接受转写；静态 SRT 不能代替源词证据。选中音频/视频源，会纳入其完整 linked 音画组及关联字幕。第一版只支持相同工程区间、相同速度的完整组，所选组之间不能在时间上重叠；最多32组、128处候选，复杂情况分段处理。

调用 `aividcut_preview_workflow`，示例参数：

```json
{"input":{"workflowId":"talking-head","selection":["<源ClipID>"],"parameters":{"gapThresholdMs":800,"keepGapMs":300,"edgeProtectionMs":120,"maxGapMs":5000,"sentencePauseMs":500,"preserveSpeakerChanges":true,"rippleScope":"selection"}}}
```

方法先合并相互覆盖的词，再只考虑可见词之间的空隙；首尾未识别区间不删。超过最大候选、说话人切换、与原始识别中未被接受的词重叠时保留。句末标点额外保留停顿。删除区间按有理数帧率向内量化，保留词与保护边缘；不要根据文本长度或浮点fps推断切口。

预览会先用 `duplicate_clip` 从明确父源产生后续保留段，再修剪原ID，完整 linked 组同步处理，使跨gap的字幕人工文本/样式覆盖继承。只闭合所选源组，未选音轨、音乐和其它画面位置不变；若闭合与未选片段冲突则拒绝，而不是顺手删除其它轨。

上述为默认`rippleScope=selection`。需要配乐、标题和其它画面一起闭合时，用户必须明确选择`rippleScope=all_tracks`；选区仅决定停顿候选来源，全轨影响包含未选中、隐藏、静音轨和同素材其它实例。不能由“选择某段口播”自行推断全轨删除。多个候选以原工程半开帧坐标倒序交给`ripple_delete_range`，整批一次撤销；受影响锁轨或不能安全拆分的手改整句使整批拒绝。

全轨预览的`speechRippleSummary`给出切口数、原/新总帧、删除帧数、影响轨道和完整原/新标识数。标识最多2000、明细最多200，不能把截断数组长度称完整影响数。展示每个原工程切口及接受词证据，核对其它媒体来源；纯移动离线占位与需要实际裁切的源分开处理。该模式不运行额外识别或模型调用，默认及旧参数预设保持selection。

展示 `effects/evidence/warnings/affectedClipIds` 并试听候选切口。词间没有接受文字可能是未识别内容，不证明静音或语义错误。`savedFrames` 是最终工程总帧长前后差，音乐长尾存在时可为0；summary另列选区移除帧数，linked音画不双计。

只有用户明确应用且当前MCP连接有编辑授权时，调用 `aividcut_apply_workflow {"id":"<返回预览ID>"}`。不要自行执行request、改工程文件、重跑ASR或调用收费模型；工程或来源变更时重新预览。结果由同一编辑核心作为一次可撤销编辑应用。

## 明确按词删减

用户已经指定要删除的文字时，使用 `aividcut_preview_transcript_cut`，传当前 `projectRevision`、`transcriptId/transcriptRevision`、一个具体上轨 `clipId`、源顺序连续的 `wordIds` 及 `closeGap`。只读连接也能预览；应用仍使用上述编辑许可与预览ID。一次最多1000词，同素材其它上轨实例保持原位。`closeGap=true`仅闭合这个完整linked组；false保留空隙，未选轨均不波纹移动。

这里按帧覆盖所选词，与词间停顿向内量化的策略不同；部分词、邻词帧量化重叠会给警告，不能声称音素精确，也不自动判断口癖或重复。先展示词、源微秒范围、工程半开帧范围、完整音画组、字幕变化及所有warnings，再试听边界。原始与接受文字均保留；手改字幕不允许静默重写。范围或源版本已变化时重新读取和预览。

## 明确全轨时间区间

需要所有轨道后段一起前移时，调用只读 `aividcut_preview_ripple_delete`，传 `projectRevision/fromFrame/toFrame`，范围起始包含、终点不含。预览会影响未选、隐藏、静音轨，受影响锁轨或不能安全分割的人工字幕会整体拒绝。明确展示原/新总长、删除/分割/修剪/移动/字幕统计及有界明细，用户确认后仍用 `aividcut_apply_workflow` 和既有编辑许可。按词预览可明确传 `rippleAll=true,closeGap=false` 复用同一核心；旧 `closeGap=true` 仍只闭合当前组。不得根据选区需求暗中扩大为全轨操作。
