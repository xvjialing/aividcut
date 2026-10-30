package releaseaudit

import (
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

type DLLImport struct {
	Name    string `json:"name"`
	Delayed bool   `json:"delayed"`
}
type PEImage struct {
	SHA256       string      `json:"sha256"`
	Bytes        int64       `json:"bytes"`
	Architecture string      `json:"architecture"`
	DLL          bool        `json:"dll"`
	Imports      []DLLImport `json:"imports"`
}

// ReadPEImage 不执行Windows程序，同时读取普通与延迟导入；API-set不等于磁盘DLL。
func ReadPEImage(path string) (PEImage, error) {
	var image PEImage
	f, e := os.Open(path)
	if e != nil {
		return image, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return image, errors.New("PE必须为512MiB以内普通文件")
	}
	p, e := pe.NewFile(f)
	if e != nil {
		return image, e
	}
	switch p.Machine {
	case pe.IMAGE_FILE_MACHINE_AMD64:
		image.Architecture = "amd64"
	case pe.IMAGE_FILE_MACHINE_ARM64:
		image.Architecture = "arm64"
	default:
		return image, errors.New("PE目标架构不受支持")
	}
	image.DLL = p.Characteristics&pe.IMAGE_FILE_DLL != 0
	image.Imports, e = peImportDetails(p, f)
	if e != nil {
		return image, e
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxFileBytes+1))
	if err != nil || n > maxFileBytes {
		return image, errors.New("PE读取超界或失败")
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return image, errors.New("PE在检查期间变化")
	}
	image.SHA256 = hex.EncodeToString(h.Sum(nil))
	image.Bytes = n
	return image, e
}
func peImportDetails(p *pe.File, r io.ReaderAt) ([]DLLImport, error) {
	ordinary, e := peImports(p, r)
	if e != nil {
		return nil, e
	}
	out := []DLLImport{}
	for _, name := range ordinary {
		out = append(out, DLLImport{Name: name})
	}
	var table pe.DataDirectory
	var base uint64
	switch h := p.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		base = h.ImageBase
		if h.NumberOfRvaAndSizes > 13 {
			table = h.DataDirectory[13]
		}
	case *pe.OptionalHeader32:
		base = uint64(h.ImageBase)
		if h.NumberOfRvaAndSizes > 13 {
			table = h.DataDirectory[13]
		}
	default:
		return nil, errors.New("PE缺可选头")
	}
	if table.VirtualAddress == 0 && table.Size == 0 {
		return out, nil
	}
	read := func(rva uint64, b []byte) error {
		for _, s := range p.Sections {
			if rva >= uint64(s.VirtualAddress) {
				delta := rva - uint64(s.VirtualAddress)
				if delta+uint64(len(b)) <= uint64(s.Size) {
					_, e := r.ReadAt(b, int64(uint64(s.Offset)+delta))
					return e
				}
			}
		}
		return errors.New("PE延迟导入RVA越界")
	}
	for i := uint64(0); i < 2048 && i*32+32 <= uint64(table.Size); i++ {
		var entry [32]byte
		if e = read(uint64(table.VirtualAddress)+i*32, entry[:]); e != nil {
			return nil, e
		}
		if entry == [32]byte{} {
			return out, nil
		}
		attributes := binary.LittleEndian.Uint32(entry[:4])
		if attributes&^uint32(1) != 0 {
			return nil, errors.New("PE延迟导入属性未知")
		}
		nameRVA := uint64(binary.LittleEndian.Uint32(entry[4:8]))
		if attributes&1 == 0 {
			if nameRVA < base {
				return nil, errors.New("PE延迟VA无法转换")
			}
			nameRVA -= base
		}
		var name []byte
		terminated := false
		for j := uint64(0); j < 512; j++ {
			var c [1]byte
			if e = read(nameRVA+j, c[:]); e != nil {
				return nil, e
			}
			if c[0] == 0 {
				terminated = true
				break
			}
			if c[0] < 32 || c[0] > 126 {
				return nil, errors.New("PE延迟导入名称无效")
			}
			name = append(name, c[0])
		}
		if !terminated || len(name) == 0 {
			return nil, errors.New("PE延迟导入名称过长或为空")
		}
		out = append(out, DLLImport{Name: string(name), Delayed: true})
	}
	return nil, errors.New("PE延迟导入表未结束或超过2048项")
}
