//go:build !darwin && !linux && !windows

package statefilelock

import (
	"errors"
	"os"
)

func lockFile(*os.File) error   { return errors.New("当前平台尚未实现应用数据目录锁") }
func unlockFile(*os.File) error { return nil }
