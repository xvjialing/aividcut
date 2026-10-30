// 创建无用户素材的可复现音频剪切负载，只允许写入新的目录。
package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"aividcut/internal/editor"
	"aividcut/internal/projectstore"
)

func main() {
	directory := flag.String("out", "", "必须尚不存在的输出目录")
	kind := flag.String("kind", "audio", "audio为真实连续音轨；image为静态图逐帧图管理负载")
	cuts := flag.Int("clips", 64, "派生片段数量，1到10000")
	seconds := flag.Int("seconds", 6, "实际合成音频秒数，1到300")
	rate := flag.Int("rate", 44100, "源采样率，44100或48000")
	speedN := flag.Int("speed-num", 1, "速度分子")
	speedD := flag.Int("speed-den", 1, "速度分母")
	flag.Parse()
	if *kind == "image" {
		if err := createImages(*directory, *cuts); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *kind != "audio" {
		fmt.Fprintln(os.Stderr, "负载类型仅支持audio/image")
		os.Exit(1)
	}
	if err := create(*directory, *cuts, *seconds, *rate, *speedN, *speedD); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func create(directory string, clips, seconds, rate, speedN, speedD int) error {
	if directory == "" || clips < 1 || clips > 10000 || seconds < 1 || seconds > 300 || rate != 44100 && rate != 48000 || speedN < 1 || speedD < 1 || speedN > 8 || speedD > 8 {
		return errors.New("需要新目录、有效片段数/时长、44.1或48kHz及1到8的速度分子分母")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	if err := os.Mkdir(absolute, 0700); err != nil {
		return fmt.Errorf("输出目录必须尚不存在：%w", err)
	}
	path := filepath.Join(absolute, "own-chirp.wav")
	count := int64(rate) * int64(seconds)
	if err := writeTone(path, rate, count); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	asset := editor.Asset{ID: "own-chirp", Name: "自建连续音轨", Kind: "audio", Path: path, DurationUs: count * 1_000_000 / int64(rate), HasAudio: true, AudioSampleRate: rate, AudioSampleCount: &count, Fingerprint: "sha256:" + hex.EncodeToString(hash.Sum(nil))}
	project := editor.NewProject("自建音频派生负载")
	project.Settings.Width, project.Settings.Height, project.Settings.FPSNum, project.Settings.FPSDen = 64, 36, 30000, 1001
	project.Assets = []editor.Asset{asset}
	clip := editor.DefaultClip("root-audio", "track-audio-1", "audio")
	clip.AssetID, clip.SourceInUs, clip.SpeedNum, clip.SpeedDen = asset.ID, 12345, speedN, speedD
	clip, err = editor.AudioClipToSourceEnd(clip, asset, project.Settings)
	if err != nil {
		return err
	}
	if clip.DurationFrames < int64(clips) {
		return errors.New("可用输出帧少于片段数量，请增加秒数或减少片段")
	}
	project.Clips = []editor.Clip{clip}
	if err := projectstore.Save(filepath.Join(absolute, "whole.aividcut"), project); err != nil {
		return err
	}
	engine, err := editor.NewEngine(project)
	if err != nil {
		return err
	}
	commands := make([]editor.Command, 0, clips-1)
	// 逆序切同一左段，在一个真实事务内生成负载，避免为夹具保存上万份历史。
	for i := clips - 1; i > 0; i-- {
		commands = append(commands, editor.Command{Type: "split_clip", ClipID: clip.ID, AtFrame: clip.DurationFrames * int64(i) / int64(clips)})
	}
	if len(commands) > 0 {
		if _, err = engine.Apply(editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: "fixture-splits", Commands: commands}); err != nil {
			return err
		}
	}
	if err := projectstore.Save(filepath.Join(absolute, "split.aividcut"), engine.Snapshot()); err != nil {
		return err
	}
	fmt.Printf("已生成 %d 个真实样本及 %d 个派生片段：%s\n", count, len(engine.Snapshot().Clips), absolute)
	return nil
}

func writeTone(path string, rate int, count int64) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(36+count*2))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], 1)
	binary.LittleEndian.PutUint32(header[24:], uint32(rate))
	binary.LittleEndian.PutUint32(header[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(header[32:], 2)
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(count*2))
	if _, err = file.Write(header); err != nil {
		return err
	}
	buffer := make([]byte, 8192)
	for start := int64(0); start < count; {
		n := min(int64(len(buffer)/2), count-start)
		for i := int64(0); i < n; i++ {
			seconds := float64(start+i) / float64(rate)
			value := int16(10000 * math.Sin(2*math.Pi*(337*seconds+31*seconds*seconds)))
			binary.LittleEndian.PutUint16(buffer[i*2:], uint16(value))
		}
		if _, err = file.Write(buffer[:n*2]); err != nil {
			return err
		}
		start += n
	}
	return file.Close()
}
