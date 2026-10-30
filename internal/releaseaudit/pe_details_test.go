package releaseaudit

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"testing"
)

func TestPEDelayImportsAreNotSilentlyOmitted(t *testing.T) {
	data := make([]byte, 256)
	binary.LittleEndian.PutUint32(data[:4], 1)
	binary.LittleEndian.PutUint32(data[4:8], 0x1080)
	copy(data[128:], "optional-codec.dll\x00")
	h := &pe.OptionalHeader64{ImageBase: 0x140000000, NumberOfRvaAndSizes: 16}
	h.DataDirectory[13] = pe.DataDirectory{VirtualAddress: 0x1000, Size: 64}
	p := &pe.File{OptionalHeader: h, Sections: []*pe.Section{{SectionHeader: pe.SectionHeader{VirtualAddress: 0x1000, Size: 256}}}}
	imports, e := peImportDetails(p, bytes.NewReader(data))
	if e != nil || len(imports) != 1 || imports[0].Name != "optional-codec.dll" || !imports[0].Delayed {
		t.Fatal(imports, e)
	}
	binary.LittleEndian.PutUint32(data[4:8], 0xfffffff0)
	if _, e = peImportDetails(p, bytes.NewReader(data)); e == nil {
		t.Fatal("延迟RVA越界不应作为无依赖")
	}
	binary.LittleEndian.PutUint32(data[4:8], 0x1080)
	binary.LittleEndian.PutUint32(data[:4], 0)
	if _, e = peImportDetails(p, bytes.NewReader(data)); e == nil {
		t.Fatal("不合法VA模式不应被忽略")
	}
}
