package application

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"aividcut/internal/editor"
)

func TestTimedSpeechCaptionsVerifyRelinkedProjectAudioRatherThanReceiptCopy(t *testing.T) {
	service, calls, summary := timedApplication(t, nil)
	if _, err := service.ImportSpeechAudio(context.Background(), summary.ID, ImportSpeechAudioOptions{AddToTimeline: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(summary.Audio.Path)
	if err != nil {
		t.Fatal(err)
	}
	relocated := filepath.Join(t.TempDir(), "relocated-voice.mp3")
	if err := os.WriteFile(relocated, data, 0600); err != nil {
		t.Fatal(err)
	}
	// 使用已知相同MP3的本地指纹探测夹具，通过真正的重定位核心路径提交。
	if _, err := service.relinkAssetWithProbe(context.Background(), summary.Audio.ID, relocated, func(ctx context.Context, path string) (editor.Asset, error) {
		fingerprint, err := fingerprintRelinkFile(ctx, path)
		asset := *summary.Audio
		asset.Path, asset.Fingerprint = path, fingerprint
		return asset, err
	}); err != nil {
		t.Fatal(err)
	}
	// 用户当前工程引用的副本随后发生内容变化；任务原始receipt音频保持完好。
	data[len(data)-1] ^= 1
	if err := os.WriteFile(relocated, data, 0600); err != nil {
		t.Fatal(err)
	}
	before := service.GetBootstrap().Project
	if _, err := service.ImportSpeechAudio(context.Background(), summary.ID, ttsOptions()); err == nil {
		t.Error("只校验任务原音频就为已变更的工程重定位音频生成了绑定字幕")
	}
	after := service.GetBootstrap().Project
	if after.Revision != before.Revision || projectHash(after) != projectHash(before) || len(after.Transcripts) != 0 {
		t.Error("拒绝原音频不一致时仍接纳了转写、字幕或修改了撤销历史")
	}
	if calls.Load() != 1 {
		t.Error("本地导入核查不应重新配音")
	}
}
