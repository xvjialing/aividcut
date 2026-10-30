// Package projectfonts 校验工程字体文件与实际字形，不安装系统字体。
package projectfonts

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"aividcut/internal/editor"
	"golang.org/x/image/font/sfnt"
)

const MaxFileBytes int64 = 32 << 20
const MaxUsedFonts = 32
const MaxUsedBytes int64 = 128 << 20

type File struct {
	Data   []byte
	Font   *sfnt.Font
	Info   editor.FontInfo
	SHA256 string
}
type Verified struct {
	Path   string
	SHA256 string
	Info   editor.FontInfo
}

func Read(path string) (File, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".ttf" && ext != ".otf" {
		return File{}, errors.New("工程字体仅支持单字体TTF或OTF文件")
	}
	f, err := os.Open(path)
	if err != nil {
		return File{}, errors.New("字体文件无法读取，请重新定位原字体")
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() < 12 || before.Size() > MaxFileBytes {
		return File{}, errors.New("字体须为32MiB以内的普通TTF/OTF文件")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil || int64(len(data)) > MaxFileBytes {
		return File{}, errors.New("字体读取失败或过大")
	}
	after, err := f.Stat()
	if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return File{}, errors.New("字体读取期间发生变化")
	}
	if len(data) < 12 || string(data[:4]) == "ttcf" {
		return File{}, errors.New("暂不支持字体集合，请使用单字体TTF/OTF")
	}
	n := int(binary.BigEndian.Uint16(data[4:6]))
	if n < 1 || n > 256 || 12+n*16 > len(data) {
		return File{}, errors.New("字体表目录无效")
	}
	weight, italic := 400, false
	for i := 0; i < n; i++ {
		entry := data[12+i*16 : 28+i*16]
		tag := string(entry[:4])
		offset, length := uint64(binary.BigEndian.Uint32(entry[8:12])), uint64(binary.BigEndian.Uint32(entry[12:16]))
		if offset+length > uint64(len(data)) {
			return File{}, errors.New("字体表超出文件范围")
		}
		switch tag {
		case "fvar", "COLR", "CBDT", "sbix", "SVG ", "CFF2":
			return File{}, errors.New("暂不支持可变、彩色或CFF2字体，请使用静态TTF/OTF")
		}
		if tag == "OS/2" && length >= 64 {
			table := data[offset : offset+length]
			weight = int(binary.BigEndian.Uint16(table[4:6]))
			italic = binary.BigEndian.Uint16(table[62:64])&1 != 0
		}
	}
	font, err := sfnt.Parse(data)
	if err != nil {
		return File{}, fmt.Errorf("字体结构不兼容：%w", err)
	}
	var buffer sfnt.Buffer
	name := func(primary, fallback sfnt.NameID) string {
		v, _ := font.Name(&buffer, primary)
		if strings.TrimSpace(v) == "" {
			v, _ = font.Name(&buffer, fallback)
		}
		return strings.TrimSpace(v)
	}
	info := editor.FontInfo{Copyright: name(sfnt.NameIDCopyright, sfnt.NameIDCopyright), License: name(sfnt.NameIDLicense, sfnt.NameIDLicense), LicenseURL: name(sfnt.NameIDLicenseURL, sfnt.NameIDLicenseURL), Family: name(sfnt.NameIDTypographicFamily, sfnt.NameIDFamily), Style: name(sfnt.NameIDTypographicSubfamily, sfnt.NameIDSubfamily), FullName: name(sfnt.NameIDFull, sfnt.NameIDFamily), Weight: weight, Italic: italic}
	if info.Style == "" {
		info.Style = "Regular"
	}
	if info.Family == "" || len(info.Family) > 512 || len(info.Style) > 512 || len(info.FullName) > 512 || len(info.Copyright) > 8192 || len(info.License) > 16384 || len(info.LicenseURL) > 2048 || weight < 1 || weight > 1000 {
		return File{}, errors.New("字体名称或字重无效")
	}
	sum := sha256.Sum256(data)
	return File{Data: data, Font: font, Info: info, SHA256: hex.EncodeToString(sum[:])}, nil
}
func (f File) CheckText(text string) error {
	var buffer sfnt.Buffer
	missing := []rune{}
	seen := map[rune]bool{}
	for _, r := range text {
		if unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) || r >= 0xfe00 && r <= 0xfe0f {
			continue
		}
		glyph, err := f.Font.GlyphIndex(&buffer, r)
		if err != nil {
			return err
		}
		if glyph == 0 && !seen[r] {
			seen[r] = true
			missing = append(missing, r)
			if len(missing) == 8 {
				break
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("所选工程字体不含字形“%s”，请选择包含这些字的字体", string(missing))
	}
	return nil
}
func Resolve(p editor.Project) (map[string]Verified, error) {
	used := map[string][]string{}
	for _, c := range p.Clips {
		if c.TextStyle.FontAssetID != "" {
			used[c.TextStyle.FontAssetID] = append(used[c.TextStyle.FontAssetID], c.Text)
		}
	}
	if len(used) > MaxUsedFonts {
		return nil, errors.New("单工程当前最多使用32份字体")
	}
	out := map[string]Verified{}
	var total int64
	for _, a := range p.Assets {
		texts, ok := used[a.ID]
		if !ok {
			continue
		}
		file, err := Read(a.Path)
		if err != nil {
			return nil, fmt.Errorf("字体 %s：%w", a.Name, err)
		}
		total += int64(len(file.Data))
		if total > MaxUsedBytes {
			return nil, errors.New("工程使用的字体超过128MiB预算")
		}
		if a.Fingerprint == "" || a.Fingerprint != "sha256:"+file.SHA256 || a.Font == nil || *a.Font != file.Info {
			return nil, fmt.Errorf("字体 %s 已变化，不能按同名文件代换，请重新导入或定位原字体", a.Name)
		}
		for _, text := range texts {
			if err = file.CheckText(text); err != nil {
				return nil, err
			}
		}
		out[a.ID] = Verified{Path: a.Path, SHA256: file.SHA256, Info: file.Info}
	}
	if len(out) != len(used) {
		return nil, errors.New("工程引用的字体资源不存在")
	}
	return out, nil
}
