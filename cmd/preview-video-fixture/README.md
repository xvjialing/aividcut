# 编号视频与定位负载

只向尚不存在的新目录写入自建视频及工程，不使用用户素材或模型。需要显式可用的FFmpeg/ffprobe和libx264编码器。

```sh
go run ./cmd/preview-video-fixture -out work/video-example -seconds 90 -clips 64
go run ./cmd/preview-video-fixture -out work/video-vfr-example -seconds 90 -clips 64 -vfr
go run ./cmd/preview-video-fixture -out work/video-720-example -seconds 90 -clips 64 -width 1280 -height 720
go run ./cmd/preview-video-fixture -out work/video-audio-example -seconds 300 -clips 1000 -audio
```

源为60fps；64×32的8列/2行亮暗格保存16位源帧号，可按阈值独立读出，不依赖字体或平均帧率。VFR删除每5帧中的一帧并保留原PTS。大画幅由编号格最近邻放大，属于可复现编码/像素负载，不代表真实摄影纹理或运动复杂度。

H264使用CRF18、固定120帧GOP、2个B帧且禁用自适应B帧/场景切换。必须以ffprobe实际核查has_b_frames，不能仅凭参数声称夹具带B帧；早期CRF0输出曾被编码器禁用B帧，不作为B帧验收证据。

默认保持无声，`-audio`显式加入48kHz/440Hz正弦并编码AAC；来源为自建测试声音，不是自然语音或真实拍摄。该模式可配合preview-benchmark检查实际非零PCM、首尾及分割相位；它不能证明设备发声、听感、音色或ASR质量。Windows诊断脚本默认使用有声夹具。

工程29.97fps、12345µs源入点，包含whole.aividcut与通过实际核心命令分割的split.aividcut。源长度1–300秒、1–10000段，片段数不得超过源可用工程帧；源/工程偶数画幅64–1920×32–1080。分割在单个核心事务中逆序切同一左段，避免负载生成自身积累巨量历史。素材指纹为实际文件SHA-256，工程保存绝对路径。比较时固定输入、SDK、worker和代理尺寸，记录真实帧号/像素及有序定位，不能只看性能。
