# 自建语音协议样本

2026-10-25 使用本机已有 FFmpeg 从 lavfi 静音/纯色输入生成，不包含录制人声、第三方人物音色或真实模型输出，也未复制/打包 FFmpeg 程序。

- silence.mp3：0.12 秒 anullsrc 输入，mono、44.1kHz、libmp3lame 128k；ffprobe 容器时长 0.156735 秒。
- clip.mp4：0.12 秒黑色 16×16、25fps，静音 16kHz，H.264/AAC；ffprobe 容器时长 0.12 秒。

样本只验证本地读取和 HTTP 协议，不证明供应商转写质量、TTS 音质或克隆效果。测试通过 go:embed 读取这些文件，不要求运行 FFmpeg。
