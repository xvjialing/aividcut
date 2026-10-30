package media

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"aividcut/internal/editor"
)

type sourceEntry[T any] struct {
	ready   chan struct{}
	cancel  context.CancelFunc
	waiters int
	info    os.FileInfo
	tile    T
	err     error
	stamp   uint64
}

// sourceReader共享只读派生结果与有界提取；不持有工程权威状态。
type sourceReader[T any] struct {
	mu      sync.Mutex
	clone   func(T) T
	label   string
	entries map[string]*sourceEntry[T]
	slots   chan struct{}
	clock   uint64
	closed  bool
	wg      sync.WaitGroup
	extract func(context.Context, editor.Asset, int64) (T, error)
}

func newSourceReader[T any](extract func(context.Context, editor.Asset, int64) (T, error), clone func(T) T, label string) *sourceReader[T] {
	return &sourceReader[T]{entries: map[string]*sourceEntry[T]{}, slots: make(chan struct{}, 2), extract: extract, clone: clone, label: label}
}
func sameWaveFile(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// SameSourceFile 用于已核对指纹的源证据检查，不替代首次完整SHA核查。
func SameSourceFile(a, b os.FileInfo) bool { return sameWaveFile(a, b) }

func sourceDone[T any](e *sourceEntry[T]) bool {
	select {
	case <-e.ready:
		return true
	default:
		return false
	}
}

func (r *sourceReader[T]) Read(ctx context.Context, asset editor.Asset, index int64) (T, error) {
	if err := ctx.Err(); err != nil {
		return *new(T), err
	}
	info, err := os.Stat(asset.Path)
	if err != nil || !info.Mode().IsRegular() {
		return *new(T), errors.New(r.label + "源文件不可读取，请重新定位素材")
	}
	key := fmt.Sprintf("%s\x00%s\x00%s\x00%d", asset.ID, asset.Fingerprint, asset.Path, index)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return *new(T), errors.New(r.label + "读取已关闭")
	}
	e := r.entries[key]
	if e != nil && !sameWaveFile(e.info, info) {
		delete(r.entries, key)
		e.cancel()
		e = nil
	}
	if e == nil {
		pending := 0
		for _, entry := range r.entries {
			if !sourceDone(entry) {
				pending++
			}
		}
		if pending >= 128 {
			r.mu.Unlock()
			return *new(T), errors.New(r.label + "等待队列已满，请稍后重试")
		}
		child, cancel := context.WithCancel(context.Background())
		e = &sourceEntry[T]{ready: make(chan struct{}), cancel: cancel, info: info}
		r.entries[key] = e
		r.wg.Add(1)
		go r.load(child, key, e, asset, index)
	}
	e.waiters++
	r.clock++
	e.stamp = r.clock
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		e.waiters--
		if e.waiters == 0 && !sourceDone(e) {
			e.cancel()
			if r.entries[key] == e {
				delete(r.entries, key)
			}
		}
	}()
	select {
	case <-ctx.Done():
		return *new(T), ctx.Err()
	case <-e.ready:
		if err := ctx.Err(); err != nil {
			return *new(T), err
		}
		tile := e.tile
		tile = r.clone(tile)
		return tile, e.err
	}
}
func (r *sourceReader[T]) load(ctx context.Context, key string, e *sourceEntry[T], asset editor.Asset, index int64) {
	defer r.wg.Done()
	var tile T
	var err error
	select {
	case r.slots <- struct{}{}:
		if ctx.Err() == nil {
			tile, err = r.extract(ctx, asset, index)
		} else {
			err = ctx.Err()
		}
		<-r.slots
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err == nil {
		after, statErr := os.Stat(asset.Path)
		if statErr != nil || !sameWaveFile(e.info, after) {
			err = errors.New("源文件已变化，" + r.label + "缓存失效")
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e.tile, e.err = tile, err
	close(e.ready)
	e.cancel()
	if err != nil || r.closed {
		if r.entries[key] == e {
			delete(r.entries, key)
		}
		return
	}
	for {
		count := 0
		var oldest *sourceEntry[T]
		var oldestKey string
		for k, entry := range r.entries {
			if sourceDone(entry) {
				count++
				if oldest == nil || entry.stamp < oldest.stamp {
					oldest, oldestKey = entry, k
				}
			}
		}
		if count <= 128 {
			break
		}
		delete(r.entries, oldestKey)
	}
}
func (r *sourceReader[T]) Close() {
	r.mu.Lock()
	r.closed = true
	for _, e := range r.entries {
		e.cancel()
	}
	r.entries = map[string]*sourceEntry[T]{}
	r.mu.Unlock()
	r.wg.Wait()
}
