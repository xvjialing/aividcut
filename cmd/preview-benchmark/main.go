// 开发诊断实际原生拉帧；不启动音频设备，不把吞吐量当作整个编辑器播放体验。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/playback"
	"aividcut/internal/projectstore"
)

type configuration struct {
	project, output string
	from            int64
	frames          int
	width, height   int
	timeout         time.Duration
	interval        time.Duration
	seeks           string
	pictureSeeks    bool
	windowClips     int
	windowFrames    int64
}
type seekResult struct {
	Position         int64   `json:"position"`
	ElapsedMs        float64 `json:"elapsedMs"`
	AudioStartSample int64   `json:"audioStartSample"`
	Samples          int     `json:"samples"`
	ImageCRC32       uint32  `json:"imageCrc32"`
	AudioCRC32       uint32  `json:"audioCrc32"`
}
type frameResult struct {
	Position         int64   `json:"position"`
	AudioStartSample int64   `json:"audioStartSample"`
	Samples          int     `json:"samples"`
	PullMs           float64 `json:"pullMs"`
}
type report struct {
	CreatedAt             time.Time             `json:"createdAt"`
	OS                    string                `json:"os"`
	Architecture          string                `json:"architecture"`
	GoVersion             string                `json:"goVersion"`
	LogicalCPUs           int                   `json:"logicalCpus"`
	ProjectID             string                `json:"projectId"`
	ProjectRevision       int64                 `json:"projectRevision"`
	SequenceID            string                `json:"sequenceId"`
	SequenceName          string                `json:"sequenceName"`
	Sequences             int                   `json:"sequences"`
	Clips                 int                   `json:"clips"`
	Assets                int                   `json:"assets"`
	Info                  playback.Info         `json:"info"`
	MLTVersion            string                `json:"mltVersion"`
	ProjectSHA256         string                `json:"projectSha256"`
	WorkerSHA256          string                `json:"workerSha256"`
	InspectMs             float64               `json:"inspectMs"`
	LoadMs                float64               `json:"loadMs"`
	SeekMs                float64               `json:"seekMs"`
	Seeks                 []seekResult          `json:"seeks,omitempty"`
	SeekP50Ms             float64               `json:"seekP50Ms,omitempty"`
	PictureSeeks          bool                  `json:"pictureSeeks,omitempty"`
	WindowClips           int                   `json:"windowClips,omitempty"`
	WindowFrames          int64                 `json:"windowFrames,omitempty"`
	SeekDiagnosticsBefore *playback.Diagnostics `json:"seekDiagnosticsBefore,omitempty"`
	SeekDiagnosticsAfter  *playback.Diagnostics `json:"seekDiagnosticsAfter,omitempty"`
	SeekP95Ms             float64               `json:"seekP95Ms,omitempty"`
	SeekMaxMs             float64               `json:"seekMaxMs,omitempty"`
	PullP50Ms             float64               `json:"pullP50Ms"`
	PullP95Ms             float64               `json:"pullP95Ms"`
	PullMaxMs             float64               `json:"pullMaxMs"`
	FrameBudgetMs         float64               `json:"frameBudgetMs"`
	OverBudgetFrames      int                   `json:"overBudgetFrames"`
	DecodedSamples        int64                 `json:"decodedSamples"`
	NonzeroChannelSamples int64                 `json:"decodedNonzeroChannelSamples"`
	PeakAmplitude         float64               `json:"decodedPeakAmplitude"`
	ActualImageCRC32      uint32                `json:"actualImageCrc32"`
	ActualAudioCRC32      uint32                `json:"actualAudioCrc32"`
	Frames                []frameResult         `json:"frames"`
	Resources             resourceReport        `json:"resources"`
	NativeDiagnostics     *playback.Diagnostics `json:"nativeDiagnostics,omitempty"`
	DiagnosticsReason     string                `json:"diagnosticsReason,omitempty"`
	Limitations           []string              `json:"limitations"`
	Failure               string                `json:"failure,omitempty"`
}

func main() {
	var input configuration
	flag.StringVar(&input.project, "project", "", "只读打开的 .aividcut 工程")
	flag.StringVar(&input.output, "out", "", "诊断 JSON 路径，不得覆盖工程或素材")
	flag.Int64Var(&input.from, "from", 0, "定位到的工程帧")
	flag.StringVar(&input.seeks, "seek-frames", "", "可选的有序定位帧列表，逗号分隔；最多128项，然后回到from连续拉帧")
	flag.BoolVar(&input.pictureSeeks, "picture-seeks", false, "定位列表只取画面；回到from与后续拉帧仍准备真实音频")
	flag.IntVar(&input.windowClips, "window-clips", 0, "开发验收：收紧图窗口片段预算，0为生产默认")
	flag.Int64Var(&input.windowFrames, "window-frames", 0, "开发验收：收紧图窗口帧数，0为生产默认")
	flag.IntVar(&input.frames, "frames", 300, "连续拉取帧数，末端自动停止，最多100000")
	flag.IntVar(&input.width, "width", 1280, "预览代理最大宽度")
	flag.IntVar(&input.height, "height", 720, "预览代理最大高度")
	flag.DurationVar(&input.timeout, "timeout", 3*time.Minute, "本次诊断总预算")
	flag.DurationVar(&input.interval, "sample-interval", 100*time.Millisecond, "资源采样间隔，至少20ms")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := execute(ctx, input); err != nil {
		fmt.Fprintln(os.Stderr, "原生拉帧诊断未完成：", err)
		os.Exit(1)
	}
}

func milliseconds(value time.Duration) float64 { return float64(value) / float64(time.Millisecond) }

func parseSeeks(value string) ([]int64, error) {
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	if len(parts) > 128 {
		return nil, errors.New("定位帧列表最多128项")
	}
	result := make([]int64, 0, len(parts))
	for _, part := range parts {
		frame, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || frame < 0 {
			return nil, errors.New("定位列表必须为非负整数帧，不能含空项")
		}
		result = append(result, frame)
	}
	return result, nil
}

// 使用最近秩分位数，保留原始每帧数据，不能把不均匀的冷/热样本平均后冒充p95。
func percentile(values []float64, fraction float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	index := max(0, min(len(sorted)-1, int(math.Ceil(fraction*float64(len(sorted))))-1))
	return sorted[index]
}

func execute(parent context.Context, input configuration) error {
	if input.project == "" || input.output == "" || input.from < 0 || input.frames < 1 || input.frames > 100000 || input.timeout <= 0 || input.interval < 20*time.Millisecond {
		return errors.New("需要明确工程和输出路径、有效帧数/预算，资源采样间隔至少20ms")
	}
	seeks, err := parseSeeks(input.seeks)
	if err != nil {
		return err
	}
	projectPath, err := filepath.Abs(input.project)
	if err != nil {
		return err
	}
	output, err := filepath.Abs(input.output)
	if err != nil {
		return err
	}
	project, err := projectstore.Load(projectPath)
	if err != nil {
		return err
	}
	if output == projectPath {
		return errors.New("不能覆盖被诊断工程")
	}
	outputStat, _ := os.Stat(output)
	if inputStat, err := os.Stat(projectPath); err == nil && outputStat != nil && os.SameFile(inputStat, outputStat) {
		return errors.New("诊断输出与工程指向同一文件，不能覆盖")
	}
	for _, asset := range project.Assets {
		path, _ := filepath.Abs(asset.Path)
		if path == output {
			return errors.New("不能覆盖工程素材")
		}
		if inputStat, err := os.Stat(path); err == nil && outputStat != nil && os.SameFile(inputStat, outputStat) {
			return errors.New("诊断输出与素材指向同一文件，不能覆盖")
		}
	}
	ctx, cancel := context.WithTimeout(parent, input.timeout)
	defer cancel()
	result := report{CreatedAt: time.Now().UTC(), OS: runtime.GOOS, Architecture: runtime.GOARCH, GoVersion: runtime.Version(), LogicalCPUs: runtime.NumCPU(), ProjectID: project.ID, ProjectRevision: project.Revision, Clips: len(project.Clips), Assets: len(project.Assets), Frames: []frameResult{}, Limitations: []string{
		"没有设备播放、WebView、模型或CLI；连续pull是原生吞吐诊断，不是整个客户端实时性能验收。",
		"RSS仅定时采样，可能遗漏瞬时峰值；不同进程的RSS包含共享页面，不能相加视为独占物理内存。",
		"资源采样在启动worker前开始，但定时查询仍可能遗漏短暂启动峰值；采样进程本身会影响调度。",
		"此工具不判断内容语义、音色质量、完整字体/色彩或商业发行许可。",
	}}
	result.ProjectSHA256, err = fileSHA256(projectPath)
	if err != nil {
		return err
	}
	result.SequenceID, result.SequenceName, result.Sequences = editor.CurrentSequenceID(project), editor.CurrentSequenceName(project), len(project.OtherSequences)+1
	result.WorkerSHA256, err = fileSHA256(os.Getenv("AIVIDCUT_MLT_WORKER"))
	if err != nil {
		return fmt.Errorf("无法读取明确worker的构建指纹：%w", err)
	}
	started := time.Now()
	result.WindowClips, result.WindowFrames = input.windowClips, input.windowFrames
	sampler := newResourceSampler(ctx, 0, input.interval)
	defer sampler.close()
	session, err := playback.Start(ctx, playback.Config{WorkerPath: os.Getenv("AIVIDCUT_MLT_WORKER"), MLTRepository: os.Getenv("AIVIDCUT_MLT_REPOSITORY"), MLTProfilesPath: os.Getenv("AIVIDCUT_MLT_PROFILES"), MLTDataPath: os.Getenv("AIVIDCUT_MLT_DATA"), MaxWidth: input.width, MaxHeight: input.height, Timeout: 30 * time.Second, WindowClipLimit: input.windowClips, WindowFrames: input.windowFrames})
	result.InspectMs = milliseconds(time.Since(started))
	if err != nil {
		result.Failure = err.Error()
		sampler.close()
		result.Resources = sampler.snapshot()
		return errors.Join(err, writeReport(output, result))
	}
	defer session.Close()
	result.MLTVersion = session.Capabilities().MLTVersion
	workerPID := 0
	if observable, ok := any(session).(interface{ ProcessID() int }); ok {
		workerPID = observable.ProcessID()
	}
	sampler.setWorker(workerPID)
	finish := func(problem error) error {
		if diagnostics, err := session.Diagnostics(ctx); err == nil {
			result.NativeDiagnostics = &diagnostics
		} else {
			result.DiagnosticsReason = "当前worker诊断不可用：" + err.Error()
		}
		sampler.close()
		result.Resources = sampler.snapshot()
		values := make([]float64, 0, len(result.Frames))
		for _, frame := range result.Frames {
			values = append(values, frame.PullMs)
		}
		result.PullP50Ms, result.PullP95Ms = percentile(values, .5), percentile(values, .95)
		seekTimes := make([]float64, 0, len(result.Seeks))
		for _, seek := range result.Seeks {
			seekTimes = append(seekTimes, seek.ElapsedMs)
			result.SeekMaxMs = max(result.SeekMaxMs, seek.ElapsedMs)
		}
		result.SeekP50Ms, result.SeekP95Ms = percentile(seekTimes, .5), percentile(seekTimes, .95)
		if problem != nil {
			result.Failure = problem.Error()
		}
		if writeErr := writeReport(output, result); writeErr != nil {
			return errors.Join(problem, writeErr)
		}
		return problem
	}
	started = time.Now()
	result.Info, err = session.Load(ctx, project)
	result.LoadMs = milliseconds(time.Since(started))
	if err != nil {
		return finish(err)
	}
	if input.from >= result.Info.DurationFrames {
		return finish(errors.New("定位不在工程有效帧区间"))
	}
	result.FrameBudgetMs = 1000 * float64(result.Info.FPSDen) / float64(result.Info.FPSNum)
	for _, at := range seeks {
		if at >= result.Info.DurationFrames {
			return finish(errors.New("定位列表包含超出工程的帧"))
		}
	}
	if len(seeks) > 0 {
		if d, err := session.Diagnostics(ctx); err == nil {
			result.SeekDiagnosticsBefore = &d
		}
	}
	for _, at := range seeks {
		started = time.Now()
		var selected playback.Frame
		if input.pictureSeeks {
			selected, err = session.Preview(ctx, at)
		} else {
			selected, err = session.Seek(ctx, at)
		}
		elapsed := milliseconds(time.Since(started))
		if err != nil {
			return finish(fmt.Errorf("第%d次定位帧%d失败：%w", len(result.Seeks)+1, at, err))
		}
		if selected.Position != at {
			return finish(errors.New("定位返回错误画面位置，不能计入性能数据"))
		}
		result.Seeks = append(result.Seeks, seekResult{Position: at, ElapsedMs: elapsed, AudioStartSample: selected.AudioStartSample, Samples: selected.Samples, ImageCRC32: crc32.ChecksumIEEE(selected.RGBA), AudioCRC32: crc32.ChecksumIEEE(selected.PCM)})
	}
	result.PictureSeeks = input.pictureSeeks && len(seeks) > 0
	if len(seeks) > 0 {
		if d, err := session.Diagnostics(ctx); err == nil {
			result.SeekDiagnosticsAfter = &d
		}
	}
	started = time.Now()
	frame, err := session.Seek(ctx, input.from)
	result.SeekMs = milliseconds(time.Since(started))
	if err != nil {
		return finish(err)
	}
	expectedSample := frame.AudioStartSample
	if err = session.Play(ctx); err != nil {
		return finish(err)
	}
	hash := crc32.NewIEEE()
	audioHash := crc32.NewIEEE()
	for i := 0; i < input.frames && input.from+int64(i) < result.Info.DurationFrames; i++ {
		started = time.Now()
		frame, err := session.Next(ctx)
		elapsed := milliseconds(time.Since(started))
		if err != nil {
			return finish(err)
		}
		if frame.Position != input.from+int64(i) || frame.AudioStartSample != expectedSample || frame.Samples < 1 || len(frame.RGBA) != frame.Width*frame.Height*4 || len(frame.PCM) != frame.Samples*8 {
			return finish(errors.New("实际画面、PCM或位置不连续，不能用该结果宣称性能通过"))
		}
		expectedSample += int64(frame.Samples)
		nonzero, peak, err := observePCM(frame.PCM)
		if err != nil {
			return finish(err)
		}
		result.NonzeroChannelSamples += nonzero
		result.PeakAmplitude = max(result.PeakAmplitude, peak)
		result.DecodedSamples += int64(frame.Samples)
		_, _ = audioHash.Write(frame.PCM)
		_, _ = hash.Write(frame.RGBA)
		result.Frames = append(result.Frames, frameResult{Position: frame.Position, AudioStartSample: frame.AudioStartSample, Samples: frame.Samples, PullMs: elapsed})
		result.PullMaxMs = max(result.PullMaxMs, elapsed)
		if elapsed > result.FrameBudgetMs {
			result.OverBudgetFrames++
		}
	}
	result.ActualImageCRC32 = hash.Sum32()
	result.ActualAudioCRC32 = audioHash.Sum32()
	if err := finish(nil); err != nil {
		return err
	}
	fmt.Printf("已记录%d帧：pull p50 %.3fms / p95 %.3fms；资源与限制见 %s\n", len(result.Frames), result.PullP50Ms, result.PullP95Ms, output)
	return nil
}

// 读取真实双声道float32，不把空白/非有限数的PCM包冒充有效有声输出。
func observePCM(data []byte) (int64, float64, error) {
	if len(data)%4 != 0 {
		return 0, 0, errors.New("PCM不是完整float32采样")
	}
	var nonzero int64
	var peak float64
	for offset := 0; offset < len(data); offset += 4 {
		value := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[offset:])))
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, 0, errors.New("原生PCM含非有限采样，不能计为有效媒体输出")
		}
		if value != 0 {
			nonzero++
			peak = max(peak, math.Abs(value))
		}
	}
	return nonzero, peak, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func writeReport(path string, value report) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".preview-benchmark-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
