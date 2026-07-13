package xlgo_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/cron"
	"github.com/EthanCodeCraft/xlgo-core/logger"
	"gorm.io/gorm"
)

// TestAppGoAddWaitRace_M1 回归（须 -race）：App.Go 的 wg.Add 与 Shutdown 的 wg.Wait 并发，
// 必须由 lifecycleMu 串行化（Add happens-before Wait），否则违反 WaitGroup 契约。
// 修复前：-race 报 wg.Add/wg.Wait data race 或 "negative WaitGroup counter" panic。
func TestAppGoAddWaitRace_M1(t *testing.T) {
	const M = 20
	const iters = 20
	for iter := 0; iter < iters; iter++ {
		app := xlgo.New()
		var spawned, exited atomic.Int32
		gate := make(chan struct{})
		var goDone sync.WaitGroup
		goDone.Add(M)
		for i := 0; i < M; i++ {
			go func() {
				<-gate
				app.Go(func(ctx context.Context) {
					spawned.Add(1)
					<-ctx.Done()
					exited.Add(1)
				})
				goDone.Done()
			}()
		}
		shutCh := make(chan error, 1)
		go func() {
			<-gate
			shutCh <- app.Shutdown()
		}()
		close(gate)
		goDone.Wait()
		err := <-shutCh
		if err != nil {
			t.Fatalf("iter %d: Shutdown error: %v", iter, err)
		}
		sp, ex := spawned.Load(), exited.Load()
		if sp != ex {
			t.Fatalf("iter %d: spawned %d != exited %d (goroutine leak / Add-after-Wait)", iter, sp, ex)
		}
	}
}

// TestAppGoRefusedAfterShutdown_M1：Shutdown 后 Go() 必须拒绝（state>=Stopping），不 spawn。
func TestAppGoRefusedAfterShutdown_M1(t *testing.T) {
	app := xlgo.New(xlgo.WithConfig(testConfig(18102)))
	if err := app.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	ran := make(chan struct{}, 1)
	app.Go(func(context.Context) { ran <- struct{}{} })
	time.Sleep(50 * time.Millisecond) // 给可能被 spawn 的 goroutine 时间发送
	select {
	case <-ran:
		t.Fatal("Go ran after Shutdown — should be refused (state=Stopping)")
	default:
	}
}

// TestAppInitAfterShutdownRejected_M1（Edge 1）：Shutdown 后再 Init 必须返 ErrAppClosed。
func TestAppInitAfterShutdownRejected_M1(t *testing.T) {
	app := xlgo.New(xlgo.WithConfig(testConfig(18101)))
	if err := app.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := app.Init(); !errors.Is(err, xlgo.ErrAppClosed) {
		t.Fatalf("Init after Shutdown err = %v, want ErrAppClosed", err)
	}
}

// TestAppShutdownIdempotent_M1：并发 Shutdown 幂等，OnStop 仅执行一次。
func TestAppShutdownIdempotent_M1(t *testing.T) {
	var stopCount atomic.Int32
	app := xlgo.New(
		xlgo.WithConfig(testConfig(18100)),
		xlgo.WithHook(xlgo.Hook{Name: "count-stop", OnStop: func(*xlgo.App) error { stopCount.Add(1); return nil }}),
	)
	if err := app.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = app.Shutdown() }()
	go func() { defer wg.Done(); _ = app.Shutdown() }()
	wg.Wait()
	if stopCount.Load() != 1 {
		t.Fatalf("OnStop called %d times, want 1 (idempotent)", stopCount.Load())
	}
	if err := app.Shutdown(); err != nil {
		t.Fatalf("third Shutdown error: %v", err)
	}
	if stopCount.Load() != 1 {
		t.Fatalf("OnStop called %d times after 3rd, want 1", stopCount.Load())
	}
}

// TestAppInitFailureRollsBackCron_M1（Edge 3 + 资源回滚）：Init 失败（OnInit hook 报错）时，
// 已 cron.Start() 的全局调度器必须被 failAfterInit 停止，canary 任务不再触发。
// 修复前：Init 失败不停 cron → canary 在下个 1s tick 跑 → ran > 0。
// TestAppShutdownTimeoutCoversOnStop_M1 回归：OnStop 必须受 server.shutdown_timeout 约束。
// 修复前：OnStop 同步执行且不看超时，阻塞 hook 会拖死 Shutdown。
func TestAppShutdownTimeoutCoversOnStop_M1(t *testing.T) {
	cfg := testConfig(18105)
	cfg.Server.ShutdownTimeout = 30 * time.Millisecond
	release := make(chan struct{})
	defer close(release)

	app := xlgo.New(
		xlgo.WithConfig(cfg),
		xlgo.WithHook(xlgo.Hook{Name: "slow-stop", OnStop: func(*xlgo.App) error {
			<-release
			return nil
		}}),
	)
	if err := app.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	start := time.Now()
	err := app.Shutdown()
	if err == nil || !strings.Contains(err.Error(), "OnStop hook") {
		t.Fatalf("Shutdown err = %v, want OnStop timeout error", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Shutdown took %s, OnStop timeout did not bound shutdown", elapsed)
	}
}

// TestWithConfigClonesAndValidates_M1 回归：WithConfig 捕获私有快照，并在 Init 时校验。
func TestWithConfigClonesAndValidates_M1(t *testing.T) {
	cfg := testConfig(18106)
	app := xlgo.New(xlgo.WithConfig(cfg))
	cfg.Server.Port = -1 // 不应污染 App 内部快照
	if err := app.Init(); err != nil {
		t.Fatalf("Init with cloned config failed after caller mutation: %v", err)
	}
	if err := app.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	bad := testConfig(-1)
	err := xlgo.New(xlgo.WithConfig(bad)).Init()
	if err == nil || !strings.Contains(err.Error(), "配置校验失败") {
		t.Fatalf("Init with invalid WithConfig err = %v, want validation error", err)
	}
}

func TestAppInitFailureRollsBackCron_M1(t *testing.T) {
	cron.StopGlobalWithTimeout(time.Second) // 清前序残留
	t.Cleanup(func() {
		cron.GetScheduler().RemoveTask("canary-m1")
		cron.StopGlobalWithTimeout(time.Second)
	})

	var ran atomic.Int32
	// Phase 3：canary 注册到 App 专属调度器（WithCronTask），Init 失败时 stopCron 必须停掉它。
	app := xlgo.New(
		xlgo.WithConfig(testConfig(18104)),
		xlgo.WithCronTask("canary-m1", cron.Every(time.Millisecond), func(context.Context) error {
			ran.Add(1)
			return nil
		}),
		xlgo.WithHook(xlgo.Hook{Name: "fail", OnInit: func(*xlgo.App) error { return errors.New("boom-init") }}),
	)
	err := app.Init()
	if err == nil || !strings.Contains(err.Error(), "boom-init") {
		t.Fatalf("Init err = %v, want boom-init", err)
	}

	time.Sleep(1300 * time.Millisecond) // 跨一个 1s ticker
	if ran.Load() != 0 {
		t.Fatalf("canary ran %d times after Init failure — cron not stopped by rollback", ran.Load())
	}
}

// TestAppGoRefusedAfterInitFailure_M1：Init 失败后 failAfterInit 置 state=Stopping，
// 后续 Go() 拒绝；先前的 Go goroutine 因 rootCtx cancel 退出（不泄漏）。
func TestAppGoRefusedAfterInitFailure_M1(t *testing.T) {
	app := xlgo.New(
		xlgo.WithConfig(testConfig(18103)),
		xlgo.WithMigrator(func(*gorm.DB) error { return nil }), // 无 WithMySQL → Init 确定性失败
	)
	firstDone := make(chan struct{})
	app.Go(func(ctx context.Context) { <-ctx.Done(); close(firstDone) })

	if err := app.Init(); err == nil {
		t.Fatal("expected Init error")
	}
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("first Go goroutine did not exit after Init failure (rootCtx not cancelled)")
	}

	ran := make(chan struct{}, 1)
	app.Go(func(context.Context) { ran <- struct{}{} })
	time.Sleep(50 * time.Millisecond)
	select {
	case <-ran:
		t.Fatal("Go ran after Init failure — should be refused (state=Stopping)")
	default:
	}
}

// TestAppInitFailureDoesNotCloseUnownedGlobals_M1 回归：一个尚未成功初始化任何资源的
// App Init 失败时，不得关闭进程里已有的全局 logger/db/redis 等资源。
func TestAppInitFailureDoesNotCloseUnownedGlobals_M1(t *testing.T) {
	dir := t.TempDir()
	if err := logger.Init(&config.Config{
		App:    config.AppConfig{Env: "test"},
		Server: config.ServerConfig{Mode: "development"},
		Log:    config.LogConfig{Dir: dir, MaxSize: 1, MaxBackups: 1, MaxAge: 1},
	}); err != nil {
		t.Fatalf("logger.Init: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })

	missingConfig := filepath.Join(t.TempDir(), "missing.yaml")
	if err := xlgo.New(xlgo.WithConfigPath(missingConfig)).Init(); err == nil {
		t.Fatal("expected Init error without config")
	}

	const mark = "M1_UNOWNED_LOGGER_STILL_ACTIVE"
	logger.Info(mark)
	_ = logger.Sync()
	data, err := os.ReadFile(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatalf("read app.log: %v", err)
	}
	if !strings.Contains(string(data), mark) {
		t.Fatal("unowned global logger was closed by failed App.Init")
	}
}

// TestAppInitShutdownConcurrentSerialization_M1 直接覆盖 initMu 串行化 doInit/doShutdown
// （Edge 2）：Init 持 initMu 期间（OnInit hook 阻塞），并发 Shutdown 必须阻塞等 Init 释放，
// 不能与 doInit 并发改资源。确定性逻辑测试，不依赖 -race 概率。
func TestAppInitFailureRestoresReplacedLogger_M1(t *testing.T) {
	oldMgr := logger.NewLogManager()
	oldDir := t.TempDir()
	if err := oldMgr.Init(&config.Config{
		App:    config.AppConfig{Env: "test"},
		Server: config.ServerConfig{Mode: "development"},
		Log:    config.LogConfig{Dir: oldDir, MaxSize: 1, MaxBackups: 1, MaxAge: 1},
	}); err != nil {
		t.Fatalf("old logger Init: %v", err)
	}
	logger.SetDefaultLogManager(oldMgr)
	t.Cleanup(func() {
		_ = logger.Close()
		logger.SetDefaultLogManager(logger.NewLogManager())
	})

	cfg := testConfig(18105)
	cfg.Log.Dir = t.TempDir()
	app := xlgo.New(
		xlgo.WithConfig(cfg),
		xlgo.WithLogger(),
		xlgo.WithHook(xlgo.Hook{Name: "fail-after-logger", OnInit: func(*xlgo.App) error {
			return errors.New("boom-after-logger")
		}}),
	)
	err := app.Init()
	if err == nil || !strings.Contains(err.Error(), "boom-after-logger") {
		t.Fatalf("Init err = %v, want boom-after-logger", err)
	}
	if got := logger.GetDefaultLogManager(); got != oldMgr {
		t.Fatalf("default logger manager was not restored after Init failure: got %p want %p", got, oldMgr)
	}

	const mark = "M1_RESTORED_LOGGER_STILL_ACTIVE"
	logger.Info(mark)
	_ = logger.Sync()
	data, err := os.ReadFile(filepath.Join(oldDir, "app.log"))
	if err != nil {
		t.Fatalf("read old app.log: %v", err)
	}
	if !strings.Contains(string(data), mark) {
		t.Fatal("restored logger did not write to old app.log")
	}
}

func TestAppInitShutdownConcurrentSerialization_M1(t *testing.T) {
	initStarted := make(chan struct{})
	releaseInit := make(chan struct{})
	app := xlgo.New(
		xlgo.WithConfig(testConfig(18200)),
		xlgo.WithHook(xlgo.Hook{
			Name: "block-init",
			OnInit: func(*xlgo.App) error {
				close(initStarted)
				<-releaseInit
				return nil
			},
		}),
	)

	initDone := make(chan error, 1)
	go func() { initDone <- app.Init() }()

	<-initStarted // OnInit 在 doInit 末尾运行 → 此时 Init 持 initMu

	// 并发 Shutdown：必须阻塞等 initMu，不能在 Init 完成前返回。
	shutDone := make(chan error, 1)
	go func() { shutDone <- app.Shutdown() }()

	select {
	case <-shutDone:
		t.Fatal("Shutdown returned before Init finished — initMu not serializing doInit/doShutdown")
	case <-time.After(150 * time.Millisecond):
		// 期望：Shutdown 仍阻塞等 initMu
	}

	close(releaseInit) // 让 OnInit 返回 → doInit 完成 → Init 释放 initMu

	select {
	case err := <-initDone:
		if err != nil {
			t.Fatalf("Init: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Init did not return after release")
	}
	select {
	case <-shutDone:
		// Shutdown 在 Init 后获得 initMu 并完成
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return after Init finished — initMu not released?")
	}
}

// TestAppStartServerListenFailureSkipsOnReady_M1 回归：监听失败时不得先执行 OnReady。
// 修复前 StartServer 在 goroutine 内 ListenAndServe，主 goroutine 立即跑 OnReady；
// 端口被占用时仍可能先触发 ready 副作用，再返回启动失败。
func TestAppStartServerListenFailureSkipsOnReady_M1(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	cfg := testConfig(port)
	cfg.Server.Host = "127.0.0.1"

	var ready atomic.Int32
	app := xlgo.New(
		xlgo.WithConfig(cfg),
		xlgo.WithHook(xlgo.Hook{
			Name: "ready-must-not-run",
			OnReady: func(*xlgo.App) error {
				ready.Add(1)
				return nil
			},
		}),
	)
	if err := app.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	err = app.StartServer()
	if err == nil || !strings.Contains(err.Error(), "服务器监听失败") {
		t.Fatalf("StartServer err = %v, want listen failure", err)
	}
	if ready.Load() != 0 {
		t.Fatalf("OnReady ran %d times despite listen failure", ready.Load())
	}
	_ = app.Shutdown()
}

// TestAppStartServerTLSFailureSkipsOnReady_M1 回归：TLS 证书装配失败也不得触发 OnReady。
func TestAppStartServerTLSFailureSkipsOnReady_M1(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("release port: %v", err)
	}

	cfg := testConfig(port)
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.TLS.Enabled = true
	cfg.Server.TLS.CertFile = filepath.Join(t.TempDir(), "missing.crt")
	cfg.Server.TLS.KeyFile = filepath.Join(t.TempDir(), "missing.key")

	var ready atomic.Int32
	app := xlgo.New(
		xlgo.WithConfig(cfg),
		xlgo.WithHook(xlgo.Hook{
			Name: "tls-ready-must-not-run",
			OnReady: func(*xlgo.App) error {
				ready.Add(1)
				return nil
			},
		}),
	)
	if err := app.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	err = app.StartServer()
	if err == nil || !strings.Contains(err.Error(), "服务器 TLS 配置无效") {
		t.Fatalf("StartServer err = %v, want TLS configuration failure", err)
	}
	if ready.Load() != 0 {
		t.Fatalf("OnReady ran %d times despite TLS failure", ready.Load())
	}
	_ = app.Shutdown()
}

// TestAppStartServerUnixSocketListens_M1 回归：server.unix_socket 必须真的通过
// net.Listen("unix", path) 监听，而不是把 path 塞给 TCP ListenAndServe 的 Addr。
func TestAppStartServerUnixSocketListens_M1(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows unix socket support varies by environment")
	}

	sock := filepath.Join(t.TempDir(), "xlgo.sock")
	cfg := testConfig(0)
	cfg.Server.UnixSocket = sock

	app := xlgo.New(
		xlgo.WithConfig(cfg),
		xlgo.WithHook(xlgo.Hook{
			Name: "dial-unix-ready",
			OnReady: func(*xlgo.App) error {
				conn, err := net.DialTimeout("unix", sock, time.Second)
				if err != nil {
					return err
				}
				_ = conn.Close()
				return errors.New("stop after unix socket ready")
			},
		}),
	)
	if err := app.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	err := app.StartServer()
	if err == nil || !strings.Contains(err.Error(), "stop after unix socket ready") {
		t.Fatalf("StartServer err = %v, want OnReady sentinel after unix dial", err)
	}
}
