package cron

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func mustPanicM13(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s should panic", name)
		}
	}()
	fn()
}

// TestCronRunTaskPanicRecovered_M13 回归：RunTask handler panic 时，executeTask 边界
// recover 转为 error 返回调用方并记入 LastError，不崩进程；running 守卫随后释放。
// 修复前：panic 直接上抛 → 测试进程崩溃。
func TestCronRunTaskPanicRecovered_M13(t *testing.T) {
	s := NewScheduler()
	s.AddTask("panic", Every(time.Minute), func(context.Context) error { panic("boom") })

	err := s.RunTask("panic")
	if err == nil || !strings.Contains(err.Error(), "panic recovered") {
		t.Fatalf("RunTask err = %v, want a panic-recovered error", err)
	}

	got, _ := s.GetTask("panic")
	if got.LastError == nil || !strings.Contains(got.LastError.Error(), "panic recovered") {
		t.Fatalf("LastError = %v, want panic-recovered error", got.LastError)
	}

	// running 守卫必须已释放：再次 RunTask 不应返“任务正在执行中”（会再次 panic→recover）。
	err2 := s.RunTask("panic")
	if err2 != nil && strings.Contains(err2.Error(), "任务正在执行中") {
		t.Fatal("running guard not released after panic (second RunTask blocked)")
	}
}

// TestCronRunTaskRecordsLastError_M13 回归：RunTask 手动路径此前只更 LastRun/RunCount，
// 不记 LastError。修复后与调度路径一致，正常 error 也记入 LastError。
func TestCronRunTaskRecordsLastError_M13(t *testing.T) {
	s := NewScheduler()
	sentinel := errors.New("sentinel fail")
	s.AddTask("err", Every(time.Minute), func(context.Context) error { return sentinel })

	err := s.RunTask("err")
	if !errors.Is(err, sentinel) {
		t.Fatalf("RunTask err = %v, want sentinel", err)
	}
	got, _ := s.GetTask("err")
	if !errors.Is(got.LastError, sentinel) {
		t.Fatalf("LastError = %v, want sentinel", got.LastError)
	}
	if got.RunCount != 1 {
		t.Fatalf("RunCount = %d, want 1", got.RunCount)
	}
}

// TestCronCheckAndRunPanicRecovered_M13 回归：调度 goroutine 内 handler panic 时，
// recover 防止进程崩溃；同 tick 派生的兄弟 goroutine 不受影响；无 goroutine 泄漏。
// 同包测试直接驱动 checkAndRun + wg.Wait，零 sleep、确定性。
// 修复前：派生 goroutine 内未 recover 的 panic 终止测试进程。
func TestCronCheckAndRunPanicRecovered_M13(t *testing.T) {
	s := NewScheduler()
	var ran atomic.Int32
	s.AddTask("panic", Every(time.Millisecond), func(context.Context) error { panic("boom") })
	s.AddTask("canary", Every(time.Millisecond), func(context.Context) error {
		ran.Add(1)
		return nil
	})

	// AddTask 设 NextRun=now+1ms；手动改到过去确保两者 due，跨过 1s ticker 的不确定性。
	s.mu.Lock()
	s.tasks["panic"].NextRun = time.Now().Add(-time.Second)
	s.tasks["canary"].NextRun = time.Now().Add(-time.Second)
	s.mu.Unlock()

	s.checkAndRun()
	s.wg.Wait() // 等派生的两个 goroutine 退出，证无泄漏

	pt, _ := s.GetTask("panic")
	if pt.LastError == nil || !strings.Contains(pt.LastError.Error(), "panic recovered") {
		t.Fatalf("panic task LastError = %v, want panic-recovered error", pt.LastError)
	}
	if ran.Load() == 0 {
		t.Fatal("canary did not run — sibling goroutine killed by panic?")
	}
}

func TestCronAddTaskRejectsNilScheduleOrHandler_M13(t *testing.T) {
	s := NewScheduler()
	mustPanicM13(t, "nil schedule", func() {
		s.AddTask("nil-schedule", nil, func(context.Context) error { return nil })
	})
	mustPanicM13(t, "nil handler", func() {
		s.AddTask("nil-handler", Every(time.Minute), nil)
	})
}

func TestCronScheduleConstructorsRejectInvalidBounds_M13(t *testing.T) {
	mustPanicM13(t, "Every(0)", func() { Every(0) })
	mustPanicM13(t, "Every(negative)", func() { Every(-time.Second) })
	mustPanicM13(t, "Daily hour", func() { Daily(24, 0) })
	mustPanicM13(t, "Daily minute", func() { Daily(23, 60) })
	mustPanicM13(t, "Weekly day", func() { Weekly(time.Weekday(9), 9, 0) })
	mustPanicM13(t, "Weekly hour", func() { Weekly(time.Monday, -1, 0) })
	mustPanicM13(t, "Weekly minute", func() { Weekly(time.Monday, 9, -1) })
}

func TestCronAddTaskRejectsInvalidExportedScheduleValues_M13(t *testing.T) {
	s := NewScheduler()
	mustPanicM13(t, "invalid interval schedule", func() {
		s.AddTask("bad-interval", &IntervalSchedule{}, func(context.Context) error { return nil })
	})
	mustPanicM13(t, "invalid daily schedule", func() {
		s.AddTask("bad-daily", &DailySchedule{Hour: 99}, func(context.Context) error { return nil })
	})
	mustPanicM13(t, "invalid weekly schedule", func() {
		s.AddTask("bad-weekly", &WeeklySchedule{Day: time.Weekday(8)}, func(context.Context) error { return nil })
	})
}

func TestCronStartAfterStopRebuildsContext_M13(t *testing.T) {
	s := NewScheduler()
	s.AddTask("ctx", Every(time.Hour), func(ctx context.Context) error {
		return ctx.Err()
	})

	s.Start()
	s.Stop()
	s.Start()
	t.Cleanup(s.Stop)

	if err := s.RunTask("ctx"); err != nil {
		t.Fatalf("RunTask after Stop/Start got ctx err = %v, want nil", err)
	}
}

func TestCronCheckAndRunSkipsAfterStopContextCanceled_M13(t *testing.T) {
	s := NewScheduler()
	var ran atomic.Int32
	s.AddTask("due", Every(time.Minute), func(context.Context) error {
		ran.Add(1)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s.mu.Lock()
	s.running = false
	s.tasks["due"].NextRun = time.Now().Add(-time.Second)
	s.mu.Unlock()

	s.checkAndRun(ctx)
	s.wg.Wait()

	if got := ran.Load(); got != 0 {
		t.Fatalf("task ran %d times after stopped/canceled ctx, want 0", got)
	}
}
