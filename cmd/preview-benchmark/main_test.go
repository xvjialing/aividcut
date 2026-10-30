package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/projectstore"
)

func TestBenchmarkNeverOverwritesProjectThroughAnotherName(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "project.aividcut")
	alias := filepath.Join(dir, "alias.json")
	if err := projectstore.Save(original, editor.NewProject("只读保护")); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, alias); err != nil {
		t.Skip("文件系统不能构造同一文件的另一个路径", err)
	}
	err = execute(context.Background(), configuration{project: original, output: alias, frames: 1, width: 1280, height: 720, timeout: time.Second, interval: time.Millisecond * 20})
	if err == nil {
		t.Fatal("同一输入文件被允许作为输出")
	}
	after, err := os.ReadFile(original)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("只读诊断修改了工程", err)
	}
}

func TestUnobservedResourceIsNullWithReason(t *testing.T) {
	s := resourceSampler{value: resourceReport{GoPID: 1, WorkerPID: 2}}
	value := s.snapshot()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["goSampledPeakRssBytes"] != nil || decoded["workerSampledPeakRssBytes"] != nil || decoded["reason"] == "" {
		t.Fatal("未观测资源被标为零", string(data))
	}
}

func TestLatencyPercentileKeepsRawSamplesAndUsesNearestRank(t *testing.T) {
	values := []float64{100, 1, 4, 2, 3}
	before := append([]float64{}, values...)
	if percentile(values, .5) != 3 || percentile(values, .95) != 100 || !reflect.DeepEqual(values, before) || math.IsNaN(percentile(nil, .95)) {
		t.Fatal(values)
	}
}

func TestSeekListKeepsOrderDuplicatesAndRejectsUnboundedOrBadInput(t *testing.T) {
	values, err := parseSeeks("20, 0,20,1")
	if err != nil || !reflect.DeepEqual(values, []int64{20, 0, 20, 1}) {
		t.Fatal("定位列表改变了实际冷热调用顺序", values, err)
	}
	for _, input := range []string{"1,", "1,,2", "-1", "NaN", "0.5", "9223372036854775808", strings.Repeat("0,", 128) + "0"} {
		if _, err := parseSeeks(input); err == nil {
			t.Fatal("非法或无界定位列表被接受", input)
		}
	}
}

func TestPCMObservationUsesBothChannelsAndRejectsNonfiniteValues(t *testing.T) {
	data := make([]byte, 16)
	for index, value := range []float32{0, -.25, .5, 0} {
		binary.LittleEndian.PutUint32(data[index*4:], math.Float32bits(value))
	}
	active, peak, err := observePCM(data)
	if err != nil || active != 2 || peak != .5 {
		t.Fatal("真实通道采样没有区分零/正负幅值", active, peak, err)
	}
	for _, value := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		binary.LittleEndian.PutUint32(data[4:], math.Float32bits(value))
		if _, _, err := observePCM(data); err == nil {
			t.Fatal("非法PCM被当作有效音频")
		}
	}
	if _, _, err := observePCM(data[:3]); err == nil {
		t.Fatal("不完整采样被接受")
	}
}
