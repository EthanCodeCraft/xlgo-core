package xlgo_test

import (
	"errors"
	"testing"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/storage"
)

// localStorageCfg 构造一个本地存储配置（tmpdir 由调用方提供）。
func localStorageCfg(port int, dir string) *config.Config {
	cfg := testConfig(port)
	cfg.Storage = config.StorageConfig{
		Driver: "local",
		Local:  config.LocalStorageConfig{Path: dir},
	}
	return cfg
}

// TestAppWithStorageSwapsDefault 固化 Phase 2 契约：App.Init(WithStorage) 必须把 App 的
// StorageManager 提升为全局默认。证据：Init 前 GetStorage()=nil（空 manager），Init 后非 nil。
func TestAppWithStorageSwapsDefault(t *testing.T) {
	// 用空 manager 作"Init 前"默认，确保 GetStorage() 起点 nil
	saved := storage.SwapDefaultStorageManager(storage.NewStorageManager())
	defer storage.SwapDefaultStorageManager(saved)

	if s := storage.GetStorage(); s != nil {
		t.Fatalf("precondition: GetStorage() should be nil, got %T", s)
	}

	dir := t.TempDir()
	app := xlgo.New(xlgo.WithConfig(localStorageCfg(18094, dir)), xlgo.WithStorage())
	if err := app.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer app.Shutdown()

	if s := storage.GetStorage(); s == nil {
		t.Fatal("storage.GetStorage() = nil after App.Init with WithStorage（未把 App manager 提升为全局默认）")
	}
}

// TestAppStorageRollbackOnInitFailure 固化 Phase 2 回滚契约：Init 在 storage 之后失败时，
// rollbackReplacedResources 必须把全局默认 swap 回 Init 前的旧 manager。
// 用 OnInit hook 触发失败（OnInit 在 storage init 之后、commitReplacedResources 之前）。
func TestAppStorageRollbackOnInitFailure(t *testing.T) {
	saved := storage.SwapDefaultStorageManager(storage.NewStorageManager())
	defer storage.SwapDefaultStorageManager(saved)

	dir := t.TempDir()
	app := xlgo.New(
		xlgo.WithConfig(localStorageCfg(18095, dir)),
		xlgo.WithStorage(),
		xlgo.WithHook(xlgo.Hook{
			Name:   "force-fail",
			OnInit: func(*xlgo.App) error { return errors.New("boom") },
		}),
	)

	if err := app.Init(); err == nil {
		_ = app.Shutdown()
		t.Fatal("expected Init to fail via OnInit hook")
	}

	// 回滚后全局默认应恢复为 saved（空 manager）-> GetStorage() 返回 nil
	if s := storage.GetStorage(); s != nil {
		t.Errorf("Init 失败回滚未恢复 storage 全局默认: GetStorage()=%T", s)
	}
}
