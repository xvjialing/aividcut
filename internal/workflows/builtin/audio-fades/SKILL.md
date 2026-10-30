---
name: audio-fades
description: 对明确范围内的声音批量设置或清除淡入淡出，默认保留既有曲线；先预览后通过统一编辑核心一次应用与撤销。
version: 1.0.0
---

# 批量声音淡入淡出

先读取当前工程、选区及aividcut_list_workflows。此工作流仅更新v4音频包络，不生成声音、不识别内容、不读源文件或调用模型。已有淡化默认保留，不能未经明确要求覆盖用户曲线。

通过aividcut_preview_workflow传入input。scope必须为selection或project；selection需要具体且不重复的片段ID，不扩展音画链接；project明确表示整个当前工程且selection必须为空。不能以丢失选区替代全工程范围。

```json
{"input":{"workflowId":"audio-fades","scope":"selection","selection":["<声音ClipID>"],"parameters":{"mode":"set","targetKinds":"audio_only","audibleOnly":true,"inMs":100,"outMs":100,"existingPolicy":"keep","shortPolicy":"scale"}}}
```

mode为set或clear；clear只移除匹配声音上的已有曲线，省略设置参数。targetKinds可选audio_only或audio_and_video；audibleOnly默认true，跳过静音片段/轨、零音量和已知无声范围，隐藏画面不等于静音。包含视频原声或静音内容都应依据用户明确范围，不自动展开其它链接成员。

设置时inMs/outMs为0–86400000整数毫秒；existingPolicy=keep保留已有曲线，replace明确按当前片段重建。shortPolicy=scale沿核心比例缩短两端，reject则时长不足整批拒绝。参数设0/0并覆盖也可清零，但明确清除宜使用clear。最多1000个实际变更，明细200，不截断执行。

展示summary/audioFadeSummary/effects/warnings：检查实际两端时长、缩短、保留/跳过及范围。淡化按输出显示片段范围，不保证极短音尾覆盖、交叉混音或去爆音；离线素材仅改元数据，不能声称已试听。轨道锁定或核心校验失败整批拒绝，不顺手解锁或改静音。

只有用户明确应用且连接有编辑许可，调用aividcut_apply_workflow的返回ID。工程/参数/选区变化后重新预览；不直接改工程JSON或重放已应用的旧计划。结果一次Undo/Redo，不改变时间、源范围、原话、字体、音量或静音。参数可通过客户端另存预设，不修改用户Codex/Claude配置。
