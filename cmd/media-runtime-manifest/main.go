// media-runtime-manifest 在新的装配目录中生成并核验完整内容清单。
package main

import (
	"aividcut/internal/mediaruntime"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	root := flag.String("root", "", "明确的新装配媒体目录，不覆盖已有runtime.json")
	specPath := flag.String("spec", "", "无payload的Manifest规格JSON")
	verify := flag.Bool("verify", false, "只核验root下已有清单，目标从规格读取")
	targetOS := flag.String("target-os", "", "只核验时明确预期OS")
	targetArch := flag.String("target-arch", "", "只核验时明确预期架构")
	flag.Parse()
	if *root == "" || flag.NArg() != 0 {
		return fmt.Errorf("需要-root")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if *verify && *specPath == "" {
		if *targetOS == "" || *targetArch == "" {
			return fmt.Errorf("核验需要-target-os与-target-arch")
		}
		result, e := mediaruntime.Load(ctx, filepath.Join(*root, mediaruntime.Filename), *targetOS, *targetArch)
		if e != nil {
			return e
		}
		return printResult(result)
	}
	if *specPath == "" {
		return fmt.Errorf("需要-root和-spec；生成只用于新的装配目录")
	}
	f, e := os.Open(*specPath)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
		return fmt.Errorf("规格必须为64KiB以内普通文件")
	}
	data, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var spec mediaruntime.Manifest
	if e = d.Decode(&spec); e != nil {
		return e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("规格含尾随数据")
	}
	var result mediaruntime.Result
	if *verify {
		result, e = mediaruntime.Load(ctx, *root+string(os.PathSeparator)+mediaruntime.Filename, spec.OS, spec.Arch)
	} else {
		result, e = mediaruntime.CreateManifest(ctx, *root, spec)
	}
	if e != nil {
		return e
	}
	return printResult(result)
}
func printResult(result mediaruntime.Result) error {
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"id": result.ID, "version": result.Version, "files": result.Files, "bytes": result.Bytes, "fingerprint": result.Fingerprint, "releaseVerified": false})
}
