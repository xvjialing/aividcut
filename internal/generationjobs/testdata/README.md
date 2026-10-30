# 本地视频夹具

`seedance-result.mp4` 为本工程通过本机 FFmpeg 生成的纯蓝色视频，不含第三方素材、账户输出或声音。1280×720、24fps、6帧、0.25秒、H.264/yuv420p；用于验证异步任务的原ID恢复、下载字节和配方保存，不代表供应商生成质量或5秒请求已经真实执行。

生成命令：

```sh
ffmpeg -hide_banner -loglevel error -f lavfi -i color=c=blue:s=1280x720:r=24:d=0.25 -an -c:v libx264 -pix_fmt yuv420p -map_metadata -1 -movflags +faststart -y seedance-result.mp4
```

文件内容检测之外，阶段验收另用FFmpeg完整解码；测试运行自身不依赖外部FFmpeg。
