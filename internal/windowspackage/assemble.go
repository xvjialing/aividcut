package windowspackage

import (
	"aividcut/internal/mediaruntime"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Assemble 仅在闭包完整且worker存在后创建新目录，失败保留stage供核对。
func Assemble(ctx context.Context, c Config) (string, error) {
	if c.Worker == "" || c.Output == "" || !filepath.IsAbs(c.Output) || c.ID == "" || c.Version == "" {
		return "", errors.New("装配需要worker、身份和明确新输出目录")
	}
	if _, e := os.Lstat(c.Output); !os.IsNotExist(e) {
		return "", errors.New("输出已存在或无法确认，未覆盖")
	}
	p, e := Scan(ctx, c)
	if e != nil {
		return "", e
	}
	if len(p.Missing) > 0 {
		return "", errors.New("DLL闭包不完整，请先查看-plan报告")
	}
	if !filepath.IsAbs(c.Data) {
		return "", errors.New("MLT数据需要绝对目录")
	}
	dataCanonical, e := filepath.EvalSymlinks(c.Data)
	if e != nil {
		return "", e
	}
	parentCanonical, e := filepath.EvalSymlinks(filepath.Dir(c.Output))
	if e != nil {
		return "", e
	}
	relative, e := filepath.Rel(dataCanonical, parentCanonical)
	if e == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)) {
		return "", errors.New("输出不能位于MLT输入数据目录内")
	}
	if _, e = os.Stat(filepath.Join(c.Data, "core", "loader.dict")); e != nil {
		return "", errors.New("MLT数据缺少core/loader.dict")
	}
	stage, e := os.MkdirTemp(filepath.Dir(c.Output), ".windows-media-stage-")
	if e != nil {
		return "", e
	}
	root := filepath.Join(stage, "media")
	if e = os.Mkdir(root, 0700); e != nil {
		return stage, e
	}
	copyFile := func(source, destination string, mode fs.FileMode) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := os.MkdirAll(filepath.Dir(destination), 0700); e != nil {
			return e
		}
		in, e := os.Open(source)
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
		if e != nil {
			return e
		}
		_, e = io.Copy(out, io.LimitReader(in, 512<<20+1))
		closeErr := out.Close()
		if e != nil {
			return e
		}
		return closeErr
	}
	for _, image := range p.Images {
		destination := filepath.Join(root, filepath.FromSlash(image.Destination))
		if e = copyFile(image.Source, destination, 0755); e != nil {
			return stage, e
		}
		sha, size, err := fileHash(ctx, destination)
		if err != nil || sha != image.SHA256 || size != image.Bytes {
			return stage, errors.New("装配期间PE文件变化")
		}
	}
	dataRoot, e := os.OpenRoot(c.Data)
	if e != nil {
		return stage, e
	}
	defer dataRoot.Close()
	var dataBytes int64
	for _, image := range p.Images {
		dataBytes += image.Bytes
	}
	nodes := 0
	e = fs.WalkDir(dataRoot.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		nodes++
		if nodes > 32768 {
			return errors.New("MLT数据目录节点过多")
		}
		destination := filepath.Join(root, "data", filepath.FromSlash(name))
		if entry.IsDir() {
			return os.MkdirAll(destination, 0700)
		}
		if !portable(name) || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("MLT数据只接受普通封闭文件树")
		}
		info, e := entry.Info()
		if e != nil || !info.Mode().IsRegular() || info.Size() > 512<<20 {
			return errors.New("MLT数据类型或大小无效")
		}
		dataBytes += info.Size()
		if dataBytes > 2<<30 {
			return errors.New("MLT数据过大")
		}
		return copyFile(filepath.Join(c.Data, filepath.FromSlash(name)), destination, 0644)
	})
	if e != nil {
		return stage, e
	}
	if e = os.WriteFile(filepath.Join(root, "bin", "qt.conf"), []byte("[Paths]\nPrefix = ..\nPlugins = plugins\n"), 0644); e != nil {
		return stage, e
	}
	_, e = mediaruntime.CreateManifest(ctx, root, mediaruntime.Manifest{SchemaVersion: 2, ID: c.ID, Version: c.Version, OS: "windows", Arch: c.Arch, Paths: mediaruntime.Paths{FFmpeg: "bin/ffmpeg.exe", FFprobe: "bin/ffprobe.exe", Worker: "bin/aividcut-mlt-worker.exe", Repository: "mlt", Profiles: "data/profiles", Data: "data"}})
	if e != nil {
		return stage, e
	}
	if e = os.Rename(stage, c.Output); e != nil {
		return stage, e
	}
	return c.Output, nil
}
