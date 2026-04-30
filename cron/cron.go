package cron

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Task 定时任务
type Task struct {
	Name     string        // 任务名称
	Schedule Schedule      // 调度规则
	Handler  TaskHandler   // 任务处理函数
	Enabled  bool          // 是否启用
	LastRun  time.Time     // 上次运行时间
	NextRun  time.Time     // 下次运行时间
	RunCount int           // 运行次数
}

// TaskHandler 任务处理函数
type TaskHandler func(ctx context.Context) error

// Schedule 调度接口
type Schedule interface {
	Next(now time.Time) time.Time
}

// Scheduler 调度器
type Scheduler struct {
	tasks   map[string]*Task
	mu      sync.RWMutex
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	running bool
}

// NewScheduler 创建调度器
func NewScheduler() *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		tasks:  make(map[string]*Task),
		ctx:    ctx,
		cancel: cancel,
	}
}

// AddTask 添加任务
func (s *Scheduler) AddTask(name string, schedule Schedule, handler TaskHandler) *Task {
	task := &Task{
		Name:     name,
		Schedule: schedule,
		Handler:  handler,
		Enabled:  true,
		NextRun:  schedule.Next(time.Now()),
	}

	s.mu.Lock()
	s.tasks[name] = task
	s.mu.Unlock()

	return task
}

// RemoveTask 移除任务
func (s *Scheduler) RemoveTask(name string) {
	s.mu.Lock()
	delete(s.tasks, name)
	s.mu.Unlock()
}

// EnableTask 启用任务
func (s *Scheduler) EnableTask(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[name]
	if !ok {
		return fmt.Errorf("任务不存在: %s", name)
	}

	task.Enabled = true
	task.NextRun = task.Schedule.Next(time.Now())
	return nil
}

// DisableTask 禁用任务
func (s *Scheduler) DisableTask(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[name]
	if !ok {
		return fmt.Errorf("任务不存在: %s", name)
	}

	task.Enabled = false
	return nil
}

// GetTask 获取任务
func (s *Scheduler) GetTask(name string) (*Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, ok := s.tasks[name]
	if !ok {
		return nil, fmt.Errorf("任务不存在: %s", name)
	}
	return task, nil
}

// ListTasks 获取所有任务
func (s *Scheduler) ListTasks() []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks := make([]*Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		tasks = append(tasks, task)
	}
	return tasks
}

// RunTask 立即运行任务
func (s *Scheduler) RunTask(name string) error {
	s.mu.RLock()
	task, ok := s.tasks[name]
	s.mu.RUnlock()

	if !ok {
		return fmt.Errorf("任务不存在: %s", name)
	}

	return s.runTask(task)
}

// runTask 执行任务
func (s *Scheduler) runTask(task *Task) error {
	task.LastRun = time.Now()
	task.RunCount++

	err := task.Handler(s.ctx)

	s.mu.Lock()
	task.NextRun = task.Schedule.Next(time.Now())
	s.mu.Unlock()

	return err
}

// Start 启动调度器
func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()

	s.wg.Add(1)
	go s.run()
}

// Stop 停止调度器
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	s.cancel()
	s.wg.Wait()
}

// run 运行调度循环
func (s *Scheduler) run() {
	defer s.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.checkAndRun()
		}
	}
}

// checkAndRun 检查并运行到期任务
func (s *Scheduler) checkAndRun() {
	now := time.Now()

	s.mu.RLock()
	for _, task := range s.tasks {
		if task.Enabled && !task.NextRun.IsZero() && now.After(task.NextRun) {
			go s.runTask(task)
		}
	}
	s.mu.RUnlock()
}

// IntervalSchedule 固定间隔调度
type IntervalSchedule struct {
	Interval time.Duration
}

// Next 计算下次运行时间
func (s *IntervalSchedule) Next(now time.Time) time.Time {
	return now.Add(s.Interval)
}

// Every 每隔指定时间运行
func Every(interval time.Duration) *IntervalSchedule {
	return &IntervalSchedule{Interval: interval}
}

// DailySchedule 每日定时调度
type DailySchedule struct {
	Hour   int
	Minute int
}

// Next 计算下次运行时间
func (s *DailySchedule) Next(now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), s.Hour, s.Minute, 0, 0, now.Location())
	if next.Before(now) || next.Equal(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

// Daily 每日指定时间运行
func Daily(hour, minute int) *DailySchedule {
	return &DailySchedule{Hour: hour, Minute: minute}
}

// WeeklySchedule 每周定时调度
type WeeklySchedule struct {
	Day    time.Weekday
	Hour   int
	Minute int
}

// Next 计算下次运行时间
func (s *WeeklySchedule) Next(now time.Time) time.Time {
	daysUntil := int(s.Day) - int(now.Weekday())
	if daysUntil <= 0 {
		daysUntil += 7
	}

	next := time.Date(now.Year(), now.Month(), now.Day()+daysUntil, s.Hour, s.Minute, 0, 0, now.Location())
	return next
}

// Weekly 每周指定时间运行
func Weekly(day time.Weekday, hour, minute int) *WeeklySchedule {
	return &WeeklySchedule{Day: day, Hour: hour, Minute: minute}
}

// FullCronSchedule 完整 Cron 表达式调度
// 格式: "分钟 小时 日 月 星期" (5字段)
// 示例: "0 12 * * *" 每天12点
//       "0 0 1 * *" 每月1号凌晨
//       "0 9-17 * * 1-5" 周一到周五 9点到17点每小时
type FullCronSchedule struct {
	Minute  string // 分钟: 0-59, "*", "*/n", "a-b", "a,b,c"
	Hour    string // 小时: 0-23, "*", "*/n", "a-b", "a,b,c"
	Day     string // 日: 1-31, "*", "*/n", "a-b", "a,b,c"
	Month   string // 月: 1-12, "*", "*/n", "a-b", "a,b,c"
	Weekday string // 星期: 0-6 (周日=0), "*", "*/n", "a-b", "a,b,c"
}

// Next 计算下次运行时间
func (s *FullCronSchedule) Next(now time.Time) time.Time {
	// 从下一分钟开始查找
	next := now.Add(time.Minute)
	next = time.Date(next.Year(), next.Month(), next.Day(), next.Hour(), next.Minute(), 0, 0, next.Location())

	// 最多查找一年（防止无效表达式死循环）
	maxAttempts := 366 * 24 * 60
	for i := 0; i < maxAttempts; i++ {
		if s.match(next) {
			return next
		}
		next = next.Add(time.Minute)
	}
	return time.Time{}
}

// match 检查时间是否匹配表达式
func (s *FullCronSchedule) match(t time.Time) bool {
	return s.matchField(s.Minute, t.Minute(), 0, 59) &&
		s.matchField(s.Hour, t.Hour(), 0, 23) &&
		s.matchField(s.Day, t.Day(), 1, 31) &&
		s.matchField(s.Month, int(t.Month()), 1, 12) &&
		s.matchField(s.Weekday, int(t.Weekday()), 0, 6)
}

// matchField 匹配单个字段
func (s *FullCronSchedule) matchField(field string, value int, min, max int) bool {
	if field == "*" {
		return true
	}

	// 处理范围 "a-b"
	if strings.Contains(field, "-") {
		parts := strings.Split(field, "-")
		if len(parts) == 2 {
			start := parseInt(parts[0])
			end := parseInt(parts[1])
			return value >= start && value <= end
		}
	}

	// 处理步长 "*/n"
	if strings.HasPrefix(field, "*/") {
		step := parseInt(strings.TrimPrefix(field, "*/"))
		if step > 0 {
			return (value - min) % step == 0
		}
		return true
	}

	// 处理列表 "a,b,c"
	for _, p := range strings.Split(field, ",") {
		if parseInt(strings.TrimSpace(p)) == value {
			return true
		}
	}

	return false
}

// ParseCron 解析完整 Cron 表达式
// 格式: "分钟 小时 日 月 星期"
// 示例:
//
//	"0 12 * * *"       - 每天12:00
//	"*/15 * * * *"     - 每15分钟
//	"0 9-17 * * 1-5"   - 工作日9-17点每小时
//	"0 0 1 * *"        - 每月1号凌晨
//	"0 0 * * 0"        - 每周日凌晨
func ParseCron(expr string) *FullCronSchedule {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		// 默认每分钟执行
		return &FullCronSchedule{"*", "*", "*", "*", "*"}
	}
	return &FullCronSchedule{
		Minute:  fields[0],
		Hour:    fields[1],
		Day:     fields[2],
		Month:   fields[3],
		Weekday: fields[4],
	}
}

// CronSchedule 简化 Cron 调度（仅分钟和小时）
type CronSchedule struct {
	Minute string // 分钟: "*" 或具体值如 "0,15,30"
	Hour   string // 小时: "*" 或具体值如 "8,12"
}

// Next 计算下次运行时间
func (s *CronSchedule) Next(now time.Time) time.Time {
	for i := 1; i <= 60*24; i++ { // 最多查找24小时
		next := now.Add(time.Duration(i) * time.Minute)
		if s.matchMinute(next.Minute()) && s.matchHour(next.Hour()) {
			return next
		}
	}
	return time.Time{}
}

func (s *CronSchedule) matchMinute(minute int) bool {
	return s.Minute == "*" || s.matchValue(s.Minute, minute)
}

func (s *CronSchedule) matchHour(hour int) bool {
	return s.Hour == "*" || s.matchValue(s.Hour, hour)
}

func (s *CronSchedule) matchValue(pattern string, value int) bool {
	for _, p := range splitPattern(pattern) {
		if p == value {
			return true
		}
	}
	return false
}

func splitPattern(pattern string) []int {
	var values []int
	for _, p := range split(pattern, ',') {
		v := parseInt(p)
		if v >= 0 {
			values = append(values, v)
		}
	}
	return values
}

func split(s string, sep byte) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func parseInt(s string) int {
	var v int
	for _, c := range s {
		if c >= '0' && c <= '9' {
			v = v*10 + int(c-'0')
		}
	}
	return v
}

// Cron 创建类 Cron 调度
func Cron(minute, hour string) *CronSchedule {
	return &CronSchedule{Minute: minute, Hour: hour}
}

// 全局调度器
var globalScheduler *Scheduler
var schedulerOnce sync.Once

// GetScheduler 获取全局调度器
func GetScheduler() *Scheduler {
	schedulerOnce.Do(func() {
		globalScheduler = NewScheduler()
	})
	return globalScheduler
}

// AddTask 添加任务到全局调度器
func AddTask(name string, schedule Schedule, handler TaskHandler) *Task {
	return GetScheduler().AddTask(name, schedule, handler)
}

// Start 启动全局调度器
func Start() {
	GetScheduler().Start()
}

// Stop 停止全局调度器
func Stop() {
	GetScheduler().Stop()
}