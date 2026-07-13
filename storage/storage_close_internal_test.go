package storage

import (
	"mime/multipart"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/config"
)

// closeableMock 实现 Storage + io.Closer，验证 StorageManager.Close 调用驱动的 Close。
type closeableMock struct {
	closed bool
}

func (closeableMock) Upload(*multipart.FileHeader, string) (string, error) { return "", nil }
func (closeableMock) UploadFromBytes([]byte, string, string) (string, error) {
	return "", nil
}
func (closeableMock) GetURL(string) string        { return "" }
func (closeableMock) Delete(string) error         { return nil }
func (closeableMock) Get(string) ([]byte, error)  { return nil, nil }
func (closeableMock) Exists(string) (bool, error) { return false, nil }
func (c *closeableMock) Close() error             { c.closed = true; return nil }

// TestStorageManagerCloseNil：未初始化时 Close 为 no-op，不 panic。
func TestStorageManagerCloseNil(t *testing.T) {
	m := NewStorageManager()
	if err := m.Close(); err != nil {
		t.Fatalf("Close on uninitialized manager should be no-op, got %v", err)
	}
}

// TestStorageManagerCloseLocalStorageNoop：LocalStorage 未实现 io.Closer，Close 为 no-op。
func TestStorageManagerCloseLocalStorageNoop(t *testing.T) {
	m := NewStorageManager()
	if err := m.Init(&config.StorageConfig{
		Driver: "local",
		Local:  config.LocalStorageConfig{Path: t.TempDir()},
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close on LocalStorage-backed manager should be no-op, got %v", err)
	}
}

// TestStorageManagerCloseInvokesCloser：驱动实现 io.Closer 时，StorageManager.Close 调用它。
func TestStorageManagerCloseInvokesCloser(t *testing.T) {
	m := NewStorageManager()
	mock := &closeableMock{}
	m.Set(mock)
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !mock.closed {
		t.Fatal("StorageManager.Close did not invoke io.Closer on driver (未来带连接池的驱动应经此收口)")
	}
}

// TestStorageManagerCloseIdempotent：重复 Close 不 panic。
func TestStorageManagerCloseIdempotent(t *testing.T) {
	m := NewStorageManager()
	mock := &closeableMock{}
	m.Set(mock)
	if err := m.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("second Close should be idempotent, got %v", err)
	}
}
