---
name: captions
description: 为已有静态或源绑定字幕批量设置明确字体颜色和像素偏移，保留原文及来源，不伪造转写或断句。
version: 1.0.0
---

# 已有字幕样式与布局

读取当前工程，调用 `aividcut_list_workflows` 检查参数。默认只处理选中字幕，或选中源及其 linked 组的绑定字幕；`scope=all_captions` 才明确处理全部字幕。标题文字不属于字幕，不自动修改。相关字幕锁定会阻止整批应用。

```json
{"input":{"workflowId":"captions","selection":["<字幕或源ClipID>"],"parameters":{"scope":"selection","fontSize":48,"color":"#ffffff","background":"transparent","align":"center","offsetX":0,"offsetY":0}}}
```

将此输入传给 `aividcut_preview_workflow`。`color/background` 支持 `#RRGGBB/#RRGGBBAA`，背景也可为 `transparent`；align为left/center/right。offsetX/offsetY是相对客户端当前字幕基准的绝对像素偏移，不是百分比、字数或新的断句时间。

方法只改变字号、颜色、背景、对齐与偏移，保留文字、时间、字幕ID和源绑定；核心会记录人工样式覆盖。已有人工文案不会被换成ASR原文。陈旧绑定只允许整理现有文字样式，不能因此声称来源重新验证。参数已经一致时返回无需编辑，不制造无意义撤销项。

检查预览的范围与实际布局后，用户明确同意且MCP连接有编辑权限时才调用 `aividcut_apply_workflow {"id":"<预览ID>"}`。不要用这个工作流生成新词时间、改写原始识别或安装用户CLI配置。一次应用可整体撤销。
