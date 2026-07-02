package database

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/logger"
	"gorm.io/gorm"
)

func init() {
	// 部分用例触发 logger.Warnf 路径，确保 logger 已初始化避免 nil deref。
	logger.Close()
}

// sentinelDB 返回一个可安全调用 .DB() 的 gorm.DB 占位实例。
// 直接用 &gorm.DB{} 会令内嵌 *Config 为 nil，.DB() 访问提升字段 ConnPool 时 nil deref；
// 这里给定非 nil Config，.DB() 走到 nil ConnPool 分支返回 ErrInvalidDB 而不 panic。
func sentinelDB() *gorm.DB {
	return &gorm.DB{Config: &gorm.Config{}}
}

// TestC11MasterReplicasConcurrentReadWrite 验证 Master/Replicas/Replica 全程加锁，
// 与并发重建路径（写 master/replicas）无数据竞争（C11d）。
// 修复前 Master/Replicas 为裸读，-race 必采到竞争。
func TestC11MasterReplicasConcurrentReadWrite(t *testing.T) {
	m := &Manager{picker: &RandomPicker{}}
	sentinels := []*gorm.DB{{}, {}, {}}
	replicaSets := [][]*gorm.DB{
		{sentinels[0], sentinels[1]},
		{sentinels[2]},
		{},
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 写者：在锁内置换 master/replicas（模拟 InitDB/InitDBWithReplicas/Close 的写路径）
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			m.mu.Lock()
			m.master = sentinels[i%len(sentinels)]
			m.replicas = replicaSets[i%len(replicaSets)]
			m.mu.Unlock()
		}
	}()

	// 读者：并发调用读取方法
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = m.Master()
				_ = m.Replicas()
				_ = m.Replica()
				_ = m.FromContext(context.Background())
			}
		}()
	}

	// 让竞争窗口运行一段时间，确保 -race 能采到（若有未加锁读）
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestC11ReplicasReturnsCopy 验证 Replicas 返回拷贝，调用方修改不影响内部状态（C11d）。
func TestC11ReplicasReturnsCopy(t *testing.T) {
	m := &Manager{picker: &RandomPicker{}}
	m.mu.Lock()
	m.replicas = []*gorm.DB{{}, {}}
	m.mu.Unlock()

	got := m.Replicas()
	if len(got) != 2 {
		t.Fatalf("expected 2 replicas, got %d", len(got))
	}
	got[0] = nil

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.replicas[0] == nil {
		t.Fatal("Replicas should return a copy, not the live slice")
	}
}

// TestC11ReplicaHealthResetOnRebuild 验证重建从库前重置健康状态，
// 使 initReplicaHealth 按新 replicas 长度重建（C11a）。
// 修复前 initReplicaHealth 的 replicaHealthSet 早返回导致健康切片与新 replicas 长度错位。
func TestC11ReplicaHealthResetOnRebuild(t *testing.T) {
	m := &Manager{picker: &RandomPicker{}}

	// 首轮：2 个从库 + 健康初始化
	m.mu.Lock()
	m.replicas = []*gorm.DB{{}, {}}
	m.mu.Unlock()
	m.initReplicaHealth()
	if !m.replicaHealthSet || len(m.replicaHealthy) != 2 {
		t.Fatalf("expected health init for 2 replicas, got set=%v len=%d", m.replicaHealthSet, len(m.replicaHealthy))
	}

	// 重建：3 个从库 + 重置健康状态（C11a/C11c 路径）
	m.mu.Lock()
	m.replicas = []*gorm.DB{{}, {}, {}}
	m.resetReplicaHealth()
	m.mu.Unlock()
	m.initReplicaHealth()
	if !m.replicaHealthSet || len(m.replicaHealthy) != 3 {
		t.Fatalf("C11a: expected re-init aligned with 3 new replicas, got set=%v len=%d", m.replicaHealthSet, len(m.replicaHealthy))
	}
	for i := range m.replicaHealthy {
		if !m.replicaHealthy[i].Load() {
			t.Fatal("re-init should mark all replicas healthy")
		}
	}
}

// TestC11ReplicaHealthStaleWithoutReset 反向验证：不调 resetReplicaHealth 时
// initReplicaHealth 早返回，健康切片长度不随新 replicas 变化（复现 C11a 缺陷根因）。
func TestC11ReplicaHealthStaleWithoutReset(t *testing.T) {
	m := &Manager{picker: &RandomPicker{}}
	m.mu.Lock()
	m.replicas = []*gorm.DB{{}, {}}
	m.mu.Unlock()
	m.initReplicaHealth()

	// 仅换 replicas 不重置健康状态
	m.mu.Lock()
	m.replicas = []*gorm.DB{{}, {}, {}}
	m.mu.Unlock()
	m.initReplicaHealth() // replicaHealthSet 仍为 true → 早返回

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.replicaHealthy) == 3 {
		t.Fatal("without reset, initReplicaHealth should NOT re-align (early-returns); this proves reset is required")
	}
	if len(m.replicaHealthy) != 2 {
		t.Fatalf("expected stale len=2, got %d", len(m.replicaHealthy))
	}
}

// TestC11ManagerCloseResetsState 验证 Close 清空 master/replicas 并重置健康状态（C11a/C11c/C11d）。
func TestC11ManagerCloseResetsState(t *testing.T) {
	m := &Manager{picker: &RandomPicker{}}
	m.mu.Lock()
	m.master = sentinelDB()
	m.replicas = []*gorm.DB{sentinelDB(), sentinelDB()}
	m.replicaHealthy = make([]atomic.Bool, 2)
	m.replicaHealthy[0].Store(true)
	m.replicaHealthy[1].Store(true)
	m.replicaHealthSet = true
	m.mu.Unlock()
	m.healthy.Store(true)

	// closeDB 对空 gorm.DB{}（无 ConnPool）返回 ErrInvalidDB，Close 收集后 join 返回；
	// 这里不关心关闭错误，只断言状态被重置。
	_ = m.Close()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.master != nil {
		t.Fatal("expected master nil after Close")
	}
	if m.replicas != nil {
		t.Fatal("expected replicas nil after Close")
	}
	if m.replicaHealthSet {
		t.Fatal("expected replicaHealthSet reset after Close")
	}
	if m.replicaHealthy != nil {
		t.Fatal("expected replicaHealthy nil after Close")
	}
	if m.healthy.Load() {
		t.Fatal("expected healthy=false after Close")
	}
}

// TestC11PackageCloseClosesReplicas 验证包级 Close 委托 CloseAll，
// 关闭从库而非仅主库（C11f）。修复前包级 Close 仅关 master，replicas 残留。
func TestC11PackageCloseClosesReplicas(t *testing.T) {
	// 保存并恢复 DefaultManager 状态，避免污染其他用例
	defer func() {
		DefaultManager.mu.Lock()
		DefaultManager.master = nil
		DefaultManager.replicas = nil
		DefaultManager.resetReplicaHealth()
		DefaultManager.healthy.Store(false)
		DefaultManager.mu.Unlock()
	}()

	DefaultManager.mu.Lock()
	DefaultManager.master = sentinelDB()
	DefaultManager.replicas = []*gorm.DB{sentinelDB(), sentinelDB()}
	DefaultManager.mu.Unlock()

	// 包级 Close → CloseAll → DefaultManager.Close()，应同时清空 master 与 replicas
	_ = Close()

	DefaultManager.mu.Lock()
	master := DefaultManager.master
	repl := DefaultManager.replicas
	DefaultManager.mu.Unlock()

	if master != nil {
		t.Fatal("C11f: expected master nil after package Close")
	}
	if repl != nil {
		t.Fatal("C11f: package Close should close replicas too (delegates to CloseAll), got residual replicas")
	}
}

// TestC11HealthCheckLockedRead 验证 HealthCheck 在锁内快照 master，未初始化时返错（C11d）。
func TestC11HealthCheckLockedRead(t *testing.T) {
	m := &Manager{picker: &RandomPicker{}}
	if err := m.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected error when health checking uninitialized master")
	}

	// 空 gorm.DB（无 ConnPool）：DB() 返 ErrInvalidDB，HealthCheck 返该错而非 panic
	m.mu.Lock()
	m.master = sentinelDB()
	m.mu.Unlock()
	if err := m.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected error for gorm.DB without ConnPool")
	}
}
