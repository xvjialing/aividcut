# 自建音频剪切负载

本命令创建新目录中的真实单声道 PCM WAV，以及同一声音的整段/实际核心分割工程；不读取用户素材、不启动模型或修改已有目录。源样本数来自实际写出的数据，指纹为文件 SHA-256。支持44.1/48kHz，工程为29.97fps、48kHz、64×36黑画面，源入点12345µs。

```sh
go run ./cmd/preview-fixture -out work/audio-example -clips 64 -seconds 6 -rate 44100
go run ./cmd/preview-fixture -out work/audio-long-example -clips 64 -seconds 300 -rate 44100 -speed-num 3 -speed-den 2
```

输出包含 `own-chirp.wav`、`whole.aividcut`、`split.aividcut`。输出目录必须尚不存在，父目录需先准备；失败不会覆盖已有资料。片段数1–10000、源时长1–300秒，片段数不得超过可用工程帧。逆序切同一左段、在单个真实核心事务内完成，不为生成负载保存上万份撤销历史。工程保存绝对素材路径，移动整个夹具后需按普通重定位规则处理。

`-kind image -clips 10000`生成静态图片段负载：自建64×36格图、320×180工程、每段1帧并周期改变平移。输出own-grid.png与split.aividcut，用于图管理/传输开销；没有真实声音或自然视频解码，不能将其静音PCM或吞吐推广为全部剪辑质量。

使用 `cmd/preview-benchmark` 分别只读打开两份工程，比较实际样本锚、PCM校验、加载/定位/拉帧与资源；保存相同输入、SDK和worker指纹。该夹具只覆盖音频及时间线片段开销，不能代表视频、字体、GPU、WebView或整机容量。
