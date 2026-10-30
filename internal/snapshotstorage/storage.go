// Package snapshotstorage 提供快照资料的有界检查、可还原移动和已知文件清理。
package snapshotstorage

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Label struct {
	Name, ProjectID string
	Revision        int64
	Time            time.Time
	Readable        bool
}
type Proof struct {
	Root, Filename, View string
	Directory            bool
	Info                 os.FileInfo
	Children             map[string]os.FileInfo
}
type Item struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	ProjectID string    `json:"projectId,omitempty"`
	Revision  int64     `json:"revision"`
	Time      time.Time `json:"time"`
	Bytes     int64     `json:"bytes"`
	Readable  bool      `json:"readable"`
	CanPurge  bool      `json:"canPurge"`
	Reason    string    `json:"reason,omitempty"`
	Locked    bool      `json:"locked"`
	Token     string    `json:"token"`
	Proof     Proof     `json:"-"`
}
type Page struct {
	Items      []Item `json:"items"`
	Total      int    `json:"total"`
	TotalBytes int64  `json:"totalBytes"`
	Offset     int    `json:"offset"`
	Limit      int    `json:"limit"`
}

func ValidView(view string) bool { return view == "active" || view == "trash" }
func viewPath(root, view string) string {
	if view == "trash" {
		return filepath.Join(root, ".trash")
	}
	return root
}
func ensureDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("资料目录不是普通目录")
	}
	return nil
}

func inspect(root, filename, view string, directory bool) (Proof, int64, bool, string, error) {
	base := viewPath(root, view)
	if err := ensureDirectory(root); err != nil {
		return Proof{}, 0, false, "", err
	}
	if err := ensureDirectory(base); err != nil {
		return Proof{}, 0, false, "", err
	}
	info, err := os.Lstat(filepath.Join(base, filename))
	if err != nil {
		return Proof{}, 0, false, "", err
	}
	p := Proof{Root: root, Filename: filename, View: view, Directory: directory, Info: info, Children: map[string]os.FileInfo{}}
	if info.Mode()&os.ModeSymlink != 0 || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return p, 0, false, "不自动处理链接或特殊文件", nil
	}
	if !directory {
		return p, info.Size(), true, "", nil
	}
	f, err := os.Open(filepath.Join(base, filename))
	if err != nil {
		return Proof{}, 0, false, "", err
	}
	defer f.Close()
	rows, err := f.ReadDir(17)
	if err != nil && err != io.EOF {
		return p, 0, false, "无法读取目录", nil
	}
	if len(rows) > 16 {
		return p, 0, false, "目录包含额外内容，请人工核对", nil
	}
	var total int64
	canPurge := true
	reason := ""
	for _, row := range rows {
		child, err := row.Info()
		if err != nil {
			return Proof{}, 0, false, "", err
		}
		p.Children[row.Name()] = child
		if child.Mode().IsRegular() {
			if child.Size() < 0 || child.Size() > math.MaxInt64-total {
				return Proof{}, 0, false, "", errors.New("资料大小超过支持范围")
			}
			total += child.Size()
		}
		if (row.Name() != "metadata.json" && row.Name() != "project.aividcut") || !child.Mode().IsRegular() {
			canPurge = false
			reason = "包含额外文件、目录或链接，不自动永久清理"
		}
	}
	return p, total, canPurge, reason, nil
}

// Catalog不解析全部工程正文，未知内容仅标记，不自动删除。
func Catalog(root, view string, directory bool, valid func(string) bool, label func(string, string) (Label, error), max int) ([]Item, error) {
	if !ValidView(view) {
		return nil, errors.New("资料位置无效")
	}
	if err := ensureDirectory(root); errors.Is(err, os.ErrNotExist) {
		return []Item{}, nil
	} else if err != nil {
		return nil, err
	}
	base := viewPath(root, view)
	if err := ensureDirectory(base); errors.Is(err, os.ErrNotExist) {
		return []Item{}, nil
	} else if err != nil {
		return nil, err
	}
	f, err := os.Open(base)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	values := []Item{}
	scanned := 0
	for {
		rows, readErr := f.ReadDir(64)
		for _, row := range rows {
			scanned++
			if scanned > 4096 {
				return nil, errors.New("资料目录超出有界扫描范围")
			}
			id := row.Name()
			if !directory {
				if !strings.HasSuffix(id, ".json") {
					continue
				}
				id = strings.TrimSuffix(id, ".json")
			}
			if !valid(id) {
				continue
			}
			p, size, canPurge, reason, err := inspect(root, row.Name(), view, directory)
			if err != nil {
				return nil, err
			}
			item := Item{ID: id, Name: "不可读资料 · " + id, Time: p.Info.ModTime(), Bytes: size, CanPurge: canPurge, Reason: reason, Proof: p}
			item.Locked = p.Info.Mode()&os.ModeSymlink != 0 || (!directory && !p.Info.Mode().IsRegular()) || (directory && !p.Info.IsDir())
			if v, err := label(base, id); err == nil && v.Readable {
				item.Name = v.Name
				item.ProjectID = v.ProjectID
				item.Revision = v.Revision
				item.Time = v.Time
				item.Readable = true
			}
			values = append(values, item)
			if len(values) > max {
				return nil, errors.New("该位置的资料数量超过当前支持范围")
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return nil, readErr
			}
			break
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Time.Equal(values[j].Time) {
			return values[i].ID > values[j].ID
		}
		return values[i].Time.After(values[j].Time)
	})
	return values, nil
}

func same(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && a.Mode() == b.Mode()
}
func protected(source string, paths []string) bool {
	if resolved, err := filepath.EvalSymlinks(source); err == nil {
		source = resolved
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		rel, err := filepath.Rel(source, path)
		if err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))) {
			return true
		}
	}
	return false
}
func Conflicts(p Proof, paths []string) bool {
	return protected(filepath.Join(viewPath(p.Root, p.View), p.Filename), paths)
}

func Apply(p Proof, action string, paths []string) error {
	if !ValidView(p.View) || p.Info == nil {
		return errors.New("资料身份无效，请刷新")
	}
	current, _, canPurge, _, err := inspect(p.Root, p.Filename, p.View, p.Directory)
	if err != nil {
		return err
	}
	if !same(p.Info, current.Info) || len(p.Children) != len(current.Children) {
		return errors.New("资料已变化，请刷新后重新核对")
	}
	for name, info := range p.Children {
		if !same(info, current.Children[name]) {
			return errors.New("资料文件已变化，请刷新后重新核对")
		}
	}
	source := filepath.Join(viewPath(p.Root, p.View), p.Filename)
	if protected(source, paths) {
		return errors.New("该资料正作为当前工程文件或素材使用，不能移动或清理")
	}
	if current.Info.Mode()&os.ModeSymlink != 0 || (!p.Directory && !current.Info.Mode().IsRegular()) || (p.Directory && !current.Info.IsDir()) {
		return errors.New("不自动处理链接或特殊文件")
	}
	root, err := os.OpenRoot(p.Root)
	if err != nil {
		return err
	}
	defer root.Close()
	relative := p.Filename
	if p.View == "trash" {
		relative = filepath.Join(".trash", p.Filename)
	}
	switch action {
	case "trash", "restore":
		if (action == "trash" && p.View != "active") || (action == "restore" && p.View != "trash") {
			return errors.New("操作与资料位置不匹配")
		}
		destination := p.Filename
		if action == "trash" {
			if err := root.MkdirAll(".trash", 0700); err != nil {
				return err
			}
			if err := ensureDirectory(filepath.Join(p.Root, ".trash")); err != nil {
				return err
			}
			destination = filepath.Join(".trash", p.Filename)
		}
		if _, err := root.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
			return errors.New("还原或回收目标已被占用，未覆盖")
		}
		return root.Rename(relative, destination)
	case "purge":
		if p.View != "trash" {
			return errors.New("只能永久删除回收站中的资料")
		}
		if !canPurge {
			return errors.New("包含未知内容，不执行永久删除")
		}
		if p.Directory {
			for _, name := range []string{"metadata.json", "project.aividcut"} {
				if _, ok := current.Children[name]; ok {
					if err := root.Remove(filepath.Join(relative, name)); err != nil {
						return fmt.Errorf("清理未完整完成，请刷新检查保留文件：%w", err)
					}
				}
			}
		}
		return root.Remove(relative)
	default:
		return errors.New("未知资料操作")
	}
}
