// windows-media-package 静态规划或在新目录装配明确来源的Windows SDK。
package main

import (
	"aividcut/internal/windowspackage"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", "", "明确JSON配置，所有输入使用绝对路径")
	assemble := flag.Bool("assemble", false, "闭包完整后装配到新的output；默认只规划")
	flag.Parse()
	if *path == "" || flag.NArg() != 0 {
		return fmt.Errorf("需要-config")
	}
	f, e := os.Open(*path)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
		return fmt.Errorf("配置须为64KiB内普通文件")
	}
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	var c windowspackage.Config
	if e = d.Decode(&c); e != nil {
		return e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("配置含多余数据")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if *assemble {
		path, e := windowspackage.Assemble(ctx, c)
		if e != nil {
			return fmt.Errorf("装配未完成，保留路径%s：%w", path, e)
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"output": path, "releaseVerified": false})
	}
	p, e := windowspackage.Scan(ctx, c)
	if e != nil {
		return e
	}
	out := json.NewEncoder(os.Stdout)
	out.SetIndent("", "  ")
	return out.Encode(p)
}
