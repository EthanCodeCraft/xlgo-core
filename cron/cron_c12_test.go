package cron_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/cron"
)

// ============================================================
// C12a：runTask 计数写入无锁 + GetTask/ListTasks 返回 live 指针 → data race
// ============================================================

// TestC12aConcurrentReadWriteNoRace 验证并发 RunTask/GetTask/ListTasks + 调度运行
// 无数据竞争（-race）。
//
// 修复前：runTask 无锁写 LastRun/RunCount，GetTask/ListTasks 返回 live 指针并发读 →
// -race 必采到 DATA RACE。修复后：写入纳入锁、Getter 返回拷贝。
func TestC12aConcurrentReadWriteNoRace(t *testing.T) {
	scheduler := cron.NewScheduler()

	scheduler.AddTask("t1", cron.Every(50*time.Millisecond), func(ctx context.Context) error {
		return nil
	})
	scheduler.AddTask("t2", cron.Every(80*time.Millisecond), func(ctx context.Context) error {
		return nil
	})

	scheduler.Start()
	t.Cleanup(scheduler.Stop)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// 读取返回拷贝的字段——若写侧无锁，与此处读竞争（C12a）。
				if g, err := scheduler.GetTask("t1"); err == nil {
					_ = g.RunCount
					_ = g.LastRun
				}
				for _, tk := range scheduler.ListTasks() {
					_ = tk.RunCount
					_ = tk.LastRun
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = scheduler.RunTask("t2")
		}
	}()

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestC12aGetTaskReturnsCopy 验证 GetTask 返回拷贝，修改返回值不影响内部状态。
func TestC12aGetTaskReturnsCopy(t *testing.T) {
	scheduler := cron.NewScheduler()
	scheduler.AddTask("t", cron.Every(time.Minute), func(ctx context.Context) error {
		return nil
	})

	got, err := scheduler.GetTask("t")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	got.RunCount = 9999
	got.Enabled = false

	again, _ := scheduler.GetTask("t")
	if again.RunCount == 9999 {
		t.Error("GetTask returned live pointer (RunCount mutated internally)")
	}
	if !again.Enabled {
		t.Error("GetTask returned live pointer (Enabled mutated internally)")
	}
}

// TestC12aListTasksReturnsCopies 验证 ListTasks 元素为拷贝。
func TestC12aListTasksReturnsCopies(t *testing.T) {
	scheduler := cron.NewScheduler()
	scheduler.AddTask("t", cron.Every(time.Minute), func(ctx context.Context) error {
		return nil
	})

	list := scheduler.ListTasks()
	list[0].RunCount = 777

	again := scheduler.ListTasks()
	if again[0].RunCount == 777 {
		t.Error("ListTasks returned live pointer (RunCount mutated internally)")
	}
}

// ============================================================
// C12b：长任务跨 tick 重叠执行
// ============================================================

// TestC12bRunTaskConcurrentManualTriggerReturnsError 验证手动 RunTask 占用守卫期间，
// 再次 RunTask 返"任务正在执行中"错误。
func TestC12bRunTaskConcurrentManualTriggerReturnsError(t *testing.T) {
	scheduler := cron.NewScheduler()

	started := make(chan struct{})
	release := make(chan struct{})
	scheduler.AddTask("block", cron.Every(time.Hour), func(ctx context.Context) error {
		close(started)
		<-release
		return nil
	})

	var firstErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		firstErr = scheduler.RunTask("block")
	}()

	<-started

	secondErr := scheduler.RunTask("block")
	if secondErr == nil {
		t.Error("second RunTask should fail while first is running (C12b running guard)")
	}

	close(release)
	wg.Wait()
	if firstErr != nil {
		t.Errorf("first RunTask error: %v", firstErr)
	}
}

// ============================================================
// C12d：Weekly 当天未到点目标被跳一周
// ============================================================

func TestC12dWeeklySameDayBeforeTarget(t *testing.T) {
	schedule := cron.WeeklySchedule{Day: time.Monday, Hour: 9, Minute: 0}

	// 周一 8:00（目标 9:00 未到）→ 应返回本周一 9:00，不跳周。
	now := time.Date(2026, 6, 29, 8, 0, 0, 0, time.UTC) // 2026-06-29 是周一
	if now.Weekday() != time.Monday {
		t.Fatalf("test fixture: expected Monday, got %v", now.Weekday())
	}
	next := schedule.Next(now)
	want := time.Date(2026, 6, 29, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("Weekly same-day-before-target: got %v, want %v (C12d skip-week bug)", next, want)
	}
}

func TestC12dWeeklySameDayAfterTarget(t *testing.T) {
	schedule := cron.WeeklySchedule{Day: time.Monday, Hour: 9, Minute: 0}

	// 周一 10:00（目标 9:00 已过）→ 下周一 9:00。
	now := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	next := schedule.Next(now)
	want := time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("Weekly same-day-after-target: got %v, want %v", next, want)
	}
}

func TestC12dWeeklyCrossWeek(t *testing.T) {
	schedule := cron.WeeklySchedule{Day: time.Monday, Hour: 9, Minute: 0}

	// 周三 → 下周一。
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC) // 周三
	if now.Weekday() != time.Wednesday {
		t.Fatalf("test fixture: expected Wednesday, got %v", now.Weekday())
	}
	next := schedule.Next(now)
	want := time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("Weekly cross-week: got %v, want %v", next, want)
	}
}

// ============================================================
// C12e：cron 解析缺陷（周日 7 / 范围环绕 / 严格解析）
// （1-5,8 / garbage / */garbage 的 matchField 直接测试见 cron_c12_internal_test.go）
// ============================================================

func TestC12eWeekdaySundayAs7(t *testing.T) {
	schedule := cron.FullCronSchedule{
		Minute:  "0",
		Hour:    "0",
		Day:     "*",
		Month:   "*",
		Weekday: "7",
	}
	// 0 0 * * 7 → 每周日凌晨。从周六 23:00 找下一个匹配应落周日 00:00。
	now := time.Date(2026, 6, 27, 23, 0, 0, 0, time.UTC) // 周六
	if now.Weekday() != time.Saturday {
		t.Fatalf("test fixture: expected Saturday, got %v", now.Weekday())
	}
	next := schedule.Next(now)
	if next.Weekday() != time.Sunday {
		t.Errorf("0 0 * * 7 should land on Sunday, got %v (C12e 7≠Sunday)", next.Weekday())
	}
	if next.Hour() != 0 || next.Minute() != 0 {
		t.Errorf("0 0 * * 7 should land on 00:00, got %v", next)
	}
}

func TestC12eWeekdayRangeWraparound(t *testing.T) {
	// "6-1" 在 weekday 环绕：周六(6)、周日(0)、周一(1)。
	schedule := cron.FullCronSchedule{
		Minute:  "0",
		Hour:    "0",
		Day:     "*",
		Month:   "*",
		Weekday: "6-1",
	}
	for _, w := range []time.Weekday{time.Saturday, time.Sunday, time.Monday} {
		tt := time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC) // 周一
		for tt.Weekday() != w {
			tt = tt.AddDate(0, 0, 1)
		}
		next := schedule.Next(tt.Add(-1 * time.Minute))
		if next.Weekday() != w {
			t.Errorf("6-1 should match %v, got %v", w, next.Weekday())
		}
	}
	// 周二不应被 6-1 匹配，下一个应是周六。
	tt := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC) // 周二
	if tt.Weekday() != time.Tuesday {
		t.Fatalf("test fixture: expected Tuesday, got %v", tt.Weekday())
	}
	next := schedule.Next(tt.Add(-1 * time.Minute))
	if next.Weekday() != time.Saturday {
		t.Errorf("6-1 should skip Tuesday, next match %v, want Saturday", next.Weekday())
	}
}

// TestC12eParseCronStrict 验证严格解析。
func TestC12eParseCronStrict(t *testing.T) {
	cases := []struct {
		expr string
		ok   bool
	}{
		{"0 12 * * *", true},
		{"*/15 * * * *", true},
		{"0 9-17 * * 1-5", true},
		{"0 0 1 * 7", true},  // 周日 7 合法
		{"0 0 * * 0-7", false},
		{"invalid", false},
		{"1-5,8 0 * * *", true},
		{"60 0 * * *", false},  // 分钟越界
		{"0 25 * * *", false},  // 小时越界
		{"0 0 0 * *", false},   // 日越界
		{"0 0 * 13 *", false},  // 月越界
		{"0 0 * * 9", false},   // 周越界
		{"garbage 0 * * *", false},
		{"*/0 * * * *", false}, // step=0 非法
	}
	for _, c := range cases {
		_, err := cron.ParseCronStrict(c.expr)
		if c.ok && err != nil {
			t.Errorf("ParseCronStrict(%q) unexpected error: %v", c.expr, err)
		}
		if !c.ok && err == nil {
			t.Errorf("ParseCronStrict(%q) expected error, got nil", c.expr)
		}
	}
}

// TestC12eParseCronFallback 验证 ParseCron 非法回退默认全 *（保持原行为）。
func TestC12eParseCronFallback(t *testing.T) {
	s := cron.ParseCron("invalid")
	if s.Minute != "*" || s.Hour != "*" || s.Day != "*" || s.Month != "*" || s.Weekday != "*" {
		t.Errorf("ParseCron(invalid) should fall back to all-*, got %+v", s)
	}
	// 合法表达式不回退。
	s = cron.ParseCron("1-5,8 0 * * *")
	if s.Minute != "1-5,8" {
		t.Errorf("ParseCron valid: Minute = %q, want 1-5,8", s.Minute)
	}
}

// TestC12eCronScheduleGarbageNoMatch 验证简化 Cron 不再 parseInt 容错为 0。
func TestC12eCronScheduleGarbageNoMatch(t *testing.T) {
	bad := cron.CronSchedule{Minute: "garbage", Hour: "9"}
	// garbage → splitPattern 返空 → matchMinute 返 false → 24h 内无匹配。
	now := time.Date(2026, 6, 29, 8, 0, 0, 0, time.UTC)
	next := bad.Next(now)
	if !next.IsZero() {
		t.Errorf("CronSchedule garbage minute should not match, got next=%v (C12e)", next)
	}
}
