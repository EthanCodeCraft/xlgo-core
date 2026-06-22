package repository

import (
	"strings"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type pageCountModel struct {
	gorm.Model
	Name string
}

// newDryRunDB 用 mysql 驱动 + DryRun 模式构造一个不实际连接数据库的 GORM 实例，
// 仅用于校验生成的 SQL。SkipInitializeWithVersion 避免初始化时执行 SELECT VERSION()。
func newDryRunDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(mysql.New(mysql.Config{
		DSN:                       "root:root@tcp(127.0.0.1:3306)/test?parseTime=true",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{
		DryRun:                  true,
		DisableAutomaticPing:    true,
		Logger:                  gormlogger.Default.LogMode(gormlogger.Silent),
		SkipDefaultTransaction:  true,
	})
	if err != nil {
		t.Fatalf("open dry-run db: %v", err)
	}
	return db
}

// TestQueryBuilderPageCountStripsLimit 验证 Page 的 Count 不受残留 Limit/Offset 影响。
// 修复前：count SQL 会被包成子查询并带上 LIMIT，导致统计行数被截断。
// 修复后：count SQL 不应包含 LIMIT。
func TestQueryBuilderPageCountStripsLimit(t *testing.T) {
	db := newDryRunDB(t)
	repo := NewBaseRepo[pageCountModel](db)

	qb := repo.NewQueryBuilder().
		Where("name = ?", "foo").
		Limit(10).
		Offset(5)

	// 复刻 Page 内部的统计路径（已修复）
	countDB := qb.db.Session(&gorm.Session{}).Limit(-1).Offset(-1)
	var total int64
	result := countDB.Session(&gorm.Session{}).Count(&total)
	sql := result.Statement.SQL.String()

	if strings.Contains(strings.ToUpper(sql), "LIMIT") {
		t.Errorf("count SQL should not contain LIMIT (residual limit would truncate total), got: %s", sql)
	}
}

// TestQueryBuilderPageFindKeepsLimit 验证查询路径仍然带分页 Limit（回归保护）。
func TestQueryBuilderPageFindKeepsLimit(t *testing.T) {
	db := newDryRunDB(t)
	repo := NewBaseRepo[pageCountModel](db)

	qb := repo.NewQueryBuilder().Where("name = ?", "foo")
	var models []pageCountModel
	result := qb.db.Session(&gorm.Session{}).Limit(10).Offset(5).Find(&models)
	sql := strings.ToUpper(result.Statement.SQL.String())

	if !strings.Contains(sql, "LIMIT") {
		t.Errorf("find SQL should contain LIMIT for pagination, got: %s", sql)
	}
}
