package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// 同父目录原生排他重命名为首选；不支持时预留私有目录后逐个排他复制。
func publishSpeechDirectory(ctx context.Context, stage, target string, parts []SpeechAudioPart, ops commitOperations) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ops.renameNoReplace(stage, target); err == nil {
		return nil
	} else if !errors.Is(err, errNoReplaceUnsupported) && !errors.Is(err, errCommitCrossDevice) {
		return err
	}
	if err := os.Mkdir(target, 0700); err != nil {
		return err
	}
	owned, err := os.Stat(target)
	if err != nil {
		return err
	}
	type ownedFile struct {
		path string
		info os.FileInfo
	}
	var copied []ownedFile
	success := false
	defer func() {
		if success {
			return
		}
		current, err := os.Lstat(target)
		if err != nil || !os.SameFile(owned, current) {
			result = errors.Join(result, errors.New("语音输出目录被替换，保留其他操作的结果"))
			return
		}
		for _, value := range copied {
			result = errors.Join(result, removeOwned(value.path, value.info))
		}
		// 只删除自己已知的文件，不递归清扫未知内容。
		result = errors.Join(result, os.Remove(target))
	}()
	for _, part := range parts {
		path := filepath.Join(target, part.ID+".wav")
		if err := copyExclusive(ctx, filepath.Join(stage, part.ID+".wav"), path, ops.copy); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		copied = append(copied, ownedFile{path: path, info: info})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	success = true
	return nil
}
