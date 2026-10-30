package media

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aividcut/internal/editor"
)

func TestWaveformRealSamplesStereoAndLastBucket(t *testing.T) {
	tools := speechIntegrationTools(t)
	values := make([]int16, 4*48000+5777)
	values[5] = 24576
	values[490] = -16384
	values[4*48000+50] = 8192
	asset := exactWAV(t, "wave-impulses", 48000, values)
	for index, count := range []int64{192000, 5777, 0} {
		tile, err := tools.ExtractWaveformTile(context.Background(), asset, int64(index))
		if err != nil || tile.SampleCount != count || tile.SourceStartUs != float64(index)*4e6 {
			t.Fatalf("窗口%d：%+v %v", index, tile, err)
		}
		if index == 0 && (tile.Peaks[0].Max != .75 || tile.Peaks[1].Min != -.5 || tile.Peaks[10].Max != 0) {
			t.Fatal("波形没有保留真实脉冲与静音", tile.Peaks[:12])
		}
		if index == 1 && (len(tile.Peaks) != 13 || tile.Peaks[12].Samples != 17 || tile.Peaks[0].Max != .25) {
			t.Fatal("末桶没有保留实际样本数", tile)
		}
	}
	stereoPath := filepath.Join(t.TempDir(), "反相.wav")
	command(t, tools, "-v", "error", "-i", asset.Path, "-af", "pan=stereo|c0=c0|c1=-1*c0", "-c:a", "pcm_s16le", stereoPath)
	stereo, err := tools.Probe(context.Background(), stereoPath)
	if err != nil {
		t.Fatal(err)
	}
	tile, err := tools.ExtractWaveformTile(context.Background(), stereo, 0)
	if err != nil || tile.Channels != 2 || tile.Peaks[0].Min != -.75 || tile.Peaks[0].Max != .75 {
		t.Fatal("反相双声道被错误下混成静音", tile, err)
	}
}

func TestWaveformRealCompressedSeekAndLateOrigin(t *testing.T) {
	tools := speechIntegrationTools(t)
	values := make([]int16, 9*44100+77)
	for i := range values {
		values[i] = int16(14000 * math.Sin(float64(i)*2*math.Pi*211/44100) * (0.2 + 0.8*float64(i%44100)/44100))
	}
	wav := exactWAV(t, "wave-long", 44100, values)
	for _, format := range []struct{ name, codec string }{{"mp3", "libmp3lame"}, {"m4a", "aac"}} {
		t.Run(format.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "wave."+format.name)
			command(t, tools, "-v", "error", "-i", wav.Path, "-c:a", format.codec, path)
			asset, err := tools.Probe(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			raw := command(t, tools, "-v", "error", "-i", path, "-map", "0:a:0", "-c:a", "pcm_f32le", "-f", "f32le", "pipe:1")
			for index := int64(0); index < 3; index++ {
				tile, err := tools.ExtractWaveformTile(context.Background(), asset, index)
				if err != nil {
					t.Fatal(err)
				}
				expected := min(int64(4*44100), int64(len(raw)/4)-index*4*44100)
				if tile.SampleCount != expected || math.Abs(tile.SourceStartUs-float64(index)*4e6) > 1e6/44100 {
					t.Fatalf("窗口%d定位时钟错误：起点%f 样本%d/%d", index, tile.SourceStartUs, tile.SampleCount, expected)
				}
				maxError := float64(0)
				for bucket, peak := range tile.Peaks {
					low, high := float32(1), float32(-1)
					for i := 0; i < peak.Samples; i++ {
						sample := int(index)*4*44100 + bucket*tile.BucketSamples + i
						v := math.Float32frombits(binary.LittleEndian.Uint32(raw[sample*4:]))
						low = min(low, v)
						high = max(high, v)
					}
					maxError = max(maxError, math.Abs(float64(low-peak.Min)), math.Abs(float64(high-peak.Max)))
				}
				// AAC随机访问的解码状态可能使极值略变；阈值为满幅0.2%，在22px包络内小于0.022px。
				if maxError > 0.002 {
					t.Fatalf("窗口%d峰值误差%g超过包络容差", index, maxError)
				}
				t.Logf("%s窗口%d：样本%d，独立解码峰值最大误差%g", format.name, index, tile.SampleCount, maxError)
			}
		})
	}
	late := speechSource(t, tools, "wave-late.mov", "-f", "lavfi", "-i", "color=black:s=32x32:r=25:d=10", "-itsoffset", "0.5", "-i", wav.Path, "-map", "0:v:0", "-map", "1:a:0", "-c:v", "libx264", "-c:a", "pcm_s16le", "-output_ts_offset", "3")
	first, err := tools.ExtractWaveformTile(context.Background(), late, 0)
	if err != nil || first.SourceStartUs != 500000 || first.SampleCount != int64(3.5*44100) {
		t.Fatal("晚起或容器原点丢失", first, err)
	}
	second, err := tools.ExtractWaveformTile(context.Background(), late, 1)
	if err != nil || math.Abs(second.SourceStartUs-4e6) > 1e6/44100 || second.SampleCount != 4*44100 {
		t.Fatal("晚起源远窗错误", second, err)
	}
}

func TestWaveformRejectsGapsAndCancelledContext(t *testing.T) {
	tools := speechIntegrationTools(t)
	wav := exactWAV(t, "wave-gap", 48000, make([]int16, 4800))
	gap := speechSource(t, tools, "wave-gap.mkv", "-i", wav.Path, "-af", "asetnsamples=n=480:p=0,asetpts=PTS+gte(N\\,2400)*0.1/TB", "-c:a", "pcm_s16le")
	if _, err := tools.ExtractWaveformTile(context.Background(), gap, 0); err == nil {
		t.Fatal("含时钟缺口的波形被压缩成连续样本")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tools.ExtractWaveformTile(ctx, wav, 0); !errors.Is(err, context.Canceled) {
		t.Fatal("取消未传递", err)
	}
}

func waveCacheAsset(t *testing.T) editor.Asset {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.wav")
	if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	return editor.Asset{ID: "a", Path: path, Fingerprint: "registered", Kind: "audio", HasAudio: true}
}
func waitWave(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("等待波形状态超时")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWaveformCacheSharesAndCancelsLastWaiter(t *testing.T) {
	r := NewWaveformReader(Toolchain{})
	defer r.Close()
	asset := waveCacheAsset(t)
	started, stopped := make(chan struct{}), make(chan struct{})
	r.extract = func(ctx context.Context, _ editor.Asset, _ int64) (WaveformTile, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return WaveformTile{}, ctx.Err()
	}
	a, ca := context.WithCancel(context.Background())
	b, cb := context.WithCancel(context.Background())
	defer ca()
	defer cb()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = r.Read(a, asset, 0) }()
	<-started
	go func() { defer wg.Done(); _, _ = r.Read(b, asset, 0) }()
	waitWave(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, e := range r.entries {
			if e.waiters == 2 {
				return true
			}
		}
		return false
	})
	ca()
	select {
	case <-stopped:
		t.Fatal("仍有订阅者时提取被取消")
	default:
	}
	cb()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("最后订阅离开未取消")
	}
	wg.Wait()
}

func TestWaveformCacheBoundsAndSourceChange(t *testing.T) {
	r := NewWaveformReader(Toolchain{})
	defer r.Close()
	asset := waveCacheAsset(t)
	var calls atomic.Int64
	r.extract = func(_ context.Context, _ editor.Asset, index int64) (WaveformTile, error) {
		calls.Add(1)
		return WaveformTile{Index: index, Peaks: []WaveformPeak{{Min: -.1, Max: .2, Samples: 10}}}, nil
	}
	for i := int64(0); i < 130; i++ {
		if _, err := r.Read(context.Background(), asset, i); err != nil {
			t.Fatal(err)
		}
	}
	r.mu.Lock()
	count := len(r.entries)
	r.mu.Unlock()
	if count != 128 {
		t.Fatal("缓存未限制128分块", count)
	}
	tile, err := r.Read(context.Background(), asset, 129)
	if err != nil || calls.Load() != 130 {
		t.Fatal("缓存未复用", err, calls.Load())
	}
	tile.Peaks[0].Max = 99
	tile, _ = r.Read(context.Background(), asset, 129)
	if tile.Peaks[0].Max == 99 {
		t.Fatal("调用者修改污染缓存")
	}
	if err := os.WriteFile(asset.Path, []byte("changed-source"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = r.Read(context.Background(), asset, 129)
	if err != nil || calls.Load() != 131 {
		t.Fatal("源变化未失效缓存", err, calls.Load())
	}
}

func TestWaveformCacheLimitsConcurrentExtractors(t *testing.T) {
	r := NewWaveformReader(Toolchain{})
	defer r.Close()
	asset := waveCacheAsset(t)
	var active, peak atomic.Int64
	gate := make(chan struct{})
	r.extract = func(ctx context.Context, _ editor.Asset, _ int64) (WaveformTile, error) {
		v := active.Add(1)
		defer active.Add(-1)
		for p := peak.Load(); v > p && !peak.CompareAndSwap(p, v); p = peak.Load() {
		}
		select {
		case <-gate:
			return WaveformTile{}, nil
		case <-ctx.Done():
			return WaveformTile{}, ctx.Err()
		}
	}
	var wg sync.WaitGroup
	for i := int64(0); i < 10; i++ {
		wg.Add(1)
		go func(index int64) { defer wg.Done(); _, _ = r.Read(context.Background(), asset, index) }(i)
	}
	waitWave(t, func() bool { return active.Load() == 2 })
	if peak.Load() != 2 {
		t.Fatal("提取并发超过2", peak.Load())
	}
	close(gate)
	wg.Wait()
}

func TestWaveformCachePendingBoundCloseAndChangedDuringRead(t *testing.T) {
	r := NewWaveformReader(Toolchain{})
	asset := waveCacheAsset(t)
	r.extract = func(ctx context.Context, _ editor.Asset, _ int64) (WaveformTile, error) {
		<-ctx.Done()
		return WaveformTile{}, ctx.Err()
	}
	var wg sync.WaitGroup
	for i := int64(0); i < 128; i++ {
		wg.Add(1)
		go func(index int64) { defer wg.Done(); _, _ = r.Read(context.Background(), asset, index) }(i)
	}
	waitWave(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return len(r.entries) == 128 })
	if _, err := r.Read(context.Background(), asset, 200); err == nil {
		t.Fatal("等待队列超出128分块")
	}
	r.Close()
	wg.Wait()
	if _, err := r.Read(context.Background(), asset, 0); err == nil {
		t.Fatal("关闭后仍启动提取")
	}
	r = NewWaveformReader(Toolchain{})
	defer r.Close()
	r.extract = func(_ context.Context, _ editor.Asset, _ int64) (WaveformTile, error) {
		if err := os.WriteFile(asset.Path, []byte("changed while decoding"), 0600); err != nil {
			return WaveformTile{}, err
		}
		return WaveformTile{}, nil
	}
	if _, err := r.Read(context.Background(), asset, 0); err == nil {
		t.Fatal("读取中源变化仍返回波形")
	}
}
