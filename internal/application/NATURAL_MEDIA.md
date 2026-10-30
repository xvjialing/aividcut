# 摄影/视效样本的显式验收

`TestNaturalMediaImportSplitUndoExportAndReopen`只在明确设置样本清单后运行，不自动下载、不读用户素材目录。默认skip不算实际摄影验证。样本应有来源、使用许可、修改与本机文件SHA256记录。

先配置已核验SDK的AIVIDCUT_FFMPEG、AIVIDCUT_FFPROBE、AIVIDCUT_MLT_WORKER、AIVIDCUT_MLT_REPOSITORY、AIVIDCUT_MLT_PROFILES、AIVIDCUT_MLT_DATA，然后：

```sh
AIVIDCUT_MEDIA_SAMPLES=/absolute/samples.json \
AIVIDCUT_MEDIA_EVIDENCE=/absolute/new-evidence-directory \
go test -race ./internal/application -run '^TestNaturalMediaImportSplitUndoExportAndReopen$' -count=1 -v
```

证据目录父目录须已存在，自身须不存在；不指定时用测试临时目录。输入清单v1、1–16项、ID为1–64位小写字母/数字/连字符，36–900帧、偶数画幅64–1920×32–1080；时基/源范围/速度还需通过原工程校验。每个case应为可用的有声视频，指纹需与实际SHA256匹配。

```json
{
  "schemaVersion": 1,
  "cases": [{
    "id": "example-h264-mp3",
    "path": "/absolute/source.mov",
    "sha256": "实际64位SHA256",
    "sourceInUs": 130012345,
    "frames": 120,
    "width": 640,
    "height": 360,
    "fpsNum": 30000,
    "fpsDen": 1001,
    "speedNum": 3,
    "speedDen": 2
  }]
}
```

实际流程为应用导入→设置规格/源窗/速度→完整原生读取→统一分割→逐帧/PCM比较→一次Undo/Redo→保存重开→两份独立原生导出→FFmpeg完整解码后比较。检查PCM非有限数、非零信号和连续采样锚，保留matrix.json、首帧、工程和成片。解码哈希只比较两次相同处理输出，不把有损编码当无损。

长工程单帧对照另用：

```sh
AIVIDCUT_LONG_PROJECT=/absolute/long-whole.aividcut \
go test -race ./internal/playback -run '^TestNaturalLongTailMatchesIsolatedSourceFrame$' -count=1 -v
```

该工程需是超过15000帧的单视频、原速、源入点0、无派生相位；对照整片0/14999/15000/尾帧/7000与对应源时间的一帧工程，排除冻结旧图。完整连续吞吐/资源使用现有cmd/preview-benchmark，并保留Source/Project/Worker指纹，不与测试/转码并行压测。

首次公开样本、实际参数与限制见[格式矩阵](../../docs/research/媒体格式兼容矩阵.md)和[本轮规格](../../docs/specs/2026-10-27-摄影素材兼容矩阵与长画布.md)。Windows实机、任意相机HDR/复杂色彩、设备声音时钟与GUI还需单独证据。
