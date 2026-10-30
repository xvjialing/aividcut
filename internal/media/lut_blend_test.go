package media

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRealFixedLUTBlendExhaustiveRGBAAgainstExpression(t *testing.T) {
	tools := speechIntegrationTools(t)
	dir := t.TempDir()
	top, bottom := make([]byte, 256*256*4), make([]byte, 256*256*4)
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			for c := 0; c < 4; c++ {
				at := (a*256+b)*4 + c
				top[at], bottom[at] = byte(a), byte(b)
			}
		}
	}
	aPath, bPath := filepath.Join(dir, "a.rgba"), filepath.Join(dir, "b.rgba")
	if err := os.WriteFile(aPath, top, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, bottom, 0600); err != nil {
		t.Fatal(err)
	}
	for _, intensity := range []float64{.000000001, .000001, .01, .123456789, .25, .37, .5, .73, .99, .999999999} {
		t.Run(fmt.Sprint(intensity), func(t *testing.T) {
			a, b := number(1-intensity), number(intensity)
			oldPath, newPath := filepath.Join(dir, "old.rgba"), filepath.Join(dir, "new.rgba")
			graph := fmt.Sprintf("[0:v]split[a0][a1];[1:v]split[b0][b1];[a0][b0]blend=all_expr=A*%s+B*%s,format=rgba[old];[a1][b1]blend=all_mode=normal:all_opacity=%s,format=rgba[new]", a, b, a)
			command(t, tools, "-v", "error", "-y", "-filter_complex_threads", "1", "-f", "rawvideo", "-pixel_format", "rgba", "-video_size", "256x256", "-i", aPath, "-f", "rawvideo", "-pixel_format", "rgba", "-video_size", "256x256", "-i", bPath, "-filter_complex", graph, "-map", "[old]", "-frames:v", "1", "-f", "rawvideo", oldPath, "-map", "[new]", "-frames:v", "1", "-f", "rawvideo", newPath)
			old, err := os.ReadFile(oldPath)
			if err != nil {
				t.Fatal(err)
			}
			current, err := os.ReadFile(newPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(old) != len(top) || len(current) != len(top) {
				t.Fatal("RGBA长度变化")
			}
			maximum, changed := 0, 0
			for i := range old {
				d := int(old[i]) - int(current[i])
				if d < 0 {
					d = -d
				}
				if d > maximum {
					maximum = d
				}
				if d > 0 {
					changed++
				}
			}
			if maximum > 1 {
				t.Fatal("固定权重超过8位舍入边界", maximum)
			}
			t.Logf("65536对RGBA值，强度%.9f，最大差%d，变化分量%d", intensity, maximum, changed)
		})
	}
}
