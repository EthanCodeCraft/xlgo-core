package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
)

// hungDriver 是测试用 database/sql 驱动，其 Conn.PingContext 阻塞直到 ctx 取消，
// 模拟"挂起 DB"（TCP 连接活但不响应查询，区别于宕机的 connection-refused 快速失败）。
// 用于回归 H-db-1：ping 路径须经 pingWithTimeout 受 healthCheckTimeout(3s) 约束，
// 挂起 DB 不得无限阻塞探活 goroutine / 启动 / /health 端点。
type hungDriver struct{}

func (hungDriver) Open(name string) (driver.Conn, error) { return hungConn{}, nil }

type hungConn struct{}

func (hungConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("hung: not implemented") }
func (hungConn) Close() error                         { return nil }
func (hungConn) Begin() (driver.Tx, error)            { return nil, errors.New("hung: not implemented") }

// Ping 阻塞直到 ctx 取消（模拟挂起 DB 永不响应 ping）。实现 driver.Pinger 接口
// （方法名为 Ping 而非 PingContext，否则 *sql.DB.PingContext 视为 no-op 返回 nil）。
func (hungConn) Ping(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

var registerHungOnce sync.Once

func registerHungDriver() {
	registerHungOnce.Do(func() { sql.Register("xlgo_hung_hdb1", hungDriver{}) })
}

func newHungSqlDB(t *testing.T) *sql.DB {
	t.Helper()
	registerHungDriver()
	db, err := sql.Open("xlgo_hung_hdb1", "")
	if err != nil {
		t.Fatalf("sql.Open hung driver: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// newHungGormDB 构造底层 *sql.DB 为挂起驱动的 gorm.DB，其 DB() 返回挂起 *sql.DB。
func newHungGormDB(t *testing.T) *gorm.DB {
	t.Helper()
	return &gorm.DB{Config: &gorm.Config{ConnPool: newHungSqlDB(t)}}
}

// assertBoundedPing 在 max+2s 内等待 fn 返回；超时则 fail（H-db-1：ping 应被
// pingWithTimeout 3s 约束，不应无限 hang）。返回 fn 的 error 供调用方断言。
func assertBoundedPing(t *testing.T, fn func() error, max time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- fn() }()
	select {
	case err := <-done:
		if elapsed := time.Since(start); elapsed > max {
			t.Fatalf("ping 路径耗时 %v 超过上限 %v（H-db-1：应被 pingWithTimeout 3s 约束）", elapsed, max)
		}
		return err
	case <-time.After(max + 2*time.Second):
		t.Fatalf("ping 路径在挂起 DB 上无限阻塞（H-db-1：pingWithTimeout 未覆盖该路径）")
		return nil
	}
}

// TestPingWithTimeoutBoundsHungDB_Hdb1 回归 H-db-1 机制：pingWithTimeout 对挂起 DB 的
// *sql.DB.PingContext 限 healthCheckTimeout 返回，不因 ctx 无 deadline 无限阻塞。
// 修复前若直接 PingContext(Background()) 会无限阻塞。
func TestPingWithTimeoutBoundsHungDB_Hdb1(t *testing.T) {
	sqlDB := newHungSqlDB(t)
	err := assertBoundedPing(t, func() error {
		return pingWithTimeout(sqlDB, context.Background())
	}, healthCheckTimeout+1*time.Second)
	if err == nil {
		t.Fatalf("挂起 DB 的 ping 应返回超时错误，got nil")
	}
}

// TestManagerHealthCheckBoundsHungDB_Hdb1 回归 H-db-1 主路径：m.HealthCheck(Background)
// 对挂起主库经 pingWithTimeout ~3s 返回超时错误，不无限阻塞。该路径被后台探活
// probeOnce（master）与 /health 端点（app.go 经 dbm.HealthCheck）共用。
// 修复前 m.HealthCheck 用裸 sqlDB.PingContext(ctx)，Background ctx 无 deadline -> 无限阻塞。
func TestManagerHealthCheckBoundsHungDB_Hdb1(t *testing.T) {
	m := NewManager(nil)
	m.master = newHungGormDB(t)
	err := assertBoundedPing(t, func() error {
		return m.HealthCheck(context.Background())
	}, healthCheckTimeout+1*time.Second)
	if err == nil {
		t.Fatalf("挂起主库的 HealthCheck 应返回超时错误，got nil")
	}
}

// TestProbeOnceReplicaBoundsHungDB_Hdb1 回归 H-db-1 从库探活路径：probeOnce 对挂起从库
// 经 pingWithTimeout ~3s 返回，不无限阻塞探活 goroutine（#21 自愈不冻结）。master 置 nil
// 使 HealthCheck 快速返回"未初始化"，让 probeOnce 只在从库 ping 路径耗时。
// 修复前 probeOnce 从库用裸 sqlDB.PingContext(ctx)，Background ctx 无 deadline -> 无限阻塞。
func TestProbeOnceReplicaBoundsHungDB_Hdb1(t *testing.T) {
	m := NewManager(nil)
	m.master = nil // master 路径快速失败，集中测从库 ping 超时
	m.replicas = []*gorm.DB{newHungGormDB(t)}
	m.replicaHealthSet = true
	m.replicaHealthy = make([]atomic.Bool, 1)
	m.replicaHealthy[0].Store(true)

	_ = assertBoundedPing(t, func() error {
		m.probeOnce(context.Background(), 3)
		return nil
	}, healthCheckTimeout+1*time.Second)

	// 挂起从库应被标记不健康（剔除读流量，#21 自愈生效）
	if m.replicaHealthy[0].Load() {
		t.Errorf("挂起从库应被标记不健康（replicaHealthy=false），got true")
	}
}
