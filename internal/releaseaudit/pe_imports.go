package releaseaudit

import (
	"debug/pe"
	"encoding/binary"
	"errors"
	"io"
)

// Go的pe.ImportedLibraries仍未实现；直接读取普通导入描述符，不能把nil当无依赖。
func peImports(p *pe.File, reader io.ReaderAt) ([]string, error) {
	var table pe.DataDirectory
	switch h := p.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		if h.NumberOfRvaAndSizes > pe.IMAGE_DIRECTORY_ENTRY_IMPORT {
			table = h.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_IMPORT]
		}
	case *pe.OptionalHeader32:
		if h.NumberOfRvaAndSizes > pe.IMAGE_DIRECTORY_ENTRY_IMPORT {
			table = h.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_IMPORT]
		}
	default:
		return nil, errors.New("PE缺少可执行文件可选头")
	}
	if table.VirtualAddress == 0 && table.Size == 0 {
		return nil, nil
	}
	read := func(rva uint64, b []byte) error {
		for _, s := range p.Sections {
			if rva >= uint64(s.VirtualAddress) {
				delta := rva - uint64(s.VirtualAddress)
				if delta+uint64(len(b)) <= uint64(s.Size) {
					_, e := reader.ReadAt(b, int64(uint64(s.Offset)+delta))
					return e
				}
			}
		}
		return errors.New("PE导入RVA超出文件节范围")
	}
	var names []string
	for i := uint64(0); i < 2048 && i*20+20 <= uint64(table.Size); i++ {
		var entry [20]byte
		if e := read(uint64(table.VirtualAddress)+i*20, entry[:]); e != nil {
			return nil, e
		}
		if entry == [20]byte{} {
			return names, nil
		}
		rva := binary.LittleEndian.Uint32(entry[12:16])
		if rva == 0 {
			return nil, errors.New("PE导入缺少DLL名称")
		}
		var name []byte
		terminated := false
		for j := uint64(0); j < 512; j++ {
			var c [1]byte
			if e := read(uint64(rva)+j, c[:]); e != nil {
				return nil, e
			}
			if c[0] == 0 {
				terminated = true
				break
			}
			if c[0] < 32 || c[0] > 126 {
				return nil, errors.New("PE导入名称含非ASCII字节")
			}
			name = append(name, c[0])
		}
		if !terminated || len(name) == 0 {
			return nil, errors.New("PE导入名称无效或过长")
		}
		names = append(names, string(name))
	}
	return nil, errors.New("PE导入表未正常结束或超过2048项")
}
