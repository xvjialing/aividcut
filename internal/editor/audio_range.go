package editor

import (
	"math"
	"math/big"
)

// AudioRange 是源音轨半开区间；显示仍用工程帧，实际音频起止与其独立。
// samples的时钟副本必须匹配Asset实测元数据，不能从DurationUs捏造count。
type AudioRange struct {
	Unit           string `json:"unit"`
	Start          int64  `json:"start"`
	End            int64  `json:"end"`
	SampleRate     int    `json:"sampleRate,omitempty"`
	SourceOriginUs int64  `json:"sourceOriginUs,omitempty"`
	OffsetNum      int64  `json:"offsetNum,omitempty"`
	OffsetDen      int64  `json:"offsetDen,omitempty"`
}

// AudioTime 单位微秒，Num/Den保持样本/帧切口所需的精确分数。
type AudioTime struct {
	Num int64 `json:"num"`
	Den int64 `json:"den"`
}

func (t AudioTime) Rat() (*big.Rat, error) {
	if t.Den <= 0 {
		return nil, problem("audio_range", "音频时间分母必须为正")
	}
	return new(big.Rat).SetFrac64(t.Num, t.Den), nil
}

type ResolvedAudioRange struct {
	Unit            string    `json:"unit"`
	SourceStart     AudioTime `json:"sourceStart"`
	SourceEnd       AudioTime `json:"sourceEnd"`
	TimelineStart   AudioTime `json:"timelineStart"`
	TimelineEnd     AudioTime `json:"timelineEnd"`
	StartSample     int64     `json:"startSample,omitempty"`
	EndSample       int64     `json:"endSample,omitempty"`
	SampleRate      int       `json:"sampleRate,omitempty"`
	MeasuredSamples bool      `json:"measuredSamples"`
	Empty           bool      `json:"empty"`
}

func sameAudioRange(a, b *AudioRange) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func audioTime(value *big.Rat) (AudioTime, error) {
	if !value.Num().IsInt64() || !value.Denom().IsInt64() {
		return AudioTime{}, problem("audio_range", "音频分数时间超出可表示范围")
	}
	return AudioTime{value.Num().Int64(), value.Denom().Int64()}, nil
}
func audioOffset(value *AudioRange) (*big.Rat, error) {
	den := value.OffsetDen
	if den == 0 && value.OffsetNum == 0 {
		den = 1
	}
	if den <= 0 {
		return nil, problem("audio_range", "音频偏移分母无效")
	}
	return new(big.Rat).SetFrac64(value.OffsetNum, den), nil
}
func audioRangeSource(value *AudioRange) (*big.Rat, *big.Rat, error) {
	if value == nil || value.Start < 0 || value.End < value.Start {
		return nil, nil, problem("audio_range", "音频源区间无效")
	}
	switch value.Unit {
	case "us":
		if value.SampleRate != 0 || value.SourceOriginUs != 0 {
			return nil, nil, problem("audio_range", "微秒区间不能夹带自报样本时钟")
		}
		return new(big.Rat).SetInt64(value.Start), new(big.Rat).SetInt64(value.End), nil
	case "samples":
		if value.SampleRate < 1 || value.SampleRate > 1_000_000 || value.SourceOriginUs < 0 {
			return nil, nil, problem("audio_range", "音频源样本时钟无效")
		}
		start := new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(value.Start), big.NewInt(1_000_000)), big.NewInt(int64(value.SampleRate)))
		start.Add(start, new(big.Rat).SetInt64(value.SourceOriginUs))
		end := new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(value.End), big.NewInt(1_000_000)), big.NewInt(int64(value.SampleRate)))
		end.Add(end, new(big.Rat).SetInt64(value.SourceOriginUs))
		return start, end, nil
	default:
		return nil, nil, problem("audio_range", "音频区间只支持微秒或实测样本")
	}
}

func frameTimeRat(frames int64, settings Settings) *big.Rat {
	return new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(frames), big.NewInt(1_000_000*int64(settings.FPSDen))), big.NewInt(int64(settings.FPSNum)))
}
func clipSpeed(clip Clip) *big.Rat {
	return new(big.Rat).SetFrac64(int64(clip.SpeedNum), int64(clip.SpeedDen))
}
func ceilRat(value *big.Rat) (int64, error) {
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(value.Num(), value.Denom(), r)
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, problem("audio_range", "音频区间溢出")
	}
	return q.Int64(), nil
}
func floorRat(value *big.Rat) (int64, error) {
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(value.Num(), value.Denom(), r)
	if r.Sign() < 0 {
		q.Sub(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, problem("audio_range", "音频区间溢出")
	}
	return q.Int64(), nil
}

func validateAudioAsset(asset Asset) error {
	if asset.AudioSampleRate < 0 || asset.AudioSampleRate > 1_000_000 || asset.AudioStartUs < 0 {
		return problem("audio_range", "素材音轨时钟元数据无效")
	}
	if asset.AudioSampleCount != nil && (*asset.AudioSampleCount < 0 || asset.AudioSampleRate == 0 || !asset.HasAudio) {
		return problem("audio_range", "实测样本数需要真实音轨与采样率，不能由时长代替")
	}
	if !asset.HasAudio && (asset.AudioSampleRate != 0 || asset.AudioStartUs != 0) {
		return problem("audio_range", "没有音轨的素材不能声明样本时钟")
	}
	return nil
}

func resolveAudioRats(clip Clip, asset Asset, settings Settings) (*big.Rat, *big.Rat, *big.Rat, *big.Rat, error) {
	if err := ValidateSettings(settings); err != nil {
		return nil, nil, nil, nil, err
	}
	if err := validateAudioAsset(asset); err != nil {
		return nil, nil, nil, nil, err
	}
	if !validKind(clip.Kind, "audio", "video") || !asset.HasAudio || clip.SpeedNum <= 0 || clip.SpeedDen <= 0 || clip.SourceInUs < 0 || clip.StartFrame < 0 || clip.DurationFrames <= 0 || clip.StartFrame > math.MaxInt64-clip.DurationFrames {
		return nil, nil, nil, nil, problem("audio_range", "音频区间需要有效音轨、工程位置与速度")
	}
	start := new(big.Rat).SetInt64(clip.SourceInUs)
	end := new(big.Rat).Mul(frameTimeRat(clip.DurationFrames, settings), clipSpeed(clip))
	end.Add(end, start)
	offset := new(big.Rat)
	if clip.AudioRange != nil {
		var err error
		start, end, err = audioRangeSource(clip.AudioRange)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		offset, err = audioOffset(clip.AudioRange)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if clip.AudioRange.Unit == "samples" {
			if asset.AudioSampleCount == nil || clip.AudioRange.SampleRate != asset.AudioSampleRate || clip.AudioRange.SourceOriginUs != asset.AudioStartUs || clip.AudioRange.End > *asset.AudioSampleCount {
				return nil, nil, nil, nil, problem("audio_range", "源样本范围/时钟没有匹配素材实测证据")
			}
		} else {
			_, availableEnd := audioAssetBounds(asset)
			if clip.AudioRange.Start < asset.AudioStartUs || end.Cmp(availableEnd) > 0 {
				return nil, nil, nil, nil, problem("source_out_of_range", "音频微秒区间超出真实音轨覆盖")
			}
		}
		anchor := new(big.Rat).Sub(start, new(big.Rat).Mul(offset, clipSpeed(clip)))
		floor, err := floorRat(anchor)
		if err != nil || clip.Kind == "audio" && floor != clip.SourceInUs {
			return nil, nil, nil, nil, problem("audio_range", "音频源区间与片段锚点不一致")
		}
	}
	timelineStart := new(big.Rat).Add(frameTimeRat(clip.StartFrame, settings), offset)
	timelineEnd := new(big.Rat).Quo(new(big.Rat).Sub(end, start), clipSpeed(clip))
	timelineEnd.Add(timelineEnd, timelineStart)
	if start.Cmp(end) != 0 {
		if timelineStart.Sign() < 0 {
			return nil, nil, nil, nil, problem("audio_range", "真实声音不能落在负工程时间")
		}
		quantum := new(big.Rat).SetInt64(1)
		if clip.AudioRange != nil && clip.AudioRange.Unit == "samples" {
			quantum.SetFrac64(1_000_000, int64(clip.AudioRange.SampleRate))
		}
		quantum.Quo(quantum, clipSpeed(clip))
		overhang := new(big.Rat).Sub(timelineEnd, frameTimeRat(clip.StartFrame+clip.DurationFrames, settings))
		if overhang.Sign() > 0 && overhang.Cmp(quantum) >= 0 {
			return nil, nil, nil, nil, problem("audio_range", "声音末端超出显示区间，不能用占位长度循环或补齐音频")
		}
		if offset.Sign() < 0 && new(big.Rat).Neg(offset).Cmp(quantum) >= 0 {
			return nil, nil, nil, nil, problem("audio_range", "负子帧偏移只能保留共同切口的一个源单位误差")
		}
	}
	return start, end, timelineStart, timelineEnd, nil
}

func ResolveAudioRange(clip Clip, asset Asset, settings Settings) (ResolvedAudioRange, error) {
	start, end, timelineStart, timelineEnd, err := resolveAudioRats(clip, asset, settings)
	if err != nil {
		return ResolvedAudioRange{}, err
	}
	result := ResolvedAudioRange{Unit: "us", Empty: start.Cmp(end) == 0}
	if clip.AudioRange != nil {
		result.Unit = clip.AudioRange.Unit
		if result.Unit == "samples" {
			result.StartSample, result.EndSample, result.SampleRate, result.MeasuredSamples = clip.AudioRange.Start, clip.AudioRange.End, clip.AudioRange.SampleRate, true
		}
	}
	if result.SourceStart, err = audioTime(start); err != nil {
		return result, err
	}
	if result.SourceEnd, err = audioTime(end); err != nil {
		return result, err
	}
	if result.TimelineStart, err = audioTime(timelineStart); err != nil {
		return result, err
	}
	if result.TimelineEnd, err = audioTime(timelineEnd); err != nil {
		return result, err
	}
	return result, nil
}

// AudioClipToSourceEnd 是显式完整音频导入：只使用已有实测样本或声明微秒末端。
func AudioClipToSourceEnd(clip Clip, asset Asset, settings Settings) (Clip, error) {
	if clip.Kind != "audio" || clip.AssetID != asset.ID || !asset.HasAudio || clip.SourceInUs < 0 || clip.SpeedNum <= 0 || clip.SpeedDen <= 0 {
		return Clip{}, problem("audio_range", "完整音频导入需要匹配的真实音轨与有效入点/速度")
	}
	if err := ValidateSettings(settings); err != nil {
		return Clip{}, err
	}
	if err := validateAudioAsset(asset); err != nil {
		return Clip{}, err
	}
	value := AudioRange{Unit: "us", Start: max(clip.SourceInUs, asset.AudioStartUs), End: asset.DurationUs, OffsetDen: 1}
	if asset.AudioSampleCount != nil {
		start := new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(clip.SourceInUs-asset.AudioStartUs), big.NewInt(int64(asset.AudioSampleRate))), big.NewInt(1_000_000))
		index, err := ceilRat(start)
		if err != nil {
			return Clip{}, err
		}
		index = max(int64(0), min(*asset.AudioSampleCount, index))
		value = AudioRange{Unit: "samples", Start: index, End: *asset.AudioSampleCount, SampleRate: asset.AudioSampleRate, SourceOriginUs: asset.AudioStartUs, OffsetDen: 1}
	}
	if value.End <= value.Start {
		return Clip{}, problem("audio_range", "源入点之后没有真实音频余量")
	}
	start, end, err := audioRangeSource(&value)
	if err != nil {
		return Clip{}, err
	}
	offset := new(big.Rat).Quo(new(big.Rat).Sub(start, new(big.Rat).SetInt64(clip.SourceInUs)), clipSpeed(clip))
	timeValue, err := audioTime(offset)
	if err != nil {
		return Clip{}, err
	}
	value.OffsetNum, value.OffsetDen = timeValue.Num, timeValue.Den
	length := new(big.Rat).Quo(new(big.Rat).Sub(end, start), clipSpeed(clip))
	length.Add(length, offset)
	frames := new(big.Rat).Quo(length, frameTimeRat(1, settings))
	count, err := ceilRat(frames)
	if err != nil || count < 1 {
		return Clip{}, problem("audio_range", "真实音频长度无法表示为正工程帧占位")
	}
	clip = cloneClip(clip)
	clip.DurationFrames = count
	clip.AudioRange = &value
	if _, err := ResolveAudioRange(clip, asset, settings); err != nil {
		return Clip{}, err
	}
	return clip, nil
}
