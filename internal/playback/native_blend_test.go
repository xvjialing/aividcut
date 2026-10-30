package playback

import (
	"bytes"
	"context"
	"hash/crc32"
	"os"
	"testing"

	"aividcut/internal/editor"
)

// 前版本二进制仅作为本地验收参考，不随产品分发；没有参考时明确跳过。
func TestNativeFixedBlendAgainstPreservedExpressionWorker(t *testing.T) {
	beforePath := os.Getenv("AIVIDCUT_BASELINE_WORKER")
	if beforePath == "" {
		t.Skip("需要明确提供本轮保存的旧表达式worker，不把跳过当作前后比较通过")
	}
	config := nativeConfig(t)
	config.MaxWidth, config.MaxHeight = 1920, 1080
	for _, quality := range []string{"full", "half", "quarter"} {
		for _, intensity := range []float64{0, .000001, .123456789, .37, .5, .99, 1} {
			p := visualProject(t)
			p.Clips[1].Transform = editor.Transform{Scale: 1.25, Rotation: 30, X: -23, Y: 19}
			p.Clips[1].LUT = &editor.LUT{AssetID: "cube", Intensity: intensity}
			config.RenderQuality = quality
			oldConfig := config
			oldConfig.WorkerPath = beforePath
			old, err := Start(context.Background(), oldConfig)
			if err != nil {
				t.Fatal(err)
			}
			current, err := Start(context.Background(), config)
			if err != nil {
				old.Close()
				t.Fatal(err)
			}
			if _, err = old.Load(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			if _, err = current.Load(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			maxDifference, changed := 0, 0
			var oldPCM, newPCM uint32
			for at := int64(0); at < 3; at++ {
				a, err := old.Seek(context.Background(), at)
				if err != nil {
					t.Fatal(err)
				}
				b, err := current.Seek(context.Background(), at)
				if err != nil {
					t.Fatal(err)
				}
				if a.Position != b.Position || a.Width != b.Width || a.Height != b.Height || a.AudioStartSample != b.AudioStartSample || !bytes.Equal(a.PCM, b.PCM) {
					t.Fatal("混合优化改变时钟、画幅或PCM", quality, intensity)
				}
				oldPCM = crc32.Update(oldPCM, crc32.IEEETable, a.PCM)
				newPCM = crc32.Update(newPCM, crc32.IEEETable, b.PCM)
				for i := range a.RGBA {
					d := int(a.RGBA[i]) - int(b.RGBA[i])
					if d < 0 {
						d = -d
					}
					if d > maxDifference {
						maxDifference = d
					}
					if d > 0 {
						changed++
					}
				}
			}
			old.Close()
			current.Close()
			if maxDifference > 2 || ((intensity == 0 || intensity == 1) && changed != 0) {
				t.Fatal("原生半透明合成超过舍入边界或改坏端点", quality, intensity, maxDifference, changed)
			}
			t.Logf("%s/%.9f，旋转半透明3帧最大差%d，变化分量%d，PCM %d=%d", quality, intensity, maxDifference, changed, oldPCM, newPCM)
		}
	}
}
