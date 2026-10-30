package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"

	"aividcut/internal/editor"
	"aividcut/internal/projectstore"
)

func createImages(directory string, clips int) error {
	if directory == "" || clips < 1 || clips > 10000 {
		return errors.New("静态图负载需要新目录和1–10000个片段")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	if err = os.Mkdir(absolute, 0700); err != nil {
		return err
	}
	path := filepath.Join(absolute, "own-grid.png")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	pixels := image.NewRGBA(image.Rect(0, 0, 64, 36))
	for y := 0; y < 36; y++ {
		for x := 0; x < 64; x++ {
			pixels.SetRGBA(x, y, color.RGBA{R: uint8(x * 3), G: uint8(y * 6), B: uint8((x/8 + y/6) % 2 * 180), A: 255})
		}
	}
	encodeErr := png.Encode(f, pixels)
	closeErr := f.Close()
	if encodeErr != nil || closeErr != nil {
		return errors.Join(encodeErr, closeErr)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	p := editor.NewProject("静态图逐帧图管理负载")
	p.Settings.Width, p.Settings.Height, p.Settings.FPSNum, p.Settings.FPSDen = 320, 180, 30000, 1001
	p.Assets = []editor.Asset{{ID: "own-grid", Name: "自建格图", Kind: "image", Path: path, Width: 64, Height: 36, Fingerprint: "sha256:" + hex.EncodeToString(sum[:])}}
	for i := 0; i < clips; i++ {
		clip := editor.DefaultClip(fmt.Sprintf("cut-%d", i), "track-video-1", "image")
		clip.AssetID = "own-grid"
		clip.StartFrame, clip.DurationFrames = int64(i), 1
		clip.Transform.X = float64(i%7) * 2
		p.Clips = append(p.Clips, clip)
	}
	if err = projectstore.Save(filepath.Join(absolute, "split.aividcut"), p); err != nil {
		return err
	}
	fmt.Printf("已生成%d个静态图片段（每段一帧，不代表自然视频解码负载）：%s\n", clips, absolute)
	return nil
}
