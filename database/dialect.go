package database

import (
	"fmt"
	"strings"
	"sync"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// 内置驱动常量（更多驱动可通过 RegisterDialect 扩展）
const (
	DriverMySQL    = config.DriverMySQL
	DriverPostgres = config.DriverPostgres
)

// DialectorFactory 根据 DSN 返回 GORM Dialector
type DialectorFactory func(dsn string) gorm.Dialector

// DialectSpec 描述一种数据库方言：如何建立连接 + 如何拼接 DSN
type DialectSpec struct {
	// Name 驱动主名称（如 "mysql"、"postgres"、"sqlite"），大小写不敏感
	Name string
	// Aliases 驱动别名（如 postgres 的 "postgresql"、"pg"）
	Aliases []string
	// Dialector 由 DSN 构造 GORM Dialector
	Dialector DialectorFactory
	// DSN 由 DatabaseConfig 拼接连接字符串。可选——
	// 不提供时使用 cfg.MySQLDSN() 兜底（适合自定义驱动通过 CustomDSN 指定连接串的场景）
	DSN config.DSNBuilder
}

var (
	dialectsMu sync.RWMutex
	dialects   = map[string]DialectorFactory{}
)

// RegisterDialect 注册一种数据库方言。
// 同时把 DSN 构建器登记到 config 包，使 cfg.Database.DSN() 也能识别新驱动。
// 已注册的同名驱动会被覆盖。
//
// 用法示例（接入 SQLite）：
//
//	import "gorm.io/driver/sqlite"
//
//	database.RegisterDialect(database.DialectSpec{
//	    Name:      "sqlite",
//	    Dialector: func(dsn string) gorm.Dialector { return sqlite.Open(dsn) },
//	    DSN:       func(c *config.DatabaseConfig) string { return c.Name }, // 文件路径
//	})
func RegisterDialect(spec DialectSpec) {
	if spec.Dialector == nil || strings.TrimSpace(spec.Name) == "" {
		return
	}

	dialectsMu.Lock()
	for _, n := range append([]string{spec.Name}, spec.Aliases...) {
		key := normalizeDriver(n)
		if key != "" {
			dialects[key] = spec.Dialector
		}
	}
	dialectsMu.Unlock()

	if spec.DSN != nil {
		config.RegisterDSNBuilder(spec.Name, spec.DSN, spec.Aliases...)
	}
}

// LookupDialect 查找已注册的 Dialector 工厂
func LookupDialect(driver string) (DialectorFactory, bool) {
	key := normalizeDriver(driver)
	dialectsMu.RLock()
	defer dialectsMu.RUnlock()
	f, ok := dialects[key]
	return f, ok
}

// RegisteredDialects 返回所有已注册的驱动名（用于诊断）
func RegisteredDialects() []string {
	dialectsMu.RLock()
	defer dialectsMu.RUnlock()
	names := make([]string, 0, len(dialects))
	for k := range dialects {
		names = append(names, k)
	}
	return names
}

// Dialector 根据配置返回 GORM Dialector。
// 驱动由 cfg.Database.Driver 决定，未指定或未注册时按 MySQL 兜底（向后兼容）。
func Dialector(cfg *config.Config) gorm.Dialector {
	return dialectorForDSN(cfg.Database.Driver, cfg.Database.DSN())
}

// dialectorForDSN 根据驱动名和 DSN 返回 Dialector
func dialectorForDSN(driver, dsn string) gorm.Dialector {
	if f, ok := LookupDialect(driver); ok {
		return f(dsn)
	}
	// 未注册时回退到 MySQL，与 config.DSN() 的回退保持一致
	return mysql.Open(dsn)
}

// normalizeDriver 规范化驱动名（小写、去空白）
func normalizeDriver(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// driverDescription 返回带别名提示的驱动描述（用于错误信息和日志）
func driverDescription(driver string) string {
	key := normalizeDriver(driver)
	if key == "" {
		return DriverMySQL + " (default)"
	}
	if _, ok := LookupDialect(key); ok {
		return key
	}
	return fmt.Sprintf("%s (unregistered, fallback=%s)", key, DriverMySQL)
}

func init() {
	// 内置 MySQL
	RegisterDialect(DialectSpec{
		Name:      DriverMySQL,
		Dialector: func(dsn string) gorm.Dialector { return mysql.Open(dsn) },
		DSN:       func(c *config.DatabaseConfig) string { return c.MySQLDSN() },
	})
	// 内置 PostgreSQL
	RegisterDialect(DialectSpec{
		Name:      DriverPostgres,
		Aliases:   []string{"postgresql", "pg"},
		Dialector: func(dsn string) gorm.Dialector { return postgres.Open(dsn) },
		DSN:       func(c *config.DatabaseConfig) string { return c.PostgresDSN() },
	})
}
