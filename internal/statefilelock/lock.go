// Package statefilelock 以系统锁保证应用数据目录只有一个写入进程。
package statefilelock

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

var ErrInUse = errors.New("应用数据目录正由另一个进程使用，请关闭该实例或选择独立数据目录")

type Lock struct {
	file *os.File
	once sync.Once
	err  error
}

func Acquire(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return &Lock{file: f}, nil
}

func (l *Lock) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		l.err = unlockFile(l.file)
		if err := l.file.Close(); l.err == nil {
			l.err = err
		}
	})
	return l.err
}
