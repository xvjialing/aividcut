// Package projectcollect 将完整工程和登记素材收集为可迁移的目录副本，不修改原工程。
package projectcollect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"aividcut/internal/editor"
	"aividcut/internal/projectstore"
)

const BufferBytes = 256 << 10

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type Manifest struct {
	FormatVersion       int       `json:"formatVersion"`
	ProjectID           string    `json:"projectId"`
	ProjectRevision     int64     `json:"projectRevision"`
	CreatedAt           time.Time `json:"createdAt"`
	AssetCount          int       `json:"assetCount"`
	Files               []File    `json:"files"`
	ProjectSHA256       string    `json:"projectSha256"`
	UnprovenSourceCount int       `json:"unprovenSourceCount"`
}
type Summary struct {
	AssetCount          int   `json:"assetCount"`
	SourceBytes         int64 `json:"sourceBytes"`
	UnprovenSourceCount int   `json:"unprovenSourceCount"`
}

// Inspect 只读文件元信息，不复制或解码媒体；源内容在实际任务中再次核对。
func Inspect(ctx context.Context, project editor.Project) (Summary, error) {
	files, total, err := plan(ctx, project)
	if err != nil {
		return Summary{}, err
	}
	value := Summary{AssetCount: len(files), SourceBytes: total}
	for _, file := range files {
		if file.expected == "" {
			value.UnprovenSourceCount++
		}
	}
	return value, nil
}

type planned struct {
	asset    editor.Asset
	info     os.FileInfo
	expected string
}

var extensionPattern = regexp.MustCompile(`^\.[a-z0-9]{1,12}$`)

// FolderName 采用Windows与macOS共同可用的单段名称，不允许路径、设备名或末尾点。
func FolderName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 || name == "." || name == ".." || strings.HasSuffix(name, ".") {
		return "", errors.New("副本名称需为1到80个字符，且不能以点结尾")
	}
	for _, c := range name {
		if c < 32 || strings.ContainsRune(`/\:*?"<>|`, c) {
			return "", errors.New("副本名称不能包含路径分隔符或系统保留字符")
		}
	}
	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9') {
		return "", errors.New("副本名称是系统保留名称，请换一个名称")
	}
	return name, nil
}

func Destination(parent, name string) (string, string, error) {
	clean, err := FolderName(name)
	if err != nil {
		return "", "", err
	}
	if !filepath.IsAbs(parent) {
		return "", "", errors.New("请选择完整的上级文件夹路径")
	}
	parent, err = filepath.EvalSymlinks(filepath.Clean(parent))
	if err != nil {
		return "", "", errors.New("上级文件夹不可读取")
	}
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return "", "", errors.New("上级路径不是可用文件夹")
	}
	if _, err := os.Lstat(filepath.Join(parent, clean)); !errors.Is(err, os.ErrNotExist) {
		return "", "", errors.New("目标已存在或不可确认，请换一个新的副本名称")
	}
	return parent, clean, nil
}

func plan(ctx context.Context, project editor.Project) ([]planned, int64, error) {
	if err := editor.ValidateProject(project); err != nil {
		return nil, 0, err
	}
	files := make([]planned, 0, len(project.Assets))
	var total int64
	for _, a := range project.Assets {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		if !filepath.IsAbs(a.Path) {
			return nil, 0, fmt.Errorf("素材 %s 没有完整本机路径，请重新定位", a.Name)
		}
		info, err := os.Stat(a.Path)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return nil, 0, fmt.Errorf("素材 %s 缺失或不是非空普通文件，请先重新定位", a.Name)
		}
		if info.Size() > math.MaxInt64-total {
			return nil, 0, errors.New("素材总大小超过当前支持范围")
		}
		total += info.Size()
		expected := ""
		if a.Fingerprint != "" {
			raw := strings.TrimPrefix(a.Fingerprint, "sha256:")
			decoded, err := hex.DecodeString(raw)
			if !strings.HasPrefix(a.Fingerprint, "sha256:") || err != nil || len(decoded) != sha256.Size {
				return nil, 0, fmt.Errorf("素材 %s 的原始指纹无法核对，请重新定位或导入", a.Name)
			}
			expected = strings.ToLower(raw)
		}
		files = append(files, planned{asset: a, info: info, expected: expected})
	}
	return files, total, nil
}

func sameSource(a, b os.FileInfo) bool {
	return b != nil && b.Mode().IsRegular() && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func writeSynced(root *os.Root, name string, contents []byte) error {
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(contents)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	return errors.Join(writeErr, f.Close())
}

func copyFile(ctx context.Context, root *os.Root, item planned, temp string, buffer []byte, onBytes func(int)) (File, error) {
	in, err := os.Open(item.asset.Path)
	if err != nil {
		return File{}, err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil || !sameSource(item.info, opened) {
		return File{}, errors.New("源文件在收集开始前已变化")
	}
	out, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return File{}, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = out.Close()
		}
	}()
	hash := sha256.New()
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return File{}, err
		}
		n, readErr := in.Read(buffer)
		if n > 0 {
			if int64(n) > item.info.Size()-size {
				return File{}, errors.New("源文件在复制期间增长")
			}
			written, err := out.Write(buffer[:n])
			if err != nil {
				return File{}, err
			}
			if written != n {
				return File{}, io.ErrShortWrite
			}
			_, _ = hash.Write(buffer[:n])
			size += int64(n)
			onBytes(n)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return File{}, readErr
		}
	}
	current, err := in.Stat()
	if err != nil || !sameSource(item.info, current) || size != item.info.Size() {
		return File{}, errors.New("源文件在复制期间已变化")
	}
	pathInfo, err := os.Stat(item.asset.Path)
	if err != nil || !sameSource(item.info, pathInfo) {
		return File{}, errors.New("源文件路径在复制期间已变化")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if item.expected != "" && digest != item.expected {
		return File{}, errors.New("源文件内容与工程登记指纹不同")
	}
	if item.expected == "" {
		// 旧素材没有内容证明时，再读一次源文件，避免仅靠粗粒度时间戳接受在途改写。
		if _, err := in.Seek(0, io.SeekStart); err != nil {
			return File{}, err
		}
		proof := sha256.New()
		var checked int64
		for {
			if err := ctx.Err(); err != nil {
				return File{}, err
			}
			n, readErr := in.Read(buffer)
			if n > 0 {
				checked += int64(n)
				if checked > item.info.Size() {
					return File{}, errors.New("无原始指纹的源文件在复核期间增长")
				}
				_, _ = proof.Write(buffer[:n])
				onBytes(n)
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return File{}, readErr
			}
		}
		if checked != size || hex.EncodeToString(proof.Sum(nil)) != digest {
			return File{}, errors.New("无原始指纹的源文件在复制期间改动，未保留混合内容")
		}
		finalInfo, err := in.Stat()
		if err != nil || !sameSource(item.info, finalInfo) {
			return File{}, errors.New("源文件在复核期间已变化")
		}
		finalPathInfo, err := os.Stat(item.asset.Path)
		if err != nil || !sameSource(item.info, finalPathInfo) {
			return File{}, errors.New("源文件路径在复核期间已变化")
		}
	}
	if err := out.Sync(); err != nil {
		return File{}, err
	}
	err = out.Close()
	closed = true
	if err != nil {
		return File{}, err
	}
	ext := strings.ToLower(filepath.Ext(item.asset.Path))
	if !extensionPattern.MatchString(ext) {
		ext = ".media"
	}
	if item.asset.Kind == "lut" {
		ext = ".cube"
	}
	return File{Path: "media/" + digest + ext, SHA256: digest, Bytes: size}, nil
}

// Collect 固定输入快照。project.aividcut最后提交，成功返回之后取消不撤销该副本。
func Collect(ctx context.Context, project editor.Project, parent, name string, progress func(float64, string)) (result string, resultErr error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	parent, name, err := Destination(parent, name)
	if err != nil {
		return "", err
	}
	files, total, err := plan(ctx, project)
	if err != nil {
		return "", err
	}
	workBytes := total
	for _, item := range files {
		if item.expected == "" {
			if item.info.Size() > math.MaxInt64-workBytes {
				return "", errors.New("文件核对工作量超过当前支持范围")
			}
			workBytes += item.info.Size()
		}
	}
	parentRoot, err := os.OpenRoot(parent)
	if err != nil {
		return "", err
	}
	defer parentRoot.Close()
	parentInfo, err := parentRoot.Stat(".")
	if err != nil {
		return "", err
	}
	if err := parentRoot.Mkdir(name, 0700); err != nil {
		return "", errors.New("无法创建新副本目录；目标可能已被占用")
	}
	owned, err := parentRoot.Lstat(name)
	if err != nil {
		return "", err
	}
	var root *os.Root
	committed := false
	defer func() {
		if root != nil {
			err := root.Close()
			if !committed {
				resultErr = errors.Join(resultErr, err)
			}
		}
		if !committed {
			if current, err := parentRoot.Lstat(name); err == nil && current.IsDir() && current.Mode()&os.ModeSymlink == 0 && os.SameFile(owned, current) {
				resultErr = errors.Join(resultErr, parentRoot.RemoveAll(name))
			}
		}
	}()
	root, err = parentRoot.OpenRoot(name)
	if err != nil {
		return "", err
	}
	rootInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(owned, rootInfo) {
		return "", errors.New("副本目录在创建期间已变化")
	}
	if err := root.Mkdir("media", 0700); err != nil {
		return "", err
	}
	portable := editor.CloneProject(project)
	manifest := Manifest{FormatVersion: 1, ProjectID: project.ID, ProjectRevision: project.Revision, CreatedAt: time.Now().UTC(), AssetCount: len(files), Files: []File{}}
	seen := map[string]bool{}
	buffer := make([]byte, BufferBytes)
	var consumed int64
	if progress == nil {
		progress = func(float64, string) {}
	}
	for index, item := range files {
		temp := "media/." + editor.NewID("copy")
		copy, err := copyFile(ctx, root, item, temp, buffer, func(n int) {
			consumed += int64(n)
			if workBytes > 0 {
				progress(float64(consumed)/float64(workBytes), fmt.Sprintf("核对并收集素材 %d/%d", index+1, len(files)))
			}
		})
		if err != nil {
			return "", fmt.Errorf("收集 %s 失败：%w", item.asset.Name, err)
		}
		if seen[copy.Path] {
			if err := root.Remove(temp); err != nil {
				return "", err
			}
		} else {
			if err := root.Rename(temp, filepath.FromSlash(copy.Path)); err != nil {
				return "", err
			}
			seen[copy.Path] = true
			manifest.Files = append(manifest.Files, copy)
		}
		if item.expected == "" {
			manifest.UnprovenSourceCount++
		}
		portable.Assets[index].Path = copy.Path
		portable.Assets[index].Missing = false
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	encoded, err := projectstore.Encode(portable)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	manifest.ProjectSHA256 = hex.EncodeToString(sum[:])
	meta, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	if err := writeSynced(root, "manifest.json", meta); err != nil {
		return "", err
	}
	if err := writeSynced(root, ".project.pending", encoded); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	currentParent, err := os.Stat(parent)
	if err != nil || !os.SameFile(parentInfo, currentParent) {
		return "", errors.New("上级目录位置已变化，未提交工程文件")
	}
	current, err := parentRoot.Lstat(name)
	if err != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(owned, current) {
		return "", errors.New("副本目录位置已变化，未提交工程文件")
	}
	if _, err := root.Lstat("project.aividcut"); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("副本工程文件已被占用，未覆盖")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := root.Rename(".project.pending", "project.aividcut"); err != nil {
		return "", err
	}
	committed = true
	progress(1, "素材及工程副本已收集完成")
	return filepath.Join(parent, name, "project.aividcut"), nil
}
