package database_test

import (
	"context"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

func TestCloseAllWithoutInit(t *testing.T) {
	if err := database.CloseAll(); err != nil {
		t.Fatalf("CloseAll without init should not error: %v", err)
	}
	if database.GetDB() != nil {
		t.Fatal("expected DB nil")
	}
	if database.GetReadDB() != nil {
		t.Fatal("expected read DB nil")
	}
	if len(database.GetReplicas()) != 0 {
		t.Fatal("expected replicas empty")
	}
}

func TestDBContextHelpersWithoutInit(t *testing.T) {
	ctx := database.UseMaster(context.Background())
	if db := database.GetDBFromContext(ctx); db != nil {
		t.Fatal("expected nil DB without init")
	}

	ctx = database.UseReplica(context.Background())
	if db := database.GetDBFromContext(ctx); db != nil {
		t.Fatal("expected nil read DB without init")
	}
}

func TestRoundRobinPicker(t *testing.T) {
	replicas := []*gorm.DB{{}, {}, {}}
	p := &database.RoundRobinPicker{}

	first := p.Pick(replicas)
	second := p.Pick(replicas)
	third := p.Pick(replicas)
	fourth := p.Pick(replicas)

	if first == nil || second == nil || third == nil {
		t.Fatal("Picker returned nil for non-empty replicas")
	}
	if first != replicas[0] || second != replicas[1] || third != replicas[2] {
		t.Fatal("RoundRobinPicker should cycle through replicas in order")
	}
	if fourth != replicas[0] {
		t.Fatal("RoundRobinPicker should wrap around to the first replica")
	}
	if p.Pick(nil) != nil || p.Pick([]*gorm.DB{}) != nil {
		t.Fatal("Picker should return nil for empty replicas")
	}
}

func TestRandomPicker(t *testing.T) {
	replicas := []*gorm.DB{{}, {}}
	p := &database.RandomPicker{}

	picked := p.Pick(replicas)
	if picked == nil {
		t.Fatal("RandomPicker returned nil for non-empty replicas")
	}
	if picked != replicas[0] && picked != replicas[1] {
		t.Fatal("RandomPicker returned a replica not in the slice")
	}
	if p.Pick(nil) != nil || p.Pick([]*gorm.DB{}) != nil {
		t.Fatal("RandomPicker should return nil for empty replicas")
	}
}

func TestManagerReplicaFallbackToMaster(t *testing.T) {
	mgr := database.NewManager(&config.Config{})
	if mgr.Master() != nil {
		t.Fatal("expected nil master before init")
	}
	if mgr.Replicas() != nil {
		t.Fatal("expected nil replicas before init")
	}
	// 无从库时 Replica 应返回 master（此处均为 nil）
	if mgr.Replica() != nil {
		t.Fatal("expected Replica to fall back to master when no replicas")
	}
}

func TestManagerSetPicker(t *testing.T) {
	mgr := database.NewManager(&config.Config{})
	rr := &database.RoundRobinPicker{}
	mgr.SetPicker(rr)
	if mgr.Picker() != rr {
		t.Fatal("SetPicker did not install the picker")
	}
	// nil 不应覆盖已有 picker
	mgr.SetPicker(nil)
	if mgr.Picker() != rr {
		t.Fatal("SetPicker(nil) should not clear the existing picker")
	}
}

func TestDefaultManagerHealthCheckWithoutInit(t *testing.T) {
	if err := database.DefaultManager.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected error when health checking uninitialized master")
	}
}

func TestDialectorSelectsByDriver(t *testing.T) {
	mysqlCfg := &config.Config{Database: config.DatabaseConfig{
		Driver: config.DriverMySQL, Host: "localhost", Port: 3306,
		User: "root", Password: "pass", Name: "db",
	}}
	if name := database.Dialector(mysqlCfg).Name(); name != "mysql" {
		t.Fatalf("expected mysql dialector, got %q", name)
	}

	pgCfg := &config.Config{Database: config.DatabaseConfig{
		Driver: config.DriverPostgres, Host: "localhost", Port: 5432,
		User: "postgres", Password: "pass", Name: "db",
	}}
	if name := database.Dialector(pgCfg).Name(); name != "postgres" {
		t.Fatalf("expected postgres dialector, got %q", name)
	}

	// 别名也应解析为 postgres
	pgAliasCfg := &config.Config{Database: config.DatabaseConfig{
		Driver: "PG", Host: "localhost", Port: 5432,
		User: "postgres", Password: "pass", Name: "db",
	}}
	if name := database.Dialector(pgAliasCfg).Name(); name != "postgres" {
		t.Fatalf("expected postgres dialector via alias, got %q", name)
	}

	// 未指定 Driver 时默认 mysql
	defaultCfg := &config.Config{Database: config.DatabaseConfig{
		Host: "localhost", Port: 3306, User: "root", Password: "pass", Name: "db",
	}}
	if name := database.Dialector(defaultCfg).Name(); name != "mysql" {
		t.Fatalf("expected default mysql dialector, got %q", name)
	}
}

// stubDialector 是一个用于测试 RegisterDialect 的占位 Dialector。
type stubDialector struct{ name string }

func (s stubDialector) Name() string                                            { return s.name }
func (s stubDialector) Initialize(_ *gorm.DB) error                             { return nil }
func (s stubDialector) Migrator(db *gorm.DB) gorm.Migrator                      { return nil }
func (s stubDialector) DataTypeOf(*schema.Field) string                         { return "" }
func (s stubDialector) DefaultValueOf(*schema.Field) clause.Expression          { return nil }
func (s stubDialector) BindVarTo(writer clause.Writer, _ *gorm.Statement, _ any) {}
func (s stubDialector) QuoteTo(writer clause.Writer, str string)                { _, _ = writer.WriteString(str) }
func (s stubDialector) Explain(sql string, _ ...any) string                     { return sql }

func TestRegisterDialectAndCustomDriver(t *testing.T) {
	const driver = "stubdb"

	database.RegisterDialect(database.DialectSpec{
		Name:      driver,
		Aliases:   []string{"stub"},
		Dialector: func(dsn string) gorm.Dialector { return stubDialector{name: "stubdb"} },
		DSN: func(c *config.DatabaseConfig) string {
			return "stub://" + c.Host
		},
	})

	// Dialector 工厂可以解析主名和别名
	if _, ok := database.LookupDialect(driver); !ok {
		t.Fatal("expected stubdb dialector to be registered")
	}
	if _, ok := database.LookupDialect("STUB"); !ok {
		t.Fatal("expected stub alias to be registered (case-insensitive)")
	}

	cfg := &config.Config{Database: config.DatabaseConfig{
		Driver: driver, Host: "localhost",
	}}
	if name := database.Dialector(cfg).Name(); name != "stubdb" {
		t.Fatalf("expected stubdb dialector, got %q", name)
	}

	// config.DSN() 应使用注册的 DSN 构建器
	if dsn := cfg.Database.DSN(); dsn != "stub://localhost" {
		t.Fatalf("expected DSN built by registered builder, got %q", dsn)
	}

	// 未知驱动回退到 mysql
	unknownCfg := &config.Config{Database: config.DatabaseConfig{
		Driver: "no-such-driver", Host: "localhost", Port: 3306,
		User: "root", Password: "pass", Name: "db",
	}}
	if name := database.Dialector(unknownCfg).Name(); name != "mysql" {
		t.Fatalf("expected fallback to mysql for unknown driver, got %q", name)
	}
}

func TestRegisteredDialectsContainsBuiltins(t *testing.T) {
	registered := database.RegisteredDialects()
	want := map[string]bool{"mysql": false, "postgres": false, "pg": false}
	for _, n := range registered {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("expected %q to be registered by default", k)
		}
	}
}
