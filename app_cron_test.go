package xlgo_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
	"github.com/EthanCodeCraft/xlgo-core/cron"
)

// TestAppCronSchedulerInstance 固化 Phase 3：WithCronTask 把任务注册到 App 专属调度器，
// app.Scheduler() 返回该实例且含注册的任务。
func TestAppCronSchedulerInstance(t *testing.T) {
	saved := cron.SwapDefaultScheduler(cron.NewScheduler())
	defer cron.SwapDefaultScheduler(saved)

	app := xlgo.New(
		xlgo.WithConfig(testConfig(18112)),
		xlgo.WithCronTask("my-task", cron.Every(time.Hour), func(context.Context) error { return nil }),
	)
	if err := app.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer app.Shutdown()

	s := app.Scheduler()
	if s == nil {
		t.Fatal("app.Scheduler() = nil after Init with WithCronTask")
	}
	found := false
	for _, tk := range s.ListTasks() {
		if tk.Name == "my-task" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("WithCronTask task not registered on App scheduler")
	}
}

// TestAppCronRollbackRestoresDefault 固化 Phase 3 回滚：Init 失败后全局默认调度器
// 必须恢复为 Init 前的实例（不含 App 注册的任务），而非残留 App 的调度器。
func TestAppCronRollbackRestoresDefault(t *testing.T) {
	saved := cron.SwapDefaultScheduler(cron.NewScheduler())
	defer cron.SwapDefaultScheduler(saved)

	app := xlgo.New(
		xlgo.WithConfig(testConfig(18113)),
		xlgo.WithCronTask("rollback-task", cron.Every(time.Hour), func(context.Context) error { return nil }),
		xlgo.WithHook(xlgo.Hook{Name: "fail", OnInit: func(*xlgo.App) error { return errors.New("boom") }}),
	)
	if err := app.Init(); err == nil {
		_ = app.Shutdown()
		t.Fatal("expected Init to fail via OnInit hook")
	}

	// 回滚后全局默认不应含 App 的 "rollback-task"
	for _, tk := range cron.GetScheduler().ListTasks() {
		if tk.Name == "rollback-task" {
			t.Fatal("rollback did not restore global default scheduler (App task still on global)")
		}
	}
}

// TestAppCronMultiAppIsolation 固化 Phase 3 核心契约：单进程多 App 时，App A 的 Shutdown
// 只停 A 自己的调度器，不影响 App B 的调度器继续运行（修复前 cron 是全局单例，A.Shutdown
// 会经 cron.StopGlobalWithTimeout 把 B 的调度器也停了）。
//
// 调度器内部 ticker 为 1s，故任务每 1s tick 触发一次（schedule=Every(1ms) 仅决定 due 判定，
// 实际 spawn 受 1s ticker 节拍）。测试因此需要跨 1s tick 观测。
func TestAppCronMultiAppIsolation(t *testing.T) {
	saved := cron.SwapDefaultScheduler(cron.NewScheduler())
	defer cron.SwapDefaultScheduler(saved)

	var ranA, ranB atomic.Int32
	appA := xlgo.New(
		xlgo.WithConfig(testConfig(18110)),
		xlgo.WithCronTask("taskA", cron.Every(time.Millisecond), func(context.Context) error {
			ranA.Add(1)
			return nil
		}),
	)
	appB := xlgo.New(
		xlgo.WithConfig(testConfig(18111)),
		xlgo.WithCronTask("taskB", cron.Every(time.Millisecond), func(context.Context) error {
			ranB.Add(1)
			return nil
		}),
	)
	if err := appA.Init(); err != nil {
		t.Fatalf("appA Init: %v", err)
	}
	if err := appB.Init(); err != nil {
		t.Fatalf("appB Init: %v", err)
	}
	defer appB.Shutdown()

	// 轮询等第一个 1s tick：两个调度器都应各跑至少一次（deadline 3s，容忍慢机/CI 漂移）
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ranA.Load() > 0 && ranB.Load() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	aAtTick := ranA.Load()
	bAtTick := ranB.Load()
	if aAtTick == 0 || bAtTick == 0 {
		t.Fatalf("expected both schedulers ran at least once: A=%d B=%d", aAtTick, bAtTick)
	}

	// 停 App A
	if err := appA.Shutdown(); err != nil {
		t.Fatalf("appA Shutdown: %v", err)
	}

	// 轮询等 B 再跑一次（deadline 3s），证明 B 调度器未被 A.Shutdown 停掉
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ranB.Load() > bAtTick {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if ranA.Load() != aAtTick {
		t.Errorf("App A scheduler ran after its Shutdown (not isolated): before=%d after=%d", aAtTick, ranA.Load())
	}
	if ranB.Load() <= bAtTick {
		t.Errorf("App B scheduler did not continue after App A Shutdown (not isolated): before=%d after=%d", bAtTick, ranB.Load())
	}
}
