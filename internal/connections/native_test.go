package connections

import (
	"runtime"
	"testing"

	"github.com/99designs/keyring"
)

// 只验证后端白名单与构造的惰性，不执行任何 NativeStore 凭据操作。
func TestNativeBackendWhitelistAndLazyConstruction(t *testing.T) {
	for _, test := range []struct {
		os      string
		backend keyring.BackendType
	}{{"darwin", keyring.KeychainBackend}, {"windows", keyring.WinCredBackend}} {
		backend, err := backendForOS(test.os)
		if err != nil || backend != test.backend {
			t.Fatalf("系统后端选择错误：%s %v", test.os, err)
		}
	}
	for _, platform := range []string{"linux", "freebsd", ""} {
		if _, err := backendForOS(platform); err == nil {
			t.Fatalf("不支持的平台不应静默回退：%s", platform)
		}
	}
	store, err := NewNativeStore()
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		if err == nil {
			t.Fatal("未支持的平台应明确报错")
		}
		return
	}
	if err != nil {
		// 例如禁用 CGO 的 macOS 构建：正确行为是明确不可用，不能选用文件后端。
		requireCode(t, err, "secure_store_unavailable")
		return
	}
	if store.ring != nil || (store.Backend() != string(keyring.KeychainBackend) && store.Backend() != string(keyring.WinCredBackend)) {
		t.Fatal("构造应只选择系统后端，不能打开凭据库")
	}
}
