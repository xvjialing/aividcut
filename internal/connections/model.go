package connections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// SecretStore 的 Set 必须复制输入；Get 返回调用方拥有、可清零的副本。
// 正式实现只使用系统凭据库，测试在 _test.go 中使用内存实现。
type SecretStore interface {
	Set(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
}

var ErrSecretNotFound = errors.New("系统凭据不存在")

type Profile struct {
	ID         string    `json:"id"`
	ProviderID string    `json:"providerId"`
	Name       string    `json:"name"`
	ModelID    string    `json:"modelId"`
	Configured bool      `json:"configured"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type SaveInput struct {
	ID         string `json:"id,omitempty"`
	ProviderID string `json:"providerId"`
	Name       string `json:"name"`
	ModelID    string `json:"modelId"`
	APIKey     string `json:"apiKey,omitempty"`
}

// 凭据输入可以从 Wails/JSON 解码；Go 侧重新编码或格式化时不携带密钥。
func (input SaveInput) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID         string `json:"id,omitempty"`
		ProviderID string `json:"providerId"`
		Name       string `json:"name"`
		ModelID    string `json:"modelId"`
	}{input.ID, input.ProviderID, input.Name, input.ModelID})
}

func (input SaveInput) String() string   { data, _ := input.MarshalJSON(); return string(data) }
func (input SaveInput) GoString() string { return input.String() }

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Committed bool   `json:"committed"`
}

func (err *Error) Error() string { return err.Message }
func IsCommitted(err error) bool {
	var value *Error
	return errors.As(err, &value) && value != nil && value.Committed
}
func fail(code, message string) error { return &Error{Code: code, Message: message} }

// Lease 固定本次使用的 profile 与凭据；展示快照的修改不会改变密钥的服务绑定。
type Lease struct {
	Profile  Profile `json:"profile"`
	mu       sync.Mutex
	manager  *Manager
	id       string
	provider string
	secret   []byte
	closed   bool
}

func (lease *Lease) Secret(ctx context.Context, provider string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return "", fail("lease_closed", "连接凭据租约已关闭")
	}
	if len(lease.secret) == 0 {
		return "", fail("credentials_read", "连接租约没有可用凭据")
	}
	if provider != lease.provider {
		return "", fail("provider_mismatch", "凭据只能用于取得租约时的原服务")
	}
	return string(lease.secret), nil
}

func (lease *Lease) Close() error {
	lease.mu.Lock()
	if lease.closed {
		lease.mu.Unlock()
		return nil
	}
	lease.closed = true
	clear(lease.secret)
	lease.secret = nil
	manager, id := lease.manager, lease.id
	lease.mu.Unlock()
	if manager != nil {
		manager.release(id, false)
	}
	return nil
}

func (lease *Lease) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Profile Profile `json:"profile"`
	}{lease.Profile})
}
func (lease *Lease) String() string   { data, _ := lease.MarshalJSON(); return string(data) }
func (lease *Lease) GoString() string { return lease.String() }

// Pin 仅固定连接，供待恢复远程任务使用；创建和关闭均不读取系统密钥。
type Pin struct {
	Profile Profile `json:"profile"`
	mu      sync.Mutex
	manager *Manager
	id      string
	closed  bool
}

func (pin *Pin) Close() error {
	pin.mu.Lock()
	if pin.closed {
		pin.mu.Unlock()
		return nil
	}
	pin.closed = true
	manager, id := pin.manager, pin.id
	pin.mu.Unlock()
	if manager != nil {
		manager.release(id, true)
	}
	return nil
}

func (pin *Pin) String() string   { return fmt.Sprintf("connections.Pin{profile:%s}", pin.Profile.ID) }
func (pin *Pin) GoString() string { return pin.String() }
