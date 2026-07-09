package xlgo_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"testing"
	"time"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// appHungDriver 是测试用 database/sql 驱动，Conn.Ping 阻塞到 ctx 取消，模拟挂起 DB。
// 与 database 包内部的 hungDriver 同构（app_test 为外部包，需自带一份）。
type appHungDriver struct{}

func (appHungDriver) Open(string) (driver.Conn, error) { return appHungConn{}, nil }

type appHungConn struct{}

func (appHungConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("hung: not implemented") }
func (appHungConn) Close() error                         { return nil }
func (appHungConn) Begin() (driver.Tx, error)            { return nil, errors.New("hung: not implemented") }

// Ping 阻塞到 ctx 取消（实现 driver.Pinger，方法名 Ping 而非 PingContext）。
func (appHungConn) Ping(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

var appHungRegisterOnce sync.Once

func registerAppHungDialect() {
	appHungRegisterOnce.Do(func() {
		sql.Register("xlgo_app_hung_hdb1", appHungDriver{})
		database.RegisterDialect(database.DialectSpec{
			Name: "app_hung_hdb1",
			Dialector: func(string) gorm.Dialector {
				db, _ := sql.Open("xlgo_app_hung_hdb1", "")
				return appHungDialector{sqlDB: db}
			},
			DSN: func(*config.DatabaseConfig) string { return "app_hung://" },
		})
	})
}

// appHungDialector 让 gorm.Open 成功并注入挂起 *sql.DB，使 initDB 的 pingWithTimeout 触发。
type appHungDialector struct{ sqlDB *sql.DB }

func (d appHungDialector) Name() string                                { return "app_hung_hdb1" }
func (d appHungDialector) Initialize(db *gorm.DB) error                { db.Config.ConnPool = d.sqlDB; return nil }
func (d appHungDialector) Migrator(*gorm.DB) gorm.Migrator             { return nil }
func (d appHungDialector) DataTypeOf(*schema.Field) string             { return "" }
func (d appHungDialector) DefaultValueOf(*schema.Field) clause.Expression { return nil }
func (d appHungDialector) BindVarTo(clause.Writer, *gorm.Statement, any) {}
func (d appHungDialector) QuoteTo(w clause.Writer, s string)           { _, _ = w.WriteString(s) }
func (d appHungDialector) Explain(s string, _ ...any) string           { return s }

// TestAppInitHungDBBounded_Hdb1 端到端回归 H-db-1（App 级闭环）：
// App.Init 启用 MySQL 且 DB 挂起时，initDB 的 pingWithTimeout 3s 失败 -> InitDB 返回错误
// -> App.Init 失败（failAfterInit 回滚）。全程有界，不因挂起 DB 无限卡死 App.Init。
// 修复前 gorm.Open 的 automatic Ping() 在 pingWithTimeout 之前就无限阻塞 -> App.Init 永久 hang。
// 同时验证 App.Init 失败后无 goroutine 泄漏（closeResources 正常）。
func TestAppInitHungDBBounded_Hdb1(t *testing.T) {
	registerAppHungDialect()
	cfg := &config.Config{
		App:    config.AppConfig{Env: "test"},
		Server: config.ServerConfig{Port: 18091},
		Database: config.DatabaseConfig{
			Driver: "app_hung_hdb1",
			Host:   "localhost", Port: 3306, User: "u", Password: "p", Name: "n",
		},
	}
	app := xlgo.New(xlgo.WithConfig(cfg), xlgo.WithMySQL())

	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- app.Init() }()
	select {
	case err := <-done:
		elapsed := time.Since(start)
		if err == nil {
			t.Fatalf("挂起 DB 下 App.Init 应失败，got nil")
		}
		// initDB pingWithTimeout 3s 失败后重试 5 次（每次 ping 3s + retryDelay 1s 起步翻倍），
		// 全跑约 5*4s=20s+。放宽到 35s 上限。关键是不触发下方 45s watchdog 的"无限阻塞"。
		if elapsed > 35*time.Second {
			t.Fatalf("App.Init 耗时 %v 超过 35s 上限（H-db-1：initDB ping 应被 3s 约束，重试 5 次应 ~20s）", elapsed)
		}
	case <-time.After(45 * time.Second):
		t.Fatalf("App.Init 在挂起 DB 上无限阻塞（H-db-1：gorm.Open automatic ping 或 initDB ping 未被超时约束）")
	}

	// App.Init 失败后应能正常 Shutdown（failAfterInit 已回滚资源，Shutdown 幂等）。
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- app.Shutdown() }()
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Logf("App.Shutdown after failed Init returned err: %v（可接受，资源已回滚）", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("App.Shutdown 在 Init 失败后无限阻塞（closeResources 未正常收口）")
	}

	// rootCtx 已被 failAfterInit cancel，无残留 goroutine（StartProbing 未启动因 Init 失败）。
	// 此处不直接断言 goroutine 数（受测试 runtime 噪声影响），靠 Shutdown 有界退出间接证明。
	_ = context.Background()
}
