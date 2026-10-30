package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func commitFixture(t *testing.T) (string, string, []byte) {
	t.Helper()
	dir := t.TempDir()
	source, output := filepath.Join(dir, "render.tmp"), filepath.Join(dir, "result.mp4")
	data := bytes.Repeat([]byte("已完成的媒体数据\n"), 20_000)
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	return source, output, data
}

func assertContent(t *testing.T, path string, expected []byte) {
	t.Helper()
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, expected) {
		t.Fatalf("文件内容不符：%s err=%v", path, err)
	}
}

func TestCommitNativeDoesNotOverwriteAndCanReplace(t *testing.T) {
	source, output, data := commitFixture(t)
	old := []byte("原来的完整输出")
	if err := os.WriteFile(output, old, 0600); err != nil {
		t.Fatal(err)
	}
	if err := commitOutput(context.Background(), source, output, false, defaultCommitOperations()); err == nil {
		t.Fatal("默认提交不应覆盖已有输出")
	}
	assertContent(t, output, old)
	assertContent(t, source, data)
	if err := commitOutput(context.Background(), source, output, true, defaultCommitOperations()); err != nil {
		t.Fatal(err)
	}
	assertContent(t, output, data)
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("原生成功移动后临时源应已消失")
	}
}

func TestCommitNoHardLinkNeeded(t *testing.T) {
	source, output, data := commitFixture(t)
	// 此路径没有调用硬链接；在支持 no-replace rename 的 exFAT 上同样适用。
	if err := commitOutput(context.Background(), source, output, false, defaultCommitOperations()); err != nil {
		t.Fatal(err)
	}
	assertContent(t, output, data)
}

func TestCommitFallbackCopiesExclusively(t *testing.T) {
	source, output, data := commitFixture(t)
	ops := defaultCommitOperations()
	ops.renameNoReplace = func(_, _ string) error { return errNoReplaceUnsupported }
	if err := commitOutput(context.Background(), source, output, false, ops); err != nil {
		t.Fatal(err)
	}
	assertContent(t, output, data)
	assertContent(t, source, data)
	if err := commitOutput(context.Background(), source, output, false, ops); !errors.Is(err, os.ErrExist) {
		t.Fatalf("排他复制应拒绝已有目标：%v", err)
	}
	assertContent(t, output, data)
}

func TestCommitFallbackConcurrentTargetIsPreserved(t *testing.T) {
	source, output, _ := commitFixture(t)
	other := []byte("另一任务先完成的文件")
	ops := defaultCommitOperations()
	ops.renameNoReplace = func(_, _ string) error {
		if err := os.WriteFile(output, other, 0600); err != nil {
			return err
		}
		return errNoReplaceUnsupported
	}
	if err := commitOutput(context.Background(), source, output, false, ops); !errors.Is(err, os.ErrExist) {
		t.Fatalf("并发目标不应被覆盖：%v", err)
	}
	assertContent(t, output, other)
}

func TestCommitFallbackCancellationRemovesPartialTarget(t *testing.T) {
	source, output, data := commitFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ops := defaultCommitOperations()
	ops.renameNoReplace = func(_, _ string) error { return errCommitCrossDevice }
	ops.copy = func(ctx context.Context, dst io.Writer, src io.Reader) error {
		if _, err := io.CopyN(dst, src, 23); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	}
	if err := commitOutput(ctx, source, output, false, ops); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消原因丢失：%v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("取消后不应留下排他复制的半成品")
	}
	assertContent(t, source, data)
}

func TestCommitFallbackWriteFailureRemovesPartial(t *testing.T) {
	source, output, _ := commitFixture(t)
	failure := errors.New("模拟目标磁盘写满")
	ops := defaultCommitOperations()
	ops.renameNoReplace = func(_, _ string) error { return errNoReplaceUnsupported }
	ops.copy = func(_ context.Context, dst io.Writer, src io.Reader) error {
		_, _ = io.CopyN(dst, src, 7)
		return failure
	}
	if err := commitOutput(context.Background(), source, output, false, ops); !errors.Is(err, failure) {
		t.Fatalf("写失败原因丢失：%v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("磁盘写失败后留下半成品")
	}
}

func TestCommitDoesNotDeleteConcurrentReplacementOnCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 默认文件共享模式可能禁止移动打开中的文件，需在实际共享模式下单独验证")
	}
	source, output, _ := commitFixture(t)
	ops := defaultCommitOperations()
	ops.renameNoReplace = func(_, _ string) error { return errNoReplaceUnsupported }
	foreign := []byte("并发替换的用户文件")
	ops.copy = func(_ context.Context, dst io.Writer, src io.Reader) error {
		_, _ = io.CopyN(dst, src, 8)
		if err := os.Rename(output, output+".moved"); err != nil {
			return err
		}
		if err := os.WriteFile(output, foreign, 0600); err != nil {
			return err
		}
		return context.Canceled
	}
	if err := commitOutput(context.Background(), source, output, false, ops); !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "其他操作") {
		t.Fatalf("应报告清理身份冲突：%v", err)
	}
	assertContent(t, output, foreign)
}

func TestCommitFailedReplacementKeepsOldOutput(t *testing.T) {
	source, output, data := commitFixture(t)
	old := []byte("需要保留的旧输出")
	if err := os.WriteFile(output, old, 0600); err != nil {
		t.Fatal(err)
	}
	ops := defaultCommitOperations()
	ops.renameReplace = func(_, _ string) error { return os.ErrPermission }
	if err := commitOutput(context.Background(), source, output, true, ops); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("应报告替换失败：%v", err)
	}
	assertContent(t, output, old)
	assertContent(t, source, data)
}

func TestCommitFallbackSuccessChecksDestinationIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 默认文件共享模式可能禁止移动打开中的文件")
	}
	source, output, _ := commitFixture(t)
	foreign := []byte("并发用户文件")
	ops := defaultCommitOperations()
	ops.renameNoReplace = func(_, _ string) error { return errNoReplaceUnsupported }
	ops.copy = func(ctx context.Context, dst io.Writer, src io.Reader) error {
		if err := copyContext(ctx, dst, src); err != nil {
			return err
		}
		if err := os.Rename(output, output+".moved"); err != nil {
			return err
		}
		return os.WriteFile(output, foreign, 0600)
	}
	if err := commitOutput(context.Background(), source, output, false, ops); err == nil || !strings.Contains(err.Error(), "其他操作") {
		t.Fatalf("复制完成时也应验证目标身份：%v", err)
	}
	assertContent(t, output, foreign)
}

func TestCommitCrossDeviceOverwriteStagesBeforeReplacement(t *testing.T) {
	source, _, data := commitFixture(t)
	output := filepath.Join(t.TempDir(), "old.mp4")
	old := []byte("旧输出")
	if err := os.WriteFile(output, old, 0600); err != nil {
		t.Fatal(err)
	}
	ops := defaultCommitOperations()
	replacements := 0
	ops.renameReplace = func(from, to string) error {
		replacements++
		if filepath.Dir(from) != filepath.Dir(to) {
			return errCommitCrossDevice
		}
		assertContent(t, output, old)
		return platformRenameReplace(from, to)
	}
	if err := commitOutput(context.Background(), source, output, true, ops); err != nil {
		t.Fatal(err)
	}
	if replacements != 2 {
		t.Fatalf("跨卷应先完成目标目录暂存再替换，实际 %d 次", replacements)
	}
	assertContent(t, output, data)
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(output), ".aividcut-commit-*"))
	if len(leftovers) > 0 {
		t.Fatal("成功提交后遗留跨卷暂存")
	}
}

func TestCommitCrossDeviceOverwriteCancellationPreservesOld(t *testing.T) {
	source, output, _ := commitFixture(t)
	old := []byte("取消后保持的旧输出")
	if err := os.WriteFile(output, old, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ops := defaultCommitOperations()
	ops.renameReplace = func(_, _ string) error { return errCommitCrossDevice }
	ops.copy = func(ctx context.Context, dst io.Writer, src io.Reader) error {
		_, _ = io.CopyN(dst, src, 13)
		cancel()
		return ctx.Err()
	}
	if err := commitOutput(ctx, source, output, true, ops); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消原因丢失：%v", err)
	}
	assertContent(t, output, old)
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(output), ".aividcut-commit-*"))
	if len(leftovers) > 0 {
		t.Fatal("取消后遗留跨卷暂存")
	}
}

func TestCancellationAfterCommitCannotUndoReplacement(t *testing.T) {
	source, output, data := commitFixture(t)
	if err := os.WriteFile(output, []byte("旧输出"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ops := defaultCommitOperations()
	ops.renameReplace = func(from, to string) error {
		err := platformRenameReplace(from, to)
		if err == nil {
			cancel()
		}
		return err
	}
	if err := commitOutput(ctx, source, output, true, ops); err != nil {
		t.Fatalf("已成功提交不能再被晚到的取消改成失败：%v", err)
	}
	assertContent(t, output, data)
}

func TestCommitRefusesSymlinkAndDirectory(t *testing.T) {
	source, output, _ := commitFixture(t)
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	if err := commitOutput(context.Background(), source, output, true, defaultCommitOperations()); err == nil {
		t.Fatal("不应覆盖目录")
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, output); err != nil {
		t.Skipf("当前环境不能创建符号链接：%v", err)
	}
	if err := commitOutput(context.Background(), source, output, true, defaultCommitOperations()); err == nil {
		t.Fatal("不应覆盖符号链接")
	}
}
