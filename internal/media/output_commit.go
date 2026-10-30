package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var (
	errCommitCrossDevice    = errors.New("文件提交跨越文件系统")
	errNoReplaceUnsupported = errors.New("文件系统不支持无覆盖重命名")
)

// 每个调用独立持有操作集合，测试可以注入文件系统失败而不产生全局竞态。
type commitOperations struct {
	renameNoReplace func(string, string) error
	renameReplace   func(string, string) error
	copy            func(context.Context, io.Writer, io.Reader) error
}

func defaultCommitOperations() commitOperations {
	return commitOperations{platformRenameNoReplace, platformRenameReplace, copyContext}
}

// commitOutput 的成功返回就是提交点；此后到达的取消请求不能回滚已替换的旧文件。
func commitOutput(ctx context.Context, temporary, output string, overwrite bool, ops commitOperations) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	source, err := os.OpenFile(temporary, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	info, statErr := source.Stat()
	if statErr != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		source.Close()
		return errors.New("待提交的媒体必须为非空普通文件")
	}
	syncErr := source.Sync()
	closeErr := source.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("同步媒体输出失败: %w", err)
	}
	if existing, err := os.Lstat(output); err == nil {
		if !existing.Mode().IsRegular() {
			return errors.New("输出目标不能是目录、符号链接或特殊文件")
		}
		if os.SameFile(info, existing) {
			return errors.New("不能将媒体输出提交到自身")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !overwrite {
		err := ops.renameNoReplace(temporary, output)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errNoReplaceUnsupported) && !errors.Is(err, errCommitCrossDevice) {
			return err
		}
		// exFAT 不要求硬链接。仅当原生无覆盖重命名不可用时，退回排他创建。
		return copyExclusive(ctx, temporary, output, ops.copy)
	}
	if err := ops.renameReplace(temporary, output); err == nil {
		return nil
	} else if !errors.Is(err, errCommitCrossDevice) {
		// 尤其在 Windows 共享/权限失败时，不先删除旧文件再重试。
		return err
	}
	// 跨卷覆盖先复制到目标目录的私有暂存，再用同卷替换提交。
	staging, err := copyStaging(ctx, temporary, filepath.Dir(output), ops.copy)
	if err != nil {
		return err
	}
	defer os.Remove(staging)
	if err := ctx.Err(); err != nil {
		return err
	}
	return ops.renameReplace(staging, output)
}

func copyExclusive(ctx context.Context, source, output string, copyFn func(context.Context, io.Writer, io.Reader) error) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	owned, err := out.Stat()
	closed, committed := false, false
	defer func() {
		if !closed {
			result = errors.Join(result, out.Close())
		}
		if !committed {
			result = errors.Join(result, removeOwned(output, owned))
		}
	}()
	if err != nil {
		return err
	}
	if err := copyFn(ctx, out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	closeErr := out.Close()
	closed = true
	if closeErr != nil {
		return closeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := os.Lstat(output)
	if err != nil {
		return err
	}
	if !os.SameFile(current, owned) {
		return errors.New("输出路径在复制期间已被其他操作替换")
	}
	committed = true
	return nil
}

func copyStaging(ctx context.Context, source, directory string, copyFn func(context.Context, io.Writer, io.Reader) error) (result string, err error) {
	in, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.CreateTemp(directory, ".aividcut-commit-*")
	if err != nil {
		return "", err
	}
	name := out.Name()
	succeeded, closed := false, false
	defer func() {
		if !closed {
			err = errors.Join(err, out.Close())
		}
		if !succeeded {
			err = errors.Join(err, os.Remove(name))
		}
	}()
	if err := copyFn(ctx, out, in); err != nil {
		return "", err
	}
	if err := out.Sync(); err != nil {
		return "", err
	}
	closeErr := out.Close()
	closed = true
	if closeErr != nil {
		return "", closeErr
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	succeeded = true
	return name, nil
}

func removeOwned(path string, owned os.FileInfo) error {
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("清理未完成输出失败: %w", err)
	}
	if owned == nil || !os.SameFile(current, owned) {
		return errors.New("输出路径已被其他操作替换，保留新文件并报告清理冲突")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("清理未完成输出失败: %w", err)
	}
	return nil
}

func copyContext(ctx context.Context, dst io.Writer, src io.Reader) error {
	buffer := make([]byte, 256<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := src.Read(buffer)
		if n > 0 {
			written, err := dst.Write(buffer[:n])
			if err != nil {
				return err
			}
			if written != n {
				return io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return ctx.Err()
		}
		if readErr != nil {
			return readErr
		}
	}
}

// PublishDerivedFile 复用已验证的无覆盖提交，供应用私有派生缓存使用。
func PublishDerivedFile(ctx context.Context, temporary, output string) error {
	return commitOutput(ctx, temporary, output, false, defaultCommitOperations())
}
