package mediaruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CreateManifest 只向尚无清单的显式装配目录写入；不覆盖已安装运行时。
func CreateManifest(ctx context.Context, directory string, spec Manifest) (Result, error) {
	var empty Result
	if len(spec.Payloads) != 0 {
		return empty, errors.New("生成规格不能预填payload")
	}
	if (spec.SchemaVersion != 1 && spec.SchemaVersion != 2) || strings.TrimSpace(spec.ID) == "" || strings.TrimSpace(spec.Version) == "" || (spec.OS != "darwin" && spec.OS != "windows") || (spec.Arch != "arm64" && spec.Arch != "amd64") {
		return empty, errors.New("生成规格身份或目标无效")
	}
	abs, e := filepath.Abs(directory)
	if e != nil {
		return empty, e
	}
	root, e := os.OpenRoot(abs)
	if e != nil {
		return empty, e
	}
	defer root.Close()
	if _, e = root.Lstat(Filename); !os.IsNotExist(e) {
		return empty, errors.New("目标已有清单或无法确认状态，未覆盖")
	}
	nodes := 0
	var total int64
	e = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		nodes++
		if nodes > 32768 {
			return errors.New("目录节点超过生成边界")
		}
		if entry.IsDir() {
			return nil
		}
		if len(spec.Payloads) >= MaxPayloads {
			return errors.New("文件数量超过生成边界")
		}
		if !relative(name) {
			return errors.New("装配目录含不可移植路径")
		}
		p := Payload{Path: name}
		info, e := root.Lstat(filepath.FromSlash(name))
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			p.Kind = "symlink"
			target, e := root.Readlink(filepath.FromSlash(name))
			if e != nil {
				return e
			}
			p.Target = filepath.ToSlash(target)
			if _, e = root.Stat(filepath.FromSlash(name)); e != nil {
				return errors.New("链接无法在装配目录内解析")
			}
		} else {
			if !info.Mode().IsRegular() || info.Size() > MaxFileBytes {
				return errors.New("装配内容类型或大小无效")
			}
			p.Kind = "file"
			p.Bytes = info.Size()
			total += p.Bytes
			if total > MaxTotalBytes {
				return errors.New("装配内容超过2GiB")
			}
			f, e := root.Open(filepath.FromSlash(name))
			if e != nil {
				return e
			}
			h := sha256.New()
			buf := make([]byte, 65536)
			var read int64
			for {
				if e = ctx.Err(); e != nil {
					f.Close()
					return e
				}
				n, err := f.Read(buf)
				read += int64(n)
				if read > p.Bytes {
					f.Close()
					return errors.New("生成期间文件增长")
				}
				h.Write(buf[:n])
				if err == io.EOF {
					break
				}
				if err != nil {
					f.Close()
					return err
				}
			}
			f.Close()
			p.SHA256 = hex.EncodeToString(h.Sum(nil))
		}
		spec.Payloads = append(spec.Payloads, p)
		return nil
	})
	if e != nil {
		return empty, e
	}
	data, e := json.MarshalIndent(spec, "", "  ")
	if e != nil || len(data)+1 > MaxManifestBytes {
		return empty, errors.New("生成清单过大或无法编码")
	}
	f, e := root.OpenFile(Filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if e != nil {
		return empty, e
	}
	identity, e := f.Stat()
	if e != nil {
		f.Close()
		return empty, e
	}
	cleanup := func() {
		current, err := root.Lstat(Filename)
		if err == nil && os.SameFile(identity, current) {
			root.Remove(Filename)
		}
	}
	_, e = f.Write(append(data, '\n'))
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		cleanup()
		return empty, e
	}
	if closeErr != nil {
		cleanup()
		return empty, closeErr
	}
	result, e := Load(ctx, filepath.Join(abs, Filename), spec.OS, spec.Arch)
	if e != nil {
		cleanup()
		return empty, e
	}
	return result, nil
}
