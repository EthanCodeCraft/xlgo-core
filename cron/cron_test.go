package cron_test

import (
	"context"
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/cron"
)

func TestTask(t *testing.T) {
	task := cron.Task{
		Name:     "test_task",
		Enabled:  true,
		RunCount: 0,
	}

	if task.Name != "test_task" {
		t.Error("Task Name failed")
	}
	if !task.Enabled {
		t.Error("Task Enabled failed")
	}
}

func TestNewScheduler(t *testing.T) {
	scheduler := cron.NewScheduler()
	if scheduler == nil {
		t.Error("NewScheduler should not return nil")
	}
}

func TestAddTask(t *testing.T) {
	scheduler := cron.NewScheduler()
	task := scheduler.AddTask("test", cron.Every(time.Minute), func(ctx context.Context) error {
		return nil
	})

	if task == nil {
		t.Error("AddTask should return task")
	}
	if task.Name != "test" {
		t.Errorf("Task name = %s, want test", task.Name)
	}
}

func TestRemoveTask(t *testing.T) {
	scheduler := cron.NewScheduler()
	scheduler.AddTask("test", cron.Every(time.Minute), func(ctx context.Context) error {
		return nil
	})

	scheduler.RemoveTask("test")

	task, err := scheduler.GetTask("test")
	if err == nil {
		t.Error("RemoveTask should remove task")
	}
	if task != nil {
		t.Error("Removed task should be nil")
	}
}

func TestEnableDisableTask(t *testing.T) {
	scheduler := cron.NewScheduler()
	scheduler.AddTask("test", cron.Every(time.Minute), func(ctx context.Context) error {
		return nil
	})

	err := scheduler.DisableTask("test")
	if err != nil {
		t.Errorf("DisableTask error: %v", err)
	}

	task, _ := scheduler.GetTask("test")
	if task.Enabled {
		t.Error("Task should be disabled")
	}

	err = scheduler.EnableTask("test")
	if err != nil {
		t.Errorf("EnableTask error: %v", err)
	}

	task, _ = scheduler.GetTask("test")
	if !task.Enabled {
		t.Error("Task should be enabled")
	}
}

func TestListTasks(t *testing.T) {
	scheduler := cron.NewScheduler()
	scheduler.AddTask("task1", cron.Every(time.Minute), func(ctx context.Context) error { return nil })
	scheduler.AddTask("task2", cron.Every(time.Hour), func(ctx context.Context) error { return nil })

	tasks := scheduler.ListTasks()
	if len(tasks) != 2 {
		t.Errorf("ListTasks length = %d, want 2", len(tasks))
	}
}

func TestIntervalSchedule(t *testing.T) {
	schedule := cron.IntervalSchedule{Interval: time.Minute}
	now := time.Now()
	next := schedule.Next(now)

	if next.Before(now) {
		t.Error("Next should be after now")
	}
}

func TestEvery(t *testing.T) {
	schedule := cron.Every(5 * time.Minute)
	if schedule.Interval != 5*time.Minute {
		t.Errorf("Every interval = %v, want 5m", schedule.Interval)
	}
}

func TestDailySchedule(t *testing.T) {
	schedule := cron.DailySchedule{Hour: 9, Minute: 30}
	now := time.Now()
	next := schedule.Next(now)

	if next.Hour() != 9 || next.Minute() != 30 {
		t.Errorf("DailySchedule next = %v, want 09:30", next)
	}
}

func TestDaily(t *testing.T) {
	schedule := cron.Daily(10, 0)
	if schedule.Hour != 10 || schedule.Minute != 0 {
		t.Error("Daily failed")
	}
}

func TestWeeklySchedule(t *testing.T) {
	schedule := cron.WeeklySchedule{Day: time.Monday, Hour: 9, Minute: 0}
	now := time.Now()
	next := schedule.Next(now)

	if next.Hour() != 9 {
		t.Errorf("WeeklySchedule hour = %d, want 9", next.Hour())
	}
}

func TestWeekly(t *testing.T) {
	schedule := cron.Weekly(time.Monday, 10, 0)
	if schedule.Day != time.Monday {
		t.Error("Weekly Day failed")
	}
}

func TestCronSchedule(t *testing.T) {
	schedule := cron.CronSchedule{Minute: "0,15,30", Hour: "9"}
	now := time.Now()
	next := schedule.Next(now)

	if next.IsZero() {
		t.Error("CronSchedule should find next time")
	}
}

func TestCron(t *testing.T) {
	schedule := cron.Cron("0", "9")
	if schedule.Minute != "0" || schedule.Hour != "9" {
		t.Error("Cron failed")
	}
}

func TestGetScheduler(t *testing.T) {
	scheduler := cron.GetScheduler()
	if scheduler == nil {
		t.Error("GetScheduler should not return nil")
	}
}

func TestGlobalAddTask(t *testing.T) {
	task := cron.AddTask("global_test", cron.Every(time.Hour), func(ctx context.Context) error {
		return nil
	})
	if task == nil {
		t.Error("Global AddTask should return task")
	}
}

func TestTaskHandler(t *testing.T) {
	var handler cron.TaskHandler = func(ctx context.Context) error {
		return nil
	}
	if handler == nil {
		t.Error("TaskHandler should not be nil")
	}
}

func TestFullCronSchedule(t *testing.T) {
	// 测试每15分钟
	schedule := cron.ParseCron("*/15 * * * *")
	now := time.Now()
	next := schedule.Next(now)
	if next.IsZero() {
		t.Error("FullCronSchedule */15 should find next time")
	}
	// 验证分钟是0,15,30,45之一
	minute := next.Minute()
	if minute%15 != 0 {
		t.Errorf("FullCronSchedule minute = %d, should be divisible by 15", minute)
	}

	// 测试每天12点
	schedule = cron.ParseCron("0 12 * * *")
	next = schedule.Next(now)
	if next.IsZero() {
		t.Error("FullCronSchedule daily noon should find next time")
	}
	if next.Hour() != 12 || next.Minute() != 0 {
		t.Errorf("FullCronSchedule noon = %v, want 12:00", next)
	}

	// 测试工作日9-17点
	schedule = cron.ParseCron("0 9-17 * * 1-5")
	next = schedule.Next(now)
	if next.IsZero() {
		t.Error("FullCronSchedule workday should find next time")
	}
}

func TestParseCron(t *testing.T) {
	// 正常5字段表达式
	schedule := cron.ParseCron("0 12 * * *")
	if schedule.Minute != "0" || schedule.Hour != "12" {
		t.Error("ParseCron 5 fields failed")
	}

	// 无效表达式返回默认
	schedule = cron.ParseCron("invalid")
	if schedule.Minute != "*" || schedule.Hour != "*" {
		t.Error("ParseCron invalid should return default")
	}
}

func TestFullCronScheduleMatch(t *testing.T) {
	schedule := cron.FullCronSchedule{
		Minute:  "*/5",
		Hour:    "*",
		Day:     "*",
		Month:   "*",
		Weekday: "*",
	}

	// 测试匹配 - 通过Next验证
	testTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	next := schedule.Next(testTime)
	// 10:00 下一个应该是 10:05
	if next.Minute() != 5 || next.Hour() != 10 {
		t.Errorf("Next after 10:00 should be 10:05, got %v", next)
	}

	testTime = time.Date(2024, 1, 15, 10, 7, 0, 0, time.UTC)
	next = schedule.Next(testTime)
	// 10:07 下一个应该是 10:10
	if next.Minute() != 10 || next.Hour() != 10 {
		t.Errorf("Next after 10:07 should be 10:10, got %v", next)
	}
}