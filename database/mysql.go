package database

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/logger"

	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var (
	// DB 主库实例（写操作）
	DB *gorm.DB
	// DBRead 读库实例（读操作）
	DBRead *gorm.DB
	// replicas 从库列表
	replicas []*gorm.DB
	// replicaMutex 从库选择锁
	replicaMutex sync.Mutex
)

// InitMySQL 初始化 MySQL 连接（带重试机制）
func InitMySQL(cfg *config.Config) error {
	var err error

	// GORM 日志配置
	var gormLogLevel gormlogger.LogLevel
	if cfg.IsDevelopment() {
		gormLogLevel = gormlogger.Info
	} else {
		gormLogLevel = gormlogger.Warn
	}

	gormConfig := &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormLogLevel),
	}

	// 重试配置
	maxRetries := 5
	retryDelay := time.Second

	for i := range maxRetries {
		// 连接主库
		DB, err = gorm.Open(mysql.Open(cfg.Database.DSN()), gormConfig)
		if err == nil {
			sqlDB, err := DB.DB()
			if err == nil {
				sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
				sqlDB.SetMaxOpenConns(cfg.Database.MaxOpenConns)
				sqlDB.SetConnMaxLifetime(time.Hour)

				if err := sqlDB.Ping(); err == nil {
					logger.Info("MySQL 主库连接成功", zap.String("host", cfg.Database.Host), zap.Int("port", cfg.Database.Port))
					return nil
				}
			}
		}

		logger.Warnf("MySQL 连接失败，第 %d/%d 次重试: %v", i+1, maxRetries, err)
		time.Sleep(retryDelay)
		retryDelay *= 2
		if retryDelay > 30*time.Second {
			retryDelay = 30 * time.Second
		}
	}

	return fmt.Errorf("MySQL 连接失败（重试 %d 次）: %w", maxRetries, err)
}

// InitMySQLWithReplicas 初始化 MySQL 主从连接
// masterDSN: 主库连接字符串
// replicaDSNs: 从库连接字符串列表
func InitMySQLWithReplicas(cfg *config.Config, replicaDSNs []string) error {
	// 先初始化主库
	if err := InitMySQL(cfg); err != nil {
		return err
	}

	// 初始化从库
	if len(replicaDSNs) > 0 {
		var gormLogLevel gormlogger.LogLevel
		if cfg.IsDevelopment() {
			gormLogLevel = gormlogger.Info
		} else {
			gormLogLevel = gormlogger.Warn
		}

		gormConfig := &gorm.Config{
			Logger: gormlogger.Default.LogMode(gormLogLevel),
		}

		for i, dsn := range replicaDSNs {
			replicaDB, err := gorm.Open(mysql.Open(dsn), gormConfig)
			if err != nil {
				logger.Warnf("MySQL 从库 %d 连接失败: %v", i+1, err)
				continue
			}

			sqlDB, err := replicaDB.DB()
			if err != nil {
				logger.Warnf("MySQL 从库 %d 获取连接池失败: %v", i+1, err)
				continue
			}

			sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
			sqlDB.SetMaxOpenConns(cfg.Database.MaxOpenConns / 2) // 从库连接数可适当减少
			sqlDB.SetConnMaxLifetime(time.Hour)

			if err := sqlDB.Ping(); err != nil {
				logger.Warnf("MySQL 从库 %d Ping 失败: %v", i+1, err)
				continue
			}

			replicas = append(replicas, replicaDB)
			logger.Info("MySQL 从库连接成功", zap.Int("index", i+1))
		}

		// 设置默认读库
		if len(replicas) > 0 {
			DBRead = replicas[0]
		} else {
			DBRead = DB // 无从库时使用主库
		}
	} else {
		DBRead = DB // 无从库配置时使用主库
	}

	return nil
}

// GetReadDB 获取读库实例（自动选择从库）
func GetReadDB() *gorm.DB {
	if len(replicas) == 0 {
		return DB
	}

	replicaMutex.Lock()
	defer replicaMutex.Unlock()

	// 随机选择一个从库
	idx := rand.Intn(len(replicas))
	return replicas[idx]
}

// GetWriteDB 获取写库实例（主库）
func GetWriteDB() *gorm.DB {
	return DB
}

// GetDB 获取数据库实例（默认主库，兼容旧代码）
func GetDB() *gorm.DB {
	return DB
}

// GetReplicas 获取所有从库实例
func GetReplicas() []*gorm.DB {
	return replicas
}

// UseMaster 强制使用主库（用于事务或需要实时数据的场景）
func UseMaster(ctx context.Context) context.Context {
	return context.WithValue(ctx, "db_mode", "master")
}

// UseReplica 强制使用从库（用于报表查询等场景）
func UseReplica(ctx context.Context) context.Context {
	return context.WithValue(ctx, "db_mode", "replica")
}

// GetDBFromContext 根据上下文选择数据库
func GetDBFromContext(ctx context.Context) *gorm.DB {
	mode, ok := ctx.Value("db_mode").(string)
	if !ok {
		return GetReadDB()
	}

	switch mode {
	case "master":
		return DB
	case "replica":
		return GetReadDB()
	default:
		return GetReadDB()
	}
}

// DBResolver 数据库解析器（用于 GORM 钩子）
type DBResolver struct{}

// BeforeQuery 查询前钩子，自动路由到从库
func (r *DBResolver) BeforeQuery(db *gorm.DB) {
	// 如果在事务中，使用主库
	if db.Statement.ConnPool != nil {
		return
	}

	// 检查上下文
	ctx := db.Statement.Context
	if ctx != nil {
		mode, ok := ctx.Value("db_mode").(string)
		if ok && mode == "master" {
			return // 强制主库
		}
	}

	// 读操作路由到从库
	if len(replicas) > 0 && DBRead != nil {
		db.Statement.ConnPool = DBRead.Statement.ConnPool
	}
}

// AutoMigrate 自动迁移数据库表结构（由应用重写）
func AutoMigrate() error {
	logger.Info("数据库表结构迁移完成")
	return nil
}

// Close 关闭数据库连接
func Close() error {
	if DB == nil {
		return nil
	}
	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// CloseAll 关闭所有数据库连接（包括从库）
func CloseAll() error {
	// 关闭主库
	if err := Close(); err != nil {
		return err
	}

	// 关闭从库
	for _, replica := range replicas {
		if replica == nil {
			continue
		}
		sqlDB, err := replica.DB()
		if err != nil {
			continue
		}
		sqlDB.Close()
	}

	replicas = nil
	DBRead = nil

	return nil
}

// Transaction 事务操作（自动使用主库）
func Transaction(fn func(tx *gorm.DB) error) error {
	return DB.Transaction(fn)
}

// TransactionWithContext 带上下文的事务操作
func TransactionWithContext(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return DB.WithContext(ctx).Transaction(fn)
}

// ReadQuery 读查询（自动路由到从库）
func ReadQuery(ctx context.Context, model any, query string, args ...any) error {
	db := GetDBFromContext(ctx)
	return db.WithContext(ctx).Where(query, args...).Find(model).Error
}

// WriteQuery 写查询（强制使用主库）
func WriteQuery(ctx context.Context, model any, query string, args ...any) error {
	return DB.WithContext(ctx).Where(query, args...).Find(model).Error
}

// HealthCheck 健康检查
func HealthCheck() map[string]bool {
	result := make(map[string]bool)

	// 检查主库
	if DB != nil {
		sqlDB, err := DB.DB()
		if err == nil && sqlDB.Ping() == nil {
			result["master"] = true
		} else {
			result["master"] = false
		}
	} else {
		result["master"] = false
	}

	// 检查从库
	for i, replica := range replicas {
		if replica != nil {
			sqlDB, err := replica.DB()
			if err == nil && sqlDB.Ping() == nil {
				result[fmt.Sprintf("replica_%d", i+1)] = true
			} else {
				result[fmt.Sprintf("replica_%d", i+1)] = false
			}
		} else {
			result[fmt.Sprintf("replica_%d", i+1)] = false
		}
	}

	return result
}
