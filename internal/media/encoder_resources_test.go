package media

import (
	"aividcut/internal/editor"
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEncodingResourceWhitelistAndStandardCompatibility(t *testing.T) {
	for _, encoder := range []string{"", "libx264", "mpeg4", "h264_videotoolbox"} {
		for _, mode := range []string{"", "standard"} {
			args, err := EncodingResourceArgs(encoder, mode)
			if err != nil || len(args) != 0 {
				t.Fatal("标准档位改变旧参数", encoder, mode, args, err)
			}
		}
	}
	expected := []string{"-threads", "1", "-rc-lookahead", "4", "-x264-params", "sync-lookahead=0"}
	for _, encoder := range []string{"", "libx264"} {
		args, err := EncodingResourceArgs(encoder, "lower_memory")
		if err != nil || !reflect.DeepEqual(args, expected) {
			t.Fatal(args, err)
		}
	}
	for _, input := range [][2]string{{"libx264", "-preset ultrafast"}, {"mpeg4", "lower_memory"}, {"h264_videotoolbox", "lower_memory"}} {
		if _, err := EncodingResourceArgs(input[0], input[1]); err == nil {
			t.Fatal("不兼容/自由参数未拒绝", input)
		}
	}
}
func TestBadEncodingResourcesRejectedBeforeProcess(t *testing.T) {
	tools := Toolchain{FFmpegPath: filepath.Join(t.TempDir(), "不存在的工具")}
	settings := editor.NewProject("fixture").Settings
	if _, err := tools.StartRawVideo(context.Background(), settings, 640, 360, 1, "mpeg4", "unused.mp4", "lower_memory"); err == nil || err.Error() != "较低内存档位仅支持H.264软件编码，请改用标准档位" {
		t.Fatal("请求未在进程启动前拒绝", err)
	}
}
