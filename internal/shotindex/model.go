// Package shotindex 组织视觉变化候选区间，不把它解释为语义镜头或自动剪辑命令。
package shotindex

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
)

const Version = "scdet-source-v1"
const MaxShots = 2000

type Config struct {
	Threshold     int   `json:"threshold"`
	MinDurationUs int64 `json:"minDurationUs"`
}

func Normalize(c Config) (Config, error) {
	if c.Threshold == 0 {
		c.Threshold = 10
	}
	if c.MinDurationUs == 0 {
		c.MinDurationUs = 250000
	}
	if c.Threshold < 1 || c.Threshold > 50 || c.MinDurationUs < 1000 || c.MinDurationUs > 5_000_000 {
		return c, errors.New("镜头阈值需1–50，最小间隔需1毫秒–5秒")
	}
	return c, nil
}

type Shot struct {
	ID       string `json:"id"`
	StartUs  int64  `json:"startUs"`
	EndUs    int64  `json:"endUs"`
	SampleUs int64  `json:"sampleUs"`
}
type Index struct {
	Version           string `json:"version"`
	Key               string `json:"key"`
	SourceFingerprint string `json:"sourceFingerprint"`
	SourceDurationUs  int64  `json:"sourceDurationUs"`
	DetectorVersion   string `json:"detectorVersion"`
	Config            Config `json:"config"`
	CoverageStartUs   int64  `json:"coverageStartUs"`
	CoverageEndUs     int64  `json:"coverageEndUs"`
	DecodedFrames     int64  `json:"decodedFrames"`
	RawCutCount       int    `json:"rawCutCount"`
	Shots             []Shot `json:"shots"`
}

func Build(key, fp, decoder string, duration, start, end, frames int64, cuts []int64, config Config) (Index, error) {
	c, err := Normalize(config)
	if err != nil {
		return Index{}, err
	}
	if key == "" || fp == "" || decoder == "" || duration <= 0 || start < 0 || end <= start || end > duration+100000 || frames <= 0 || len(cuts) >= MaxShots {
		return Index{}, errors.New("镜头检测缺少一致的视频覆盖或超过候选上限")
	}
	end = min(end, duration)
	if end <= start {
		return Index{}, errors.New("视频覆盖不在登记源范围")
	}
	points := append([]int64{}, cuts...)
	sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })
	boundaries := []int64{start}
	for _, point := range points {
		if point < start || point >= end {
			return Index{}, errors.New("候选边界不在视频覆盖范围")
		}
		if point-boundaries[len(boundaries)-1] >= c.MinDurationUs && end-point >= c.MinDurationUs {
			boundaries = append(boundaries, point)
		}
	}
	boundaries = append(boundaries, end)
	index := Index{Version: Version, Key: key, SourceFingerprint: fp, SourceDurationUs: duration, DetectorVersion: decoder, Config: c, CoverageStartUs: start, CoverageEndUs: end, DecodedFrames: frames, RawCutCount: len(cuts), Shots: []Shot{}}
	for i := 1; i < len(boundaries); i++ {
		a, b := boundaries[i-1], boundaries[i]
		index.Shots = append(index.Shots, Shot{ID: fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", key, a, b)))), StartUs: a, EndUs: b, SampleUs: a + (b-a)/2})
	}
	if err = index.Validate(); err != nil {
		return Index{}, err
	}
	return index, nil
}
func (i Index) Validate() error {
	c, err := Normalize(i.Config)
	if err != nil || c != i.Config || i.Version != Version || len(i.Key) != 64 || i.SourceFingerprint == "" || i.DetectorVersion == "" || i.SourceDurationUs <= 0 || i.CoverageStartUs < 0 || i.CoverageEndUs > i.SourceDurationUs || i.CoverageEndUs <= i.CoverageStartUs || i.DecodedFrames <= 0 || i.RawCutCount < 0 || i.RawCutCount >= MaxShots || len(i.Shots) < 1 || len(i.Shots) > MaxShots || i.RawCutCount < len(i.Shots)-1 || i.DecodedFrames < int64(len(i.Shots)) {
		return errors.New("镜头索引身份或覆盖无效")
	}
	end := i.CoverageStartUs
	for _, s := range i.Shots {
		expected := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", i.Key, s.StartUs, s.EndUs))))
		if s.ID != expected || s.StartUs != end || s.EndUs <= s.StartUs || s.SampleUs < s.StartUs || s.SampleUs >= s.EndUs {
			return errors.New("镜头区间不连续或取样不在段内")
		}
		end = s.EndUs
	}
	if end != i.CoverageEndUs {
		return errors.New("镜头索引没有覆盖完整解码范围")
	}
	return nil
}
