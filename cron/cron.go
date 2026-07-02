package cron

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

	// running 防止长任务跨 tick 重叠执行（C12b）。
	// 用指针以便 GetTask/ListTasks 返回 Task 拷贝时不触发 atomic.Bool 值类型的
	// copylocks 警告；拷贝共享同一守卫状态，但该字段未导出，外部无法操作。
	// nil 表示未初始化（仅非 AddTask 构造的 Task），checkAndRun/RunTask 以 nil 守卫跳过。
	running *atomic.Bool
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
		running:  &atomic.Bool{},
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

// GetTask 获取任务（返回拷贝快照，避免外部并发读 live 指针，C12a）。
func (s *Scheduler) GetTask(name string) (*Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, ok := s.tasks[name]
	if !ok {
		return nil, fmt.Errorf("任务不存在: %s", name)
	}
	cp := *task
	return &cp, nil
}

// ListTasks 获取所有任务（返回拷贝快照，C12a）。
func (s *Scheduler) ListTasks() []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks := make([]*Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		cp := *task
		tasks = append(tasks, &cp)
	}
	return tasks
}

// RunTask 立即运行任务（手动触发，同步返回 handler 错误）。
//
// 占用 per-task running 守卫，与调度循环互斥，防止同一任务重叠执行（C12b）。
// 不推进 NextRun（手动触发不影响调度节奏，C12c）。LastRun/RunCount 在锁内更新（C12a）。
func (s *Scheduler) RunTask(name string) error {
	s.mu.Lock()
	task, ok := s.tasks[name]
	s.mu.Unlock()

	if !ok {
		return fmt.Errorf("任务不存在: %s", name)
	}

	// 占用 running 守卫（nil 守卫直接放行，仅防御非 AddTask 构造的 Task）。
	if task.running != nil && !task.running.CompareAndSwap(false, true) {
		return fmt.Errorf("任务正在执行中: %s", name)
	}
	defer func() {
		if task.running != nil {
			task.running.Store(false)
		}
	}()

	err := task.Handler(s.ctx)

	s.mu.Lock()
	task.LastRun = time.Now()
	task.RunCount++
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

// checkAndRun 检查并运行到期任务。
//
// C12b：per-task running 守卫（CAS）防止长任务跨 tick 重叠 spawn。
// C12c：占用守卫后**先推进 NextRun**（以上次 NextRun 锚定，非 time.Now()），
// 避免每周期累积 handler 时长致调度漂移；推进在 spawn 前，下次 tick 不会重复 spawn。
// C12a：NextRun 推进在写锁内；LastRun/RunCount 在 goroutine 内写锁更新。
func (s *Scheduler) checkAndRun() {
	now := time.Now()

	s.mu.Lock()
	for _, task := range s.tasks {
		if !task.Enabled || task.NextRun.IsZero() || !now.After(task.NextRun) {
			continue
		}
		// 占用 running 守卫；正在执行则跳过本轮（防重叠 C12b）。
		if task.running != nil && !task.running.CompareAndSwap(false, true) {
			continue
		}
		// 先推进 NextRun（以上次 NextRun 锚定防漂移 C12c），再 spawn。
		task.NextRun = task.Schedule.Next(task.NextRun)
		s.wg.Add(1)
		go func(t *Task) {
			defer s.wg.Done()
			defer func() {
				if t.running != nil {
					t.running.Store(false)
				}
			}()

			err := t.Handler(s.ctx)

			s.mu.Lock()
			t.LastRun = time.Now()
			t.RunCount++
			s.mu.Unlock()
			_ = err
		}(task)
	}
	s.mu.Unlock()
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

// Next 计算下次运行时间。
//
// C12d：原实现 `daysUntil <= 0 → +7` 仅按 weekday 差值，不比较当天时刻，
// 当天目标时刻未到（如周一 9:00 目标、当前周一 12:00 之前的 8:00）被错误跳一周。
// 改为：先算今天的目标时刻，按 `((day-now)+7)%7` 加天数，再与 now 比较——
// 当天未到点则本周，当天已过则下周。
func (s *WeeklySchedule) Next(now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), s.Hour, s.Minute, 0, 0, now.Location())
	daysUntil := (int(s.Day) - int(now.Weekday()) + 7) % 7
	next = next.AddDate(0, 0, daysUntil)
	if !next.After(now) {
		next = next.AddDate(0, 0, 7)
	}
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

// matchField 匹配单个字段（C12e 重写）。
//
// 旧实现先判 `-` 再判 `*/` 最后列表，且 parseInt 忽略非数字逐位累积：
//   - `1-5,8` 因整字段含 `-` 被当范围，parseInt("5,8")=58 → 范围被破坏为 1..58，列表项丢失。
//   - `garbage` → parseInt=0，分/时/周字段 value=0 时误触发。
//   - `*/garbage` → step=0 → return true 匹配全部。
//   - 周日 `7` 不匹配（Go Sunday=0）。
//
// 新实现：先按逗号拆列表，每项独立判 `*/n` / `a-b/n` / `a-b` / 单值（列表分支独立于范围分支）；
// 全部用 strconv.Atoi 返错；weekday 字段（min=0,max=6）7→0，范围 lo>hi 环绕。
func (s *FullCronSchedule) matchField(field string, value int, min, max int) bool {
	if field == "*" {
		return true
	}
	for _, raw := range strings.Split(field, ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		if matchCronItem(item, value, min, max) {
			return true
		}
	}
	return false
}

// matchCronItem 处理单个 cron 字段项（已按逗号拆分）。
func matchCronItem(item string, value, min, max int) bool {
	isWeekday := min == 0 && max == 6

	// 步长 "*/n" 或 "a-b/n"
	if idx := strings.Index(item, "/"); idx >= 0 {
		base := item[:idx]
		step, err := strconv.Atoi(item[idx+1:])
		if err != nil || step <= 0 {
			return false
		}
		lo, hi := min, max
		if base != "*" {
			rlo, rhi, err := parseCronRange(base, min, max)
			if err != nil {
				return false
			}
			lo, hi = rlo, rhi
		}
		return value >= lo && value <= hi && (value-lo)%step == 0
	}

	// 范围 "a-b"
	if strings.Contains(item, "-") {
		lo, hi, err := parseCronRange(item, min, max)
		if err != nil {
			return false
		}
		if isWeekday && lo > hi {
			// 环绕：lo..6 ∪ 0..hi（如 "6-1" = 周六、周日、周一）
			return (value >= lo && value <= max) || (value >= min && value <= hi)
		}
		return value >= lo && value <= hi
	}

	// 单值
	v, err := strconv.Atoi(item)
	if err != nil {
		return false
	}
	if isWeekday && v == 7 {
		v = 0
	}
	if v < min || v > max {
		return false
	}
	return v == value
}

// parseCronRange 解析 "a-b" 范围，含边界校验与 weekday 7→0 归一化。
func parseCronRange(s string, min, max int) (int, int, error) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid range %q", s)
	}
	lo, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	hi, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("invalid range %q", s)
	}
	isWeekday := min == 0 && max == 6
	if isWeekday {
		// 7 → 0（周日）。但若两端归一化后都为 0 而原始值不同（如 "0-7"/"7-0"），
		// 语义为"整周"却坍缩成"仅周日"，属歧义范围，拒绝以免静默错误匹配。
		nlo, nhi := lo, hi
		if nlo == 7 {
			nlo = 0
		}
		if nhi == 7 {
			nhi = 0
		}
		if nlo == nhi && lo != hi {
			return 0, 0, fmt.Errorf("ambiguous weekday range %q (0 与 7 均为周日)", s)
		}
		lo, hi = nlo, nhi
	}
	if lo < min || lo > max || hi < min || hi > max {
		return 0, 0, fmt.Errorf("range out of bounds %q", s)
	}
	return lo, hi, nil
}

// ParseCron 解析完整 Cron 表达式。
// 格式: "分钟 小时 日 月 星期"
// 示例:
//
//	"0 12 * * *"       - 每天12:00
//	"*/15 * * * *"     - 每15分钟
//	"0 9-17 * * 1-5"   - 工作日9-17点每小时
//	"0 0 1 * *"        - 每月1号凌晨
//	"0 0 * * 0"        - 每周日凌晨
//
// 非法表达式回退默认全 "*"（每分钟执行）。需要严格校验请用 ParseCronStrict。
func ParseCron(expr string) *FullCronSchedule {
	if sched, err := ParseCronStrict(expr); err == nil {
		return sched
	}
	return &FullCronSchedule{"*", "*", "*", "*", "*"}
}

// ParseCronStrict 严格解析 Cron 表达式，校验字段数与各字段范围，非法返 error。
// 字段范围：分钟 0-59，小时 0-23，日 1-31，月 1-12，星期 0-6（周日=0，7 归一为 0）。
func ParseCronStrict(expr string) (*FullCronSchedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron: 需要 5 个字段，实际 %d", len(fields))
	}
	specs := []struct {
		val      string
		min, max int
		name     string
	}{
		{fields[0], 0, 59, "minute"},
		{fields[1], 0, 23, "hour"},
		{fields[2], 1, 31, "day"},
		{fields[3], 1, 12, "month"},
		{fields[4], 0, 6, "weekday"},
	}
	for _, sp := range specs {
		if err := validateCronField(sp.val, sp.min, sp.max, sp.name); err != nil {
			return nil, err
		}
	}
	return &FullCronSchedule{
		Minute:  fields[0],
		Hour:    fields[1],
		Day:     fields[2],
		Month:   fields[3],
		Weekday: fields[4],
	}, nil
}

// validateCronField 校验单个 cron 字段语法与范围。
func validateCronField(field string, min, max int, name string) error {
	if field == "*" {
		return nil
	}
	for _, raw := range strings.Split(field, ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			return fmt.Errorf("cron %s: 空列表项", name)
		}
		if err := validateCronItem(item, min, max, name); err != nil {
			return err
		}
	}
	return nil
}

func validateCronItem(item string, min, max int, name string) error {
	isWeekday := min == 0 && max == 6
	norm := func(v int) int {
		if isWeekday && v == 7 {
			return 0
		}
		return v
	}
	if idx := strings.Index(item, "/"); idx >= 0 {
		base := item[:idx]
		step, err := strconv.Atoi(item[idx+1:])
		if err != nil || step <= 0 {
			return fmt.Errorf("cron %s: 非法步长 %q", name, item)
		}
		if base == "*" {
			return nil
		}
		lo, hi, err := parseCronRange(base, min, max)
		if err != nil {
			return fmt.Errorf("cron %s: %v", name, err)
		}
		_ = lo
		_ = hi
		return nil
	}
	if strings.Contains(item, "-") {
		if _, _, err := parseCronRange(item, min, max); err != nil {
			return fmt.Errorf("cron %s: %v", name, err)
		}
		return nil
	}
	v, err := strconv.Atoi(item)
	if err != nil {
		return fmt.Errorf("cron %s: 非数字 %q", name, item)
	}
	if v = norm(v); v < min || v > max {
		return fmt.Errorf("cron %s: %d 超出范围 [%d,%d]", name, v, min, max)
	}
	return nil
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

// splitPattern 将逗号分隔的模式解析为值列表（C12e：用 strconv.Atoi，非法项跳过）。
func splitPattern(pattern string) []int {
	var values []int
	for _, p := range strings.Split(pattern, ",") {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			continue
		}
		values = append(values, v)
	}
	return values
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