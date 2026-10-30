package releaseaudit

import (
	"bytes"
	"context"
	"hash"
	"io"
)

// 流式查找跨读取块的内联映射，不把大型dist文件整体装入内存。
func hashFrontend(ctx context.Context, r io.Reader, h hash.Hash) (bool, error) {
	marker := []byte("sourceMappingURL=data:")
	buffer := make([]byte, 65536)
	var carry []byte
	found := false
	for {
		if e := ctx.Err(); e != nil {
			return false, e
		}
		n, e := r.Read(buffer)
		if n > 0 {
			h.Write(buffer[:n])
			chunk := append(carry, buffer[:n]...)
			if bytes.Contains(chunk, marker) {
				found = true
			}
			start := max(0, len(chunk)-len(marker)+1)
			carry = append([]byte(nil), chunk[start:]...)
		}
		if e == io.EOF {
			return found, nil
		}
		if e != nil {
			return false, e
		}
	}
}
