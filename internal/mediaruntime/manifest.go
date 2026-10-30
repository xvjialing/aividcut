// Package mediaruntime 只读取产品相邻的媒体清单，校验不代表发布者身份或许可认证。
package mediaruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const Filename = "runtime.json"
const MaxManifestBytes = 2 << 20
const MaxPayloads = 8192
const MaxFileBytes int64 = 512 << 20
const MaxTotalBytes int64 = 2 << 30

type Paths struct {
	FFmpeg     string `json:"ffmpeg"`
	FFprobe    string `json:"ffprobe"`
	Worker     string `json:"worker"`
	Repository string `json:"repository"`
	Profiles   string `json:"profiles"`
	Data       string `json:"data,omitempty"`
}
type Payload struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Bytes  int64  `json:"bytes,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Target string `json:"target,omitempty"`
}
type Manifest struct {
	SchemaVersion int       `json:"schemaVersion"`
	ID            string    `json:"id"`
	Version       string    `json:"version"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`
	Paths         Paths     `json:"paths"`
	Payloads      []Payload `json:"payloads"`
}
type Result struct {
	Present      bool
	ManifestPath string
	Fingerprint  string
	ID           string
	Version      string
	Paths        Paths
	Files        int
	Bytes        int64
}

func Candidate(executable, goos string) (string, string, error) {
	if !filepath.IsAbs(executable) {
		return "", "", errors.New("程序路径必须为绝对路径")
	}
	real, e := filepath.EvalSymlinks(executable)
	if e != nil {
		return "", "", e
	}
	dir := filepath.Dir(real)
	if goos == "darwin" && filepath.Base(dir) == "MacOS" && filepath.Base(filepath.Dir(dir)) == "Contents" {
		root := filepath.Dir(filepath.Dir(dir))
		return filepath.Join(root, "Contents", "Resources", "media", Filename), root, nil
	}
	return filepath.Join(dir, "media", Filename), dir, nil
}
func Find(ctx context.Context, executable, goos, arch string) (Result, error) {
	file, packageRoot, e := Candidate(executable, goos)
	if e != nil {
		return Result{}, e
	}
	result := Result{ManifestPath: file}
	info, e := os.Lstat(file)
	if os.IsNotExist(e) {
		return result, nil
	}
	result.Present = true
	if e != nil || !info.Mode().IsRegular() {
		return result, errors.New("随包媒体清单不是可读取的普通文件")
	}
	real, e := filepath.EvalSymlinks(file)
	if e != nil {
		return result, e
	}
	rel, e := filepath.Rel(packageRoot, real)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return result, errors.New("随包媒体目录指向产品目录之外")
	}
	loaded, e := Load(ctx, file, goos, arch)
	loaded.Present = true
	return loaded, e
}
func relative(p string) bool {
	return p != "." && p != "" && len(p) <= 4096 && fs.ValidPath(p) && !strings.ContainsAny(p, "\\:\x00")
}
func Load(ctx context.Context, file, goos, arch string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	absolute, err := filepath.Abs(file)
	if err != nil {
		return Result{}, err
	}
	file = absolute
	out := Result{Present: true, ManifestPath: file}
	if filepath.Base(file) != Filename {
		return out, errors.New("随包清单名称必须为runtime.json")
	}
	root, e := os.OpenRoot(filepath.Dir(file))
	if e != nil {
		return out, e
	}
	defer root.Close()
	directoryBefore, e := root.Stat(".")
	if e != nil {
		return out, e
	}
	before, e := root.Lstat(Filename)
	if e != nil || !before.Mode().IsRegular() || before.Size() > MaxManifestBytes {
		return out, errors.New("随包清单必须为2MiB以内的普通文件")
	}
	f, e := root.Open(Filename)
	if e != nil {
		return out, e
	}
	data, e := io.ReadAll(io.LimitReader(f, MaxManifestBytes+1))
	f.Close()
	if e != nil || len(data) > MaxManifestBytes {
		return out, errors.New("随包清单读取失败或过大")
	}
	var m Manifest
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e = d.Decode(&m); e != nil {
		return out, errors.New("随包清单格式无效")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return out, errors.New("随包清单包含多余数据")
	}
	if (m.SchemaVersion != 1 && m.SchemaVersion != 2) || (m.SchemaVersion == 1 && m.Paths.Data != "") || (m.SchemaVersion == 2 && m.Paths.Data == "") || strings.TrimSpace(m.ID) == "" || len(m.ID) > 120 || strings.TrimSpace(m.Version) == "" || len(m.Version) > 120 || m.OS != goos || m.Arch != arch || (goos != "darwin" && goos != "windows") || (arch != "arm64" && arch != "amd64") {
		return out, errors.New("随包清单版本、身份或目标平台不匹配")
	}
	if len(m.Payloads) == 0 || len(m.Payloads) > MaxPayloads {
		return out, errors.New("随包文件清单为空或超过8192项")
	}
	entries := map[string]Payload{}
	var total int64
	for _, p := range m.Payloads {
		if !relative(p.Path) || p.Path == Filename {
			return out, errors.New("随包文件路径无效")
		}
		if _, ok := entries[p.Path]; ok {
			return out, errors.New("随包文件路径重复")
		}
		entries[p.Path] = p
		if p.Kind == "file" {
			decoded, err := hex.DecodeString(p.SHA256)
			if err != nil || len(decoded) != 32 || strings.ToLower(p.SHA256) != p.SHA256 || p.Bytes < 0 || p.Bytes > MaxFileBytes || p.Target != "" {
				return out, errors.New("随包文件指纹或大小无效")
			}
			total += p.Bytes
			if total > MaxTotalBytes {
				return out, errors.New("随包内容超过2GiB")
			}
		} else if p.Kind == "symlink" {
			clean := path.Clean(path.Join(path.Dir(p.Path), p.Target))
			if p.Target == "" || strings.ContainsAny(p.Target, "\\:\x00") || path.IsAbs(p.Target) || !relative(clean) || p.Bytes != 0 || p.SHA256 != "" {
				return out, errors.New("随包链接目标无效或越界")
			}
		} else {
			return out, errors.New("随包文件类型未知")
		}
	}
	visited := map[string]bool{}
	proofs := map[string]os.FileInfo{}
	nodes := 0
	e = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		info, err := root.Lstat(filepath.FromSlash(name))
		if err != nil {
			return err
		}
		proofs[name] = info
		nodes++
		if nodes > 32768 {
			return errors.New("随包目录节点过多")
		}
		if entry.IsDir() {
			return nil
		}
		if name == Filename {
			return nil
		}
		p, ok := entries[name]
		if !ok {
			return fmt.Errorf("随包目录含未登记文件：%s", name)
		}
		visited[name] = true
		if entry.Type()&os.ModeSymlink != 0 {
			if p.Kind != "symlink" {
				return errors.New("随包链接类型与清单不符")
			}
			target, err := root.Readlink(filepath.FromSlash(name))
			if err != nil || filepath.ToSlash(target) != p.Target {
				return errors.New("随包链接目标变化")
			}
			if _, err = root.Stat(filepath.FromSlash(name)); err != nil {
				return errors.New("随包链接无法在产品媒体目录内解析")
			}
			return nil
		}
		if p.Kind != "file" || !entry.Type().IsRegular() {
			return errors.New("随包内容不是声明的普通文件")
		}
		file, err := root.Open(filepath.FromSlash(name))
		if err != nil {
			return err
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Size() != p.Bytes {
			return errors.New("随包文件大小与清单不符")
		}
		h := sha256.New()
		buffer := make([]byte, 65536)
		var read int64
		for {
			if e := ctx.Err(); e != nil {
				return e
			}
			n, err := file.Read(buffer)
			if n > 0 {
				read += int64(n)
				if read > p.Bytes {
					return errors.New("随包文件读取期间增长")
				}
				h.Write(buffer[:n])
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
		}
		after, err := root.Stat(filepath.FromSlash(name))
		if err != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) || hex.EncodeToString(h.Sum(nil)) != p.SHA256 {
			return fmt.Errorf("随包文件指纹不匹配或读取期间变化：%s", name)
		}
		return nil
	})
	if e != nil {
		return out, e
	}
	if len(visited) != len(entries) {
		return out, errors.New("随包清单含缺失文件")
	}
	for _, p := range []string{m.Paths.FFmpeg, m.Paths.FFprobe, m.Paths.Worker, m.Paths.Repository, m.Paths.Profiles} {
		if !relative(p) {
			return out, errors.New("随包工具入口路径无效")
		}
	}
	for _, p := range []string{m.Paths.FFmpeg, m.Paths.FFprobe, m.Paths.Worker} {
		info, err := root.Stat(filepath.FromSlash(p))
		if err != nil || !info.Mode().IsRegular() || (goos != "windows" && info.Mode()&0111 == 0) {
			return out, errors.New("随包工具入口不是可执行普通文件")
		}
	}
	for _, p := range []string{m.Paths.Repository, m.Paths.Profiles} {
		info, err := root.Stat(filepath.FromSlash(p))
		if err != nil || !info.IsDir() {
			return out, errors.New("随包媒体目录入口无效")
		}
		dir, err := root.Open(filepath.FromSlash(p))
		if err != nil {
			return out, err
		}
		items, err := dir.ReadDir(1)
		dir.Close()
		if err != nil || len(items) == 0 {
			return out, errors.New("随包媒体入口目录为空或不可读")
		}
	}
	if m.Paths.Data != "" {
		if !relative(m.Paths.Data) {
			return out, errors.New("MLT数据入口无效")
		}
		info, err := root.Stat(filepath.FromSlash(m.Paths.Data))
		if err != nil || !info.IsDir() {
			return out, errors.New("MLT数据入口不是目录")
		}
	}
	for name, before := range proofs {
		after, err := root.Lstat(filepath.FromSlash(name))
		if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
			return out, errors.New("核对期间随包内容或目录变化")
		}
	}
	after, e := root.Stat(Filename)
	if e != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return out, errors.New("随包清单在核对期间变化")
	}
	directoryAfter, err := os.Stat(filepath.Dir(file))
	manifestAfter, manifestErr := os.Lstat(file)
	if err != nil || manifestErr != nil || !os.SameFile(directoryBefore, directoryAfter) || !os.SameFile(before, manifestAfter) {
		return out, errors.New("随包目录或清单路径在核对期间替换")
	}
	abs := func(p string) string { return filepath.Join(filepath.Dir(file), filepath.FromSlash(p)) }
	out.Paths = Paths{FFmpeg: abs(m.Paths.FFmpeg), FFprobe: abs(m.Paths.FFprobe), Worker: abs(m.Paths.Worker), Repository: abs(m.Paths.Repository), Profiles: abs(m.Paths.Profiles)}
	if m.Paths.Data != "" {
		out.Paths.Data = abs(m.Paths.Data)
	}
	sum := sha256.Sum256(data)
	out.Fingerprint = hex.EncodeToString(sum[:])
	out.ID, out.Version, out.Files, out.Bytes = m.ID, m.Version, len(entries), total
	return out, nil
}
