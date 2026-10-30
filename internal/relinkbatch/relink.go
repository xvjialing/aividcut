// Package relinkbatch 按明确的新旧目录映射核对同内容素材，不搜索其它目录或改写文件。
package relinkbatch

import (
	"aividcut/internal/editor"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const MaxAssets = 256
const MaxBytes int64 = 64 << 30

var fingerprintPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type Inventory struct {
	MissingCount     int    `json:"missingCount"`
	AvailableCount   int    `json:"availableCount"`
	SuggestedOldRoot string `json:"suggestedOldRoot"`
}
type Entry struct {
	AssetID       string `json:"assetId"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	OriginalPath  string `json:"originalPath"`
	CandidatePath string `json:"candidatePath"`
	Status        string `json:"status"`
	Reason        string `json:"reason"`
}
type Summary struct {
	OldRoot   string  `json:"oldRoot"`
	NewRoot   string  `json:"newRoot"`
	Entries   []Entry `json:"entries"`
	Matched   int     `json:"matched"`
	Outside   int     `json:"outside"`
	Available int     `json:"available"`
	ReadBytes int64   `json:"readBytes"`
}
type match struct {
	id, path, fingerprint string
	info                  os.FileInfo
}
type Plan struct {
	Summary  Summary
	root     string
	rootInfo os.FileInfo
	matches  []match
}

func available(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}
func within(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	return rel, err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func Inspect(ctx context.Context, p editor.Project) (Inventory, error) {
	out := Inventory{}
	for _, a := range p.Assets {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if available(a.Path) {
			out.AvailableCount++
			continue
		}
		out.MissingCount++
		if !filepath.IsAbs(a.Path) {
			continue
		}
		parent := filepath.Dir(a.Path)
		if out.SuggestedOldRoot == "" {
			out.SuggestedOldRoot = parent
			continue
		}
		for {
			if parent == out.SuggestedOldRoot {
				break
			}
			if _, ok := within(out.SuggestedOldRoot, a.Path); ok {
				break
			}
			next := filepath.Dir(out.SuggestedOldRoot)
			if next == out.SuggestedOldRoot {
				break
			}
			out.SuggestedOldRoot = next
		}
	}
	return out, nil
}

func Prepare(ctx context.Context, p editor.Project, oldRoot, newRoot string) (*Plan, error) {
	if !filepath.IsAbs(oldRoot) || !filepath.IsAbs(newRoot) {
		return nil, errors.New("请输入新旧文件夹的完整路径")
	}
	oldRoot = filepath.Clean(oldRoot)
	newRoot, err := filepath.EvalSymlinks(newRoot)
	if err != nil {
		return nil, errors.New("当前文件夹不可读取，请重新选择")
	}
	rootInfo, err := os.Stat(newRoot)
	if err != nil || !rootInfo.IsDir() {
		return nil, errors.New("当前位置需要是已存在的文件夹")
	}
	out := &Plan{root: newRoot, rootInfo: rootInfo, Summary: Summary{OldRoot: oldRoot, NewRoot: newRoot, Entries: []Entry{}}}
	type candidate struct {
		asset    editor.Asset
		relative string
	}
	var candidates []candidate
	for _, a := range p.Assets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if available(a.Path) {
			out.Summary.Available++
			continue
		}
		rel, ok := within(oldRoot, a.Path)
		if !ok {
			out.Summary.Outside++
			continue
		}
		candidates = append(candidates, candidate{a, rel})
		if len(candidates) > MaxAssets {
			return nil, errors.New("此目录内离线素材超过256个，请选择更小的旧目录分批修复")
		}
	}
	cache := map[string]match{}
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		a := c.asset
		entry := Entry{AssetID: a.ID, Name: a.Name, Kind: a.Kind, OriginalPath: a.Path, CandidatePath: filepath.Join(newRoot, c.relative)}
		if !fingerprintPattern.MatchString(a.Fingerprint) {
			entry.Status, entry.Reason = "unproven", "原素材没有可核对的SHA256，请使用单个素材重新定位"
		} else {
			canonical, err := filepath.EvalSymlinks(entry.CandidatePath)
			if _, ok := within(newRoot, canonical); err != nil || !ok {
				entry.Status, entry.Reason = "missing", "对应文件不存在或越出当前文件夹"
			} else {
				proof, ok := cache[canonical]
				if !ok {
					var bytes int64
					proof, bytes, err = read(ctx, newRoot, canonical, MaxBytes-out.Summary.ReadBytes)
					out.Summary.ReadBytes += bytes
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					if errors.Is(err, errBudget) {
						return nil, err
					}
					if err == nil {
						cache[canonical] = proof
					}
				}
				if err != nil {
					entry.Status, entry.Reason = "unreadable", "对应文件不可稳定读取，请检查权限或稍后重试"
				} else if proof.fingerprint != a.Fingerprint {
					entry.Status, entry.Reason = "different", "文件内容与原素材不同，未作为替代文件"
				} else {
					entry.Status, entry.Reason, entry.CandidatePath = "matched", "内容一致，可恢复链接", canonical
					proof.id = a.ID
					out.matches = append(out.matches, proof)
					out.Summary.Matched++
				}
			}
		}
		out.Summary.Entries = append(out.Summary.Entries, entry)
	}
	return out, nil
}

var errBudget = errors.New("核对读取超过64GiB预算，请选择较小目录分批处理")

func same(before, after os.FileInfo) bool {
	return before != nil && after != nil && os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}
func read(ctx context.Context, root, path string, remaining int64) (match, int64, error) {
	rel, ok := within(root, path)
	if !ok {
		return match{}, 0, errors.New("文件越出所选目录")
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return match{}, 0, err
	}
	defer dir.Close()
	f, err := dir.Open(rel)
	if err != nil {
		return match{}, 0, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 {
		return match{}, 0, errors.New("需要非空普通文件")
	}
	if before.Size() > remaining {
		return match{}, 0, errBudget
	}
	h := sha256.New()
	buffer := make([]byte, 128<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return match{}, total, err
		}
		n, e := f.Read(buffer)
		total += int64(n)
		if total > remaining {
			return match{}, total, errBudget
		}
		if n > 0 {
			h.Write(buffer[:n])
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return match{}, total, e
		}
	}
	after, err := os.Stat(path)
	if err != nil || !same(before, after) || total != before.Size() {
		return match{}, total, errors.New("读取期间文件发生变化")
	}
	return match{path: path, fingerprint: "sha256:" + hex.EncodeToString(h.Sum(nil)), info: after}, total, nil
}

func (p *Plan) CheckFiles() error {
	info, err := os.Stat(p.root)
	if err != nil || !os.SameFile(p.rootInfo, info) {
		return errors.New("当前文件夹已变化，请重新核对")
	}
	for _, m := range p.matches {
		info, err := os.Stat(m.path)
		if err != nil || !same(m.info, info) {
			return errors.New("候选文件已变化，请重新核对")
		}
	}
	return nil
}
func (p *Plan) Verify(ctx context.Context) error {
	if err := p.CheckFiles(); err != nil {
		return err
	}
	seen := map[string]bool{}
	var total int64
	for _, m := range p.matches {
		if seen[m.path] {
			continue
		}
		seen[m.path] = true
		proof, n, err := read(ctx, p.root, m.path, MaxBytes-total)
		total += n
		if err != nil {
			return err
		}
		if proof.fingerprint != m.fingerprint || !same(proof.info, m.info) {
			return errors.New("候选内容已变化，请重新核对")
		}
	}
	return ctx.Err()
}
func (p *Plan) Commands(project editor.Project) ([]editor.Command, error) {
	byID := map[string]editor.Asset{}
	for _, a := range project.Assets {
		byID[a.ID] = a
	}
	commands := make([]editor.Command, 0, len(p.matches))
	for _, m := range p.matches {
		a, ok := byID[m.id]
		if !ok || a.Fingerprint != m.fingerprint {
			return nil, errors.New("原素材已变化，请重新核对")
		}
		a.Path, a.Missing = m.path, false
		a.URL, a.ThumbnailURL = "", ""
		commands = append(commands, editor.Command{Type: "update_asset", Asset: &a})
	}
	return commands, nil
}
