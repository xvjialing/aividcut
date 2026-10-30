//go:build (!darwin && !windows) || (darwin && !cgo)

package processstats

import (
	"context"
	"errors"
)

type platformReader struct{}

func (platformReader) Enumerate(context.Context) ([]Identity, error) {
	return nil, errors.New("当前平台或构建未启用原生进程资源读取")
}
func (platformReader) Read(context.Context, Identity) (Counters, error) {
	return Counters{}, errors.New("当前平台或构建未启用原生进程资源读取")
}
