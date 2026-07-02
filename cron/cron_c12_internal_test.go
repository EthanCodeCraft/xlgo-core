package cron

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// stepSchedule 是一个固定步长调度，Next(now)=now+step，用于确定性测试 C12c 锚定。
type stepSchedule struct{ step time.Duration }

func (s stepSchedule) Next(now time.Time) time.Time { return now.Add(s.step) }

// TestC12bNoOverlapManualDrive 验证 per-task running 守卫防止重叠执行。
//
// 修复前：checkAndRun 无 running 守卫，handler 未完成时下一次 checkAndRun（NextRun 仍为过去）
// 会再次 spawn 同一任务，并发执行。
// 手动驱动两轮 checkAndRun，handler 阻塞至释放——修复后第二轮被 running 守卫拦截，仅 1 次 spawn。
func TestC12bNoOverlapManualDrive(t *testing.T) {
	s := NewScheduler()
	var started int32
	release := make(chan struct{})
	task := s.AddTask("block", stepSchedule{step: time.Microsecond}, func(ctx context.Context) error {
		atomic.AddInt32(&started, 1)
		<-release
		return nil
	})
	task.NextRun = time.Now().Add(-time.Hour) // 过去 → 到期

	s.checkAndRun() // 第一轮：spawn，handler 阻塞
	for atomic.LoadInt32(&started) == 0 {
		time.Sleep(time.Millisecond)
	}

	s.checkAndRun() // 第二轮：修复后 running 守卫拦截，不再 spawn
	time.Sleep(50 * time.Millisecond) // 给潜在二次 spawn 启动时间

	if n := atomic.LoadInt32(&started); n != 1 {
		t.Errorf("task overlapped: started = %d, want 1 (C12b)", n)
	}

	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for task.running != nil && task.running.Load() {
		if time.Now().After(deadline) {
			t.Fatalf("running guard never released")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestC12cNextRunAnchoredOnPrevious 验证 NextRun 以上次 NextRun 锚定（不漂移）。
//
// 修复前：NextRun = schedule.Next(time.Now())（handler 完成后的 now），每周期累积 handler 时长。
// 修复后：NextRun = schedule.Next(task.NextRun)（上次计划时间锚定）。
//
// 将 NextRun 设到过去的固定锚点 T0，手动驱动 3 轮 checkAndRun（每轮等 handler 完成），
// 断言 NextRun == T0 + 3*step（锚定）。漂移实现下 NextRun ≈ now+step（远大于 T0+3*step）。
func TestC12cNextRunAnchoredOnPrevious(t *testing.T) {
	s := NewScheduler()
	step := 100 * time.Millisecond

	done := make(chan struct{}, 16)
	task := s.AddTask("anchored", stepSchedule{step: step}, func(ctx context.Context) error {
		// 模拟 handler 耗时——修复后不应影响 NextRun 锚定。
		time.Sleep(50 * time.Millisecond)
		done <- struct{}{}
		return nil
	})

	// 将 NextRun 设到过去的固定锚点 T0（远早于 now，确保每轮都到期触发）。
	T0 := time.Now().Add(-10 * time.Second)
	task.NextRun = T0

	for i := 1; i <= 3; i++ {
		s.checkAndRun()
		// 等 handler 执行；若 NextRun 未锚定（漂移实现下 NextRun 跳到未来，
		// 后续 checkAndRun 不再到期），done 不会收到 → 超时明确失败而非挂起。
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("iter %d: handler did not fire (NextRun drifted to future, not anchored on previous) (C12c)", i)
		}
		// 等 running 守卫释放（defer 在 handler 返回后置 false）。
		deadline := time.Now().Add(2 * time.Second)
		for task.running != nil && task.running.Load() {
			if time.Now().After(deadline) {
				t.Fatalf("running guard never released (iter %d)", i)
			}
			time.Sleep(time.Millisecond)
		}
	}

	got := task.NextRun
	want := T0.Add(3 * step)
	if diff := got.Sub(want); diff < -5*time.Millisecond || diff > 5*time.Millisecond {
		t.Errorf("NextRun not anchored on previous: got %v, want %v (diff %v, C12c drift)", got, want, diff)
	}
}

// C12e 直接测试 matchField（未导出，故 internal test）。
//
// 修复前：matchField 先判 `-` 把整字段当范围，parseInt 忽略非数字逐位累积：
//   - "1-5,8" 含 `-` → parseInt("5,8")=58 → 范围 1..58 全匹配，列表项 8 丢失。
//   - "garbage" → parseInt=0，value=0 误触发。
//   - "*/garbage" → step=0 → return true 匹配全部。

var schedForMatch = &FullCronSchedule{}

func TestC12eMatchFieldListAndRangeIndependent(t *testing.T) {
	// "1-5,8" 仅匹配 1,2,3,4,5,8。
	for _, v := range []int{1, 2, 3, 4, 5, 8} {
		if !schedForMatch.matchField("1-5,8", v, 0, 59) {
			t.Errorf("1-5,8 should match %d (C12e)", v)
		}
	}
	for _, v := range []int{0, 6, 7, 9, 30, 58} {
		if schedForMatch.matchField("1-5,8", v, 0, 59) {
			t.Errorf("1-5,8 should NOT match %d (C12e range broken to 1..58)", v)
		}
	}
}

func TestC12eMatchFieldGarbageNotMatch(t *testing.T) {
	for v := 0; v <= 59; v++ {
		if schedForMatch.matchField("garbage", v, 0, 59) {
			t.Errorf("garbage should not match any value, matched %d (C12e)", v)
		}
	}
}

func TestC12eMatchFieldStarSlashGarbageNotMatchAll(t *testing.T) {
	// 修复前 step=0 → return true 匹配全部。
	matched := 0
	for v := 0; v <= 59; v++ {
		if schedForMatch.matchField("*/garbage", v, 0, 59) {
			matched++
		}
	}
	if matched != 0 {
		t.Errorf("*/garbage should match nothing, matched %d (C12e step=0 bug)", matched)
	}
}

func TestC12eMatchFieldWeekday7IsSunday(t *testing.T) {
	// weekday 字段 7 → 0（周日）。
	if !schedForMatch.matchField("7", 0, 0, 6) {
		t.Error("weekday 7 should match Sunday(0) (C12e)")
	}
	if schedForMatch.matchField("7", 7, 0, 6) {
		t.Error("weekday 7 should not match value 7 (out of range after normalize)")
	}
}

func TestC12eMatchFieldRangeAndStepAndList(t *testing.T) {
	// 范围 9-17。
	for _, v := range []int{9, 12, 17} {
		if !schedForMatch.matchField("9-17", v, 0, 23) {
			t.Errorf("9-17 should match %d", v)
		}
	}
	for _, v := range []int{8, 18} {
		if schedForMatch.matchField("9-17", v, 0, 23) {
			t.Errorf("9-17 should NOT match %d", v)
		}
	}
	// 步长 */15。
	for _, v := range []int{0, 15, 30, 45} {
		if !schedForMatch.matchField("*/15", v, 0, 59) {
			t.Errorf("*/15 should match %d", v)
		}
	}
	for _, v := range []int{1, 7, 16} {
		if schedForMatch.matchField("*/15", v, 0, 59) {
			t.Errorf("*/15 should NOT match %d", v)
		}
	}
	// 范围步长 9-17/2 → 9,11,13,15,17。
	for _, v := range []int{9, 11, 13, 15, 17} {
		if !schedForMatch.matchField("9-17/2", v, 0, 23) {
			t.Errorf("9-17/2 should match %d", v)
		}
	}
	for _, v := range []int{10, 12, 14, 16} {
		if schedForMatch.matchField("9-17/2", v, 0, 23) {
			t.Errorf("9-17/2 should NOT match %d", v)
		}
	}
}
