---
name: voiceover
description: 将已保存并导入的配音或录音按真实音频时长上轨，可使用其现有接受词时间生成绑定字幕，不生成新声音或猜字幕时长。
version: 1.0.0
---

# 已保存配音编排

先在语音任务中取得并保存真实声音，或导入现有录音。此工作流只使用当前工程已登记、未缺失、有指纹和真实时长的音频；需要新音色/配音时返回前置，让用户单独核对对应服务与费用。

```json
{"input":{"workflowId":"voiceover","selection":[],"parameters":{"assetId":"<已导入音频AssetID>","startFrame":0,"trackId":"","volume":1,"generateCaptions":false,"transcriptId":"","captionTrackId":""}}}
```

通过 `aividcut_preview_workflow` 预览。空trackId新建音频轨；已有轨必须类型正确且未锁定，重叠会拒绝，不覆盖原音轨。长度来自素材微秒时长与工程有理数帧率；严格源边界下不足一帧的尾数仍留在素材中，并有警告，不按文案长度估计、拉伸或补静音。

`generateCaptions=true` 必须有该音频自身当前指纹匹配的接受词时间；没有时返回前置，不按字符比例猜。可以给出transcriptId。已有源字幕规则继续生效，本选项不会暗中关闭它；新启用持续规则若会影响其它源复用，必须把相关源也选中。已有规则的目标轨不能通过这个编排工作流悄悄改到另一轨。

检查实际帧区间、来源和所有隐式字幕影响，再由用户明确应用。已授权的MCP连接调用 `aividcut_apply_workflow {"id":"<预览ID>"}`；否则只显示预览。源音频、原始识别、其它音轨和账户均不被改写，也不会产生收费请求。
