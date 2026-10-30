package connections

import (
	"context"
	"errors"
	"runtime"
	"sync"

	"github.com/99designs/keyring"
)

const nativeServiceName = "com.aividcut.connections"

// NativeStore 惰性打开指定系统后端。不存在文件、pass、外部 CLI 或明文降级路径。
type NativeStore struct {
	mu      sync.Mutex
	backend keyring.BackendType
	ring    keyring.Keyring
}

func backendForOS(goos string) (keyring.BackendType, error) {
	switch goos {
	case "darwin":
		return keyring.KeychainBackend, nil
	case "windows":
		return keyring.WinCredBackend, nil
	default:
		return keyring.InvalidBackend, fail("secure_store_unsupported", "当前平台尚未提供受支持的系统凭据库，不会降级为明文文件或命令行存储")
	}
}

func NewNativeStore() (*NativeStore, error) {
	backend, err := backendForOS(runtime.GOOS)
	if err != nil {
		return nil, err
	}
	available := false
	for _, candidate := range keyring.AvailableBackends() {
		if candidate == backend {
			available = true
			break
		}
	}
	if !available {
		return nil, fail("secure_store_unavailable", "此构建不包含所需系统凭据后端，不会启用不安全替代存储")
	}
	return &NativeStore{backend: backend}, nil
}

func (store *NativeStore) Backend() string { return string(store.backend) }
func (store *NativeStore) String() string {
	return "connections.NativeStore{backend:" + string(store.backend) + "}"
}
func (store *NativeStore) GoString() string { return store.String() }

func (store *NativeStore) ringLocked() (keyring.Keyring, error) {
	if store.ring != nil {
		return store.ring, nil
	}
	ring, err := keyring.Open(keyring.Config{AllowedBackends: []keyring.BackendType{store.backend}, ServiceName: nativeServiceName, WinCredPrefix: "AIVidCut", KeychainAccessibleWhenUnlocked: true, KeychainSynchronizable: false, KeychainTrustApplication: false})
	if err != nil {
		return nil, fail("secure_store_unavailable", "系统凭据库不可用或未授权，请在系统中解锁后重试")
	}
	store.ring = ring
	return ring, nil
}

func (store *NativeStore) Set(ctx context.Context, handle string, secret []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !handlePattern.MatchString(handle) || len(secret) == 0 || len(secret) > MaxAPIKeyBytes {
		return fail("credentials", "系统凭据写入参数无效")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	ring, err := store.ringLocked()
	if err != nil {
		return err
	}
	copy := append([]byte(nil), secret...)
	defer clear(copy)
	if err := ring.Set(keyring.Item{Key: handle, Data: copy, Label: "AIVidCut 模型连接凭据", Description: "AIVidCut 本地连接", KeychainNotSynchronizable: true}); err != nil {
		return fail("secure_store_write", "系统凭据保存失败，可能尚未解锁或授权")
	}
	// 不把已经成功写入的操作伪报为未执行；Manager 会在提交资料前再检查取消。
	return nil
}

func (store *NativeStore) Get(ctx context.Context, handle string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !handlePattern.MatchString(handle) {
		return nil, fail("credentials", "系统凭据标识无效")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ring, err := store.ringLocked()
	if err != nil {
		return nil, err
	}
	item, err := ring.Get(handle)
	if errors.Is(err, keyring.ErrKeyNotFound) {
		return nil, ErrSecretNotFound
	}
	if err != nil {
		return nil, fail("secure_store_read", "系统凭据读取失败，请检查解锁或应用授权")
	}
	defer clear(item.Data)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(item.Data) == 0 || len(item.Data) > MaxAPIKeyBytes {
		return nil, fail("credentials", "系统凭据为空或超过当前支持范围")
	}
	return append([]byte(nil), item.Data...), nil
}

func (store *NativeStore) Delete(ctx context.Context, handle string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !handlePattern.MatchString(handle) {
		return fail("credentials", "系统凭据标识无效")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	ring, err := store.ringLocked()
	if err != nil {
		return err
	}
	err = ring.Remove(handle)
	if errors.Is(err, keyring.ErrKeyNotFound) {
		return ErrSecretNotFound
	}
	if err != nil {
		return fail("secure_store_delete", "系统凭据删除失败，请检查解锁或应用授权")
	}
	return nil
}
