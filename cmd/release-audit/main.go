// release-audit 只读核对明确配置的发行输入，报告不构成发行许可批准。
package main

import (
	"aividcut/internal/releaseaudit"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
)

func main() { os.Exit(run()) }
func run() int {
	config := flag.String("config", "", "显式JSON配置；路径相对配置文件")
	probe := flag.Bool("probe-ffmpeg", false, "明确执行同平台FFmpeg/FFprobe的固定-version（最多3秒/64KiB）")
	check := flag.Bool("check", false, "存在blocker或review时返回1；报告不等于发行通过")
	flag.Parse()
	if *config == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "需要-config配置文件，报告JSON写入stdout；不要覆盖被检查输入")
		return 2
	}
	c, err := releaseaudit.ReadConfig(*config)
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置读取失败：", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r, err := releaseaudit.Audit(ctx, c, releaseaudit.Options{ProbeFFmpeg: *probe})
	if err != nil {
		fmt.Fprintln(os.Stderr, "检查未完成：", err)
		return 2
	}
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	if err = e.Encode(r); err != nil {
		fmt.Fprintln(os.Stderr, "输出失败：", err)
		return 2
	}
	if *check && (r.Blockers > 0 || r.Reviews > 0) {
		return 1
	}
	return 0
}
