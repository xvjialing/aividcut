package media

import (
	"aividcut/internal/managedprocess"
	"aividcut/internal/previewproxy"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"math"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"
)

type proxyMetadata struct {
	Streams []struct {
		PixelFormat string `json:"pix_fmt"`
		Transfer    string `json:"color_transfer"`
		FieldOrder  string `json:"field_order"`
		Tags        struct {
			AlphaMode string `json:"alpha_mode"`
		} `json:"tags"`
		Width    int    `json:"width"`
		Height   int    `json:"height"`
		TimeBase string `json:"time_base"`
		SAR      string `json:"sample_aspect_ratio"`
		SideData []struct {
			Type   string `json:"side_data_type"`
			Matrix string `json:"displaymatrix"`
		} `json:"side_data_list"`
		Disposition struct {
			Attached int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
	Format struct {
		Start string `json:"start_time"`
	} `json:"format"`
}

func (t Toolchain) proxyMetadata(ctx context.Context, path string) (proxyMetadata, error) {
	var value proxyMetadata
	out, err := runOutput(ctx, t.FFprobePath, "-v", "error", "-protocol_whitelist", "file,pipe", "-format_whitelist", "mov,matroska,webm,avi", "-select_streams", "v:0", "-show_entries", "stream=width,height,time_base,sample_aspect_ratio,pix_fmt,color_transfer,field_order:stream_tags=alpha_mode:stream_side_data=side_data_type,displaymatrix:stream_disposition=attached_pic:format=start_time", "-of", "json", "-i", path)
	if err != nil {
		return value, errors.New("无法读取代理视频时基/方向元数据")
	}
	if len(out) > 256<<10 || json.Unmarshal(out, &value) != nil || len(value.Streams) != 1 {
		return value, errors.New("视频元数据缺失或超过预算")
	}
	s := value.Streams[0]
	if s.Width < 2 || s.Height < 2 || int64(s.Width)*int64(s.Height) > 64<<20 || s.Disposition.Attached != 0 {
		return value, errors.New("代理需要有效的普通视频流，不能使用封面图")
	}
	tb, ok := new(big.Rat).SetString(s.TimeBase)
	if !ok || tb.Sign() <= 0 || !tb.Num().IsInt64() || !tb.Denom().IsInt64() || tb.Num().Int64() > 1_000_000_000 || tb.Denom().Int64() > 1_000_000_000 {
		return value, errors.New("视频时基无法精确保留")
	}
	return value, nil
}
func proxyOrigin(meta proxyMetadata) (int64, error) {
	value, err := strconv.ParseFloat(meta.Format.Start, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > 86400 {
		return 0, errors.New("原格式起点缺失，不能证明代理时间映射")
	}
	return int64(math.Round(value * 1e6)), nil
}
func proxyMatrix(meta proxyMetadata) string {
	for _, s := range meta.Streams[0].SideData {
		if s.Type == "Display Matrix" {
			return strings.Join(strings.Fields(s.Matrix), " ")
		}
	}
	return ""
}
func proxyAspect(meta proxyMetadata) (*big.Rat, error) {
	s := meta.Streams[0]
	sar := strings.ReplaceAll(s.SAR, ":", "/")
	if sar == "" || sar == "N/A" {
		sar = "1/1"
	}
	r, ok := new(big.Rat).SetString(sar)
	if !ok || r.Sign() <= 0 {
		return nil, errors.New("视频像素比例无效")
	}
	return r.Mul(r, new(big.Rat).SetFrac64(int64(s.Width), int64(s.Height))), nil
}

type proxyTimeline struct {
	digest       hash.Hash
	frames       int64
	last         *big.Rat
	lastDuration *big.Rat
	timeBase     *big.Rat
	meta         proxyMetadata
}

func (p *proxyTimeline) line(line string) error {
	fields := strings.Split(line, "|")
	values := map[string]string{}
	for _, field := range fields {
		parts := strings.SplitN(field, "=", 2)
		if len(parts) == 2 {
			values[parts[0]] = parts[1]
		}
	}
	if len(p.meta.Streams) == 1 {
		s := p.meta.Streams[0]
		w, _ := strconv.Atoi(values["width"])
		h, _ := strconv.Atoi(values["height"])
		if w != s.Width || h != s.Height {
			return errors.New("视频中途变更尺寸，当前代理未验收该源")
		}
		sar := values["sample_aspect_ratio"]
		if sar != "" && sar != "N/A" && s.SAR != "" && s.SAR != "N/A" && sar != s.SAR {
			return errors.New("视频中途变更像素比例，当前代理未验收该源")
		}
	}
	raw, exists := values["best_effort_timestamp"]
	if !exists {
		if strings.TrimSpace(line) == "" {
			return nil
		}
		return errors.New("代理帧记录缺失实际PTS")
	}
	pts, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || pts < -1e14 || pts > 1e14 {
		return errors.New("代理帧PTS无效")
	}
	duration, err := strconv.ParseInt(values["duration"], 10, 64)
	if err != nil || duration <= 0 {
		return errors.New("视频帧缺少真实持续时间，不能证明代理尾部")
	}
	at := new(big.Rat).Mul(new(big.Rat).SetInt64(pts), p.timeBase)
	if p.last != nil && at.Cmp(p.last) <= 0 {
		return errors.New("视频PTS未严格递增，无法建立可验证代理")
	}
	if p.last != nil {
		length := new(big.Rat).Sub(at, p.last)
		fmt.Fprintf(p.digest, "%s|%s\n", p.last.RatString(), length.RatString())
	}
	p.last = at
	p.lastDuration = new(big.Rat).Mul(new(big.Rat).SetInt64(duration), p.timeBase)
	p.frames++
	if p.frames > 1_000_000 {
		return errors.New("代理视频超过100万帧预算，请拆分源素材")
	}
	return nil
}
func (t Toolchain) proxyTimeline(ctx context.Context, path string, meta proxyMetadata) (int64, string, error) {
	tb, _ := new(big.Rat).SetString(meta.Streams[0].TimeBase)
	state := proxyTimeline{digest: sha256.New(), timeBase: tb, meta: meta}
	command := managedprocess.CommandContext(ctx, t.FFprobePath, "-v", "error", "-protocol_whitelist", "file,pipe", "-format_whitelist", "mov,matroska,webm,avi", "-select_streams", "v:0", "-show_frames", "-show_entries", "frame=best_effort_timestamp,duration,width,height,sample_aspect_ratio", "-of", "compact=p=0:nk=0", "-i", path)
	command.WaitDelay = 2 * time.Second
	stdout, err := command.StdoutPipe()
	if err != nil {
		return 0, "", err
	}
	var log proxyLog
	command.Stderr = &log
	if err = command.Start(); err != nil {
		return 0, "", errors.New("无法启动代理时间核查")
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	total := 0
	for scanner.Scan() {
		total += len(scanner.Bytes()) + 1
		if total > 128<<20 {
			err = errors.New("代理时间核查超过128MiB流式预算")
			break
		}
		if err = state.line(scanner.Text()); err != nil {
			break
		}
	}
	if err == nil {
		err = scanner.Err()
	}
	if err != nil {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	if ctx.Err() != nil {
		return 0, "", ctx.Err()
	}
	if err != nil {
		return 0, "", err
	}
	if waitErr != nil || state.frames == 0 {
		return 0, "", errors.New("代理逐帧时间核查未完成")
	}
	state.finish()
	return state.frames, hex.EncodeToString(state.digest.Sum(nil)), nil
}

// 代理进度和失败输出只保留有界尾部，不积累完整转码日志。
type proxyLog struct{ data []byte }

func (w *proxyLog) Write(b []byte) (int, error) {
	n := len(b)
	w.data = append(w.data, b...)
	if len(w.data) > 16<<10 {
		w.data = append([]byte{}, w.data[len(w.data)-(16<<10):]...)
	}
	return n, nil
}

// BuildPreviewProxy 保留绝对PTS；无音频代理的format.start_time可变化，由独立OriginUs覆盖解释。
func (t Toolchain) BuildPreviewProxy(ctx context.Context, path, output, profile string, progress func(string)) (previewproxy.Proof, error) {
	var result previewproxy.Proof
	if err := ctx.Err(); err != nil {
		return result, err
	}
	edge, err := previewproxy.Edge(profile)
	if err != nil {
		return result, err
	}
	if t.FFmpegPath == "" || t.FFprobePath == "" {
		return result, errors.New("视频代理需要FFmpeg和ffprobe")
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Hour)
	defer cancel()
	meta, err := t.proxyMetadata(ctx, path)
	if err != nil {
		return result, err
	}
	stream := meta.Streams[0]
	if int64(stream.Width)*int64(stream.Height) > 3840*2160 {
		return result, errors.New("当前原生源解码最多支持4K像素，代理不会扩大该范围")
	}
	alpha := map[string]bool{"rgba": true, "bgra": true, "argb": true, "abgr": true, "ya8": true, "ya16le": true, "ya16be": true, "pal8": true, "ayuv64le": true, "ayuv64be": true}
	if alpha[stream.PixelFormat] || strings.HasPrefix(stream.PixelFormat, "yuva") || strings.HasPrefix(stream.PixelFormat, "gbrap") || stream.Tags.AlphaMode == "1" {
		return result, errors.New("当前画面代理未验收透明视频，请使用原片")
	}
	if stream.Transfer == "smpte2084" || stream.Transfer == "arib-std-b67" || (stream.FieldOrder != "" && stream.FieldOrder != "unknown" && stream.FieldOrder != "progressive") {
		return result, errors.New("当前代理未验收HDR或隔行视频，请使用原片")
	}
	origin, err := proxyOrigin(meta)
	if err != nil {
		return result, err
	}
	frames, digest, err := t.proxyTimeline(ctx, path, meta)
	if err != nil {
		return result, err
	}
	if progress != nil {
		progress("正在生成仅含画面的代理")
	}
	s := meta.Streams[0]
	ratio := math.Min(1, float64(edge)/float64(max(s.Width, s.Height)))
	w, h := max(2, int(math.Round(float64(s.Width)*ratio/2))*2), max(2, int(math.Round(float64(s.Height)*ratio/2))*2)
	tb, _ := new(big.Rat).SetString(s.TimeBase)
	args := []string{"-v", "error", "-nostdin", "-xerror", "-protocol_whitelist", "file,pipe", "-format_whitelist", "mov,matroska,webm,avi", "-copyts", "-threads", "1", "-filter_threads", "1", "-noautorotate", "-i", path, "-map", "0:v:0", "-an", "-sn", "-dn", "-vf", fmt.Sprintf("scale=%d:%d", w, h), "-c:v", "libx264", "-threads", "1", "-preset", "fast", "-crf", "18", "-g", "1", "-bf", "0", "-pix_fmt", "yuv420p", "-fps_mode", "passthrough", "-enc_time_base", "demux", "-video_track_timescale", tb.Denom().String(), "-avoid_negative_ts", "disabled", "-fs", strconv.FormatInt(previewproxy.MaxFileBytes, 10), "-f", "mp4", "-n", output}
	command := managedprocess.CommandContext(ctx, t.FFmpegPath, args...)
	command.WaitDelay = 2 * time.Second
	var log proxyLog
	command.Stdout = &log
	command.Stderr = &log
	if err = command.Run(); err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, errors.New("代理转码失败，请核对libx264/scale能力和源文件")
	}
	if progress != nil {
		progress("正在核查代理逐帧时间与来源")
	}
	info, err := os.Stat(output)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > previewproxy.MaxFileBytes {
		return result, errors.New("代理文件不存在或超过2GiB预算")
	}
	encoded, err := t.proxyMetadata(ctx, output)
	if err != nil {
		return result, err
	}
	a, err := proxyAspect(meta)
	if err != nil {
		return result, err
	}
	b, err := proxyAspect(encoded)
	if err != nil {
		return result, err
	}
	if proxyMatrix(meta) != proxyMatrix(encoded) || a.Cmp(b) != 0 || encoded.Streams[0].Width != w || encoded.Streams[0].Height != h {
		return result, errors.New("代理显示方向/比例或尺寸未能保持，未采用该代理")
	}
	count, actual, err := t.proxyTimeline(ctx, output, encoded)
	if err != nil {
		return result, err
	}
	if count != frames || actual != digest {
		return result, errors.New("代理逐帧PTS/时长与原视频不一致，未采用该代理")
	}
	fingerprint, err := fingerprint(ctx, output)
	if err != nil {
		return result, err
	}
	if progress != nil {
		progress("代理时间与文件核查完成")
	}
	return previewproxy.Proof{OriginUs: origin, Frames: frames, TimelineSHA256: digest, Width: w, Height: h, ProxyFingerprint: fingerprint, Bytes: info.Size()}, nil
}

// 只用于受控测试和内部核查，不读取用户工程中的代理路径。
func (t Toolchain) VerifyProxyTiming(ctx context.Context, path string) (int64, string, error) {
	meta, err := t.proxyMetadata(ctx, path)
	if err != nil {
		return 0, "", err
	}
	return t.proxyTimeline(ctx, path, meta)
}

// PreviewProxySourceOrigin 冷缓存核对运行时所需的原格式起点，避免信任被修改的清单字段。
func (t Toolchain) PreviewProxySourceOrigin(ctx context.Context, path string) (int64, error) {
	meta, err := t.proxyMetadata(ctx, path)
	if err != nil {
		return 0, err
	}
	return proxyOrigin(meta)
}

// 非末帧按下一实际PTS定义展示区间；B帧包duration不能替代画面保持规则。
func (p *proxyTimeline) finish() {
	if p.last != nil {
		fmt.Fprintf(p.digest, "%s|%s\n", p.last.RatString(), p.lastDuration.RatString())
	}
}
