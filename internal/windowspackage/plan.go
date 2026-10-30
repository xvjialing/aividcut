// Package windowspackage 只从明确SDK目录解析PE依赖，不执行Windows程序或搜索其它软件。
package windowspackage

import (
	"aividcut/internal/releaseaudit"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Root struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}
type Config struct {
	ID             string   `json:"id"`
	Version        string   `json:"version"`
	Arch           string   `json:"arch"`
	Worker         string   `json:"worker,omitempty"`
	FFmpeg         string   `json:"ffmpeg"`
	FFprobe        string   `json:"ffprobe"`
	Modules        []string `json:"modules"`
	Plugins        []Root   `json:"plugins"`
	DLLDirectories []string `json:"dllDirectories"`
	SystemDLLs     []string `json:"systemDlls"`
	Data           string   `json:"data"`
	Output         string   `json:"output,omitempty"`
}
type Image struct {
	Source       string `json:"source"`
	Destination  string `json:"destination"`
	SHA256       string `json:"sha256"`
	Bytes        int64  `json:"bytes"`
	Architecture string `json:"architecture"`
}
type Edge struct {
	From       string `json:"from"`
	Name       string `json:"name"`
	Delayed    bool   `json:"delayed"`
	Resolution string `json:"resolution"`
	Target     string `json:"target,omitempty"`
}
type Plan struct {
	Images          []Image  `json:"images"`
	Edges           []Edge   `json:"edges"`
	Missing         []Edge   `json:"missing"`
	WorkerMissing   bool     `json:"workerMissing"`
	ReleaseVerified bool     `json:"releaseVerified"`
	Limits          []string `json:"limits"`
}

func portable(path string) bool {
	return fs.ValidPath(path) && path != "." && !strings.ContainsAny(path, "\\:\x00")
}
func dllName(name string) bool {
	return name != "" && len(name) <= 255 && !strings.ContainsAny(name, "/\\:\x00") && strings.HasSuffix(strings.ToLower(name), ".dll")
}
func fileHash(ctx context.Context, path string) (string, int64, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > 512<<20 {
		return "", 0, errors.New("SDK文件类型或大小无效")
	}
	h := sha256.New()
	buf := make([]byte, 65536)
	var total int64
	for {
		if e = ctx.Err(); e != nil {
			return "", 0, e
		}
		n, err := f.Read(buf)
		total += int64(n)
		if total > 512<<20 {
			return "", 0, errors.New("SDK文件超过限额")
		}
		h.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", 0, err
		}
	}
	after, e := os.Stat(path)
	if e != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return "", 0, errors.New("SDK文件在读取期间变化")
	}
	return hex.EncodeToString(h.Sum(nil)), total, nil
}

func Scan(ctx context.Context, c Config) (Plan, error) {
	p := Plan{Images: []Image{}, Edges: []Edge{}, Missing: []Edge{}, WorkerMissing: c.Worker == "", Limits: []string{"普通与延迟导入静态解析，不覆盖计算名称的LoadLibrary/驱动和全部插件行为", "system_declared为操作者声明的OS基线，不是当前Windows注册表验证", "SDK来源/ABI/许可/签名和实机运行仍需独立验收"}}
	if (c.Arch != "amd64" && c.Arch != "arm64") || len(c.Modules) > 64 || len(c.Plugins) > 128 || len(c.DLLDirectories) > 16 || len(c.SystemDLLs) > 512 {
		return p, errors.New("Windows目标或输入数量无效")
	}
	index := map[string]string{}
	system := map[string]bool{}
	for _, name := range c.SystemDLLs {
		if !dllName(name) {
			return p, errors.New("系统DLL声明必须为普通名称")
		}
		system[strings.ToLower(name)] = true
	}
	for _, dir := range c.DLLDirectories {
		if !filepath.IsAbs(dir) {
			return p, errors.New("DLL目录必须明确绝对路径")
		}
		entries, e := os.ReadDir(dir)
		if e != nil {
			return p, e
		}
		if len(entries) > 8192 {
			return p, errors.New("DLL目录过大")
		}
		for _, entry := range entries {
			if entry.IsDir() || !dllName(entry.Name()) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			canonical, e := filepath.EvalSymlinks(path)
			if e != nil {
				return p, e
			}
			key := strings.ToLower(entry.Name())
			if previous, ok := index[key]; ok && previous != canonical {
				a, _, e := fileHash(ctx, previous)
				if e != nil {
					return p, e
				}
				b, _, e := fileHash(ctx, canonical)
				if e != nil || a != b {
					return p, fmt.Errorf("同名DLL来源冲突：%s", entry.Name())
				}
			}
			index[key] = canonical
		}
	}
	roots := []Root{{c.FFmpeg, "bin/ffmpeg.exe"}, {c.FFprobe, "bin/ffprobe.exe"}}
	if c.Worker != "" {
		roots = append(roots, Root{c.Worker, "bin/aividcut-mlt-worker.exe"})
	}
	for _, path := range c.Modules {
		roots = append(roots, Root{path, "mlt/" + filepath.Base(path)})
	}
	roots = append(roots, c.Plugins...)
	seen := map[string]bool{}
	var totalBytes int64
	destinations := map[string]string{}
	for len(roots) > 0 {
		if e := ctx.Err(); e != nil {
			return p, e
		}
		root := roots[0]
		roots = roots[1:]
		if !filepath.IsAbs(root.Source) || !portable(root.Destination) {
			return p, errors.New("PE输入需绝对路径，包内目标需相对路径")
		}
		canonical, e := filepath.EvalSymlinks(root.Source)
		if e != nil {
			return p, e
		}
		if seen[canonical] {
			continue
		}
		if len(p.Images) >= 1024 {
			return p, errors.New("Windows运行时超过1024映像")
		}
		key := strings.ToLower(root.Destination)
		if old, ok := destinations[key]; ok && old != canonical {
			return p, errors.New("包内文件名冲突")
		}
		destinations[key] = canonical
		image, e := releaseaudit.ReadPEImage(canonical)
		if e != nil {
			return p, e
		}
		if image.Architecture != c.Arch {
			return p, fmt.Errorf("PE架构不匹配：%s", canonical)
		}
		sha, size := image.SHA256, image.Bytes
		totalBytes += size
		if totalBytes > 2<<30 {
			return p, errors.New("PE闭包超过2GiB")
		}
		p.Images = append(p.Images, Image{canonical, root.Destination, sha, size, image.Architecture})
		seen[canonical] = true
		for _, dependency := range image.Imports {
			if !dllName(dependency.Name) {
				return p, errors.New("PE依赖含路径或非法名称")
			}
			edge := Edge{From: root.Destination, Name: dependency.Name, Delayed: dependency.Delayed}
			lower := strings.ToLower(dependency.Name)
			if strings.HasPrefix(lower, "api-ms-win-") || strings.HasPrefix(lower, "ext-ms-win-") {
				edge.Resolution = "api_set"
			} else if system[lower] {
				edge.Resolution = "system_declared"
			} else if target, ok := index[lower]; ok {
				edge.Resolution = "bundled"
				edge.Target = "bin/" + filepath.Base(target)
				roots = append(roots, Root{target, edge.Target})
			} else {
				edge.Resolution = "missing"
				p.Missing = append(p.Missing, edge)
			}
			p.Edges = append(p.Edges, edge)
		}
	}
	sort.Slice(p.Images, func(i, j int) bool { return p.Images[i].Destination < p.Images[j].Destination })
	return p, nil
}
