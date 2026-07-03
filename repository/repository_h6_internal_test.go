package repository

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/database"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// h6User 测试模型，内嵌 gorm.Model 支持软删除。
type h6User struct {
	gorm.Model
	Name string
	Age  int
}

func init() {
	// 注册 SQLite 方言，供路由测试通过 database 包级 facade（InitDB 等，代理到 DefaultManager）初始化主从库使用。
	database.RegisterDialect(database.DialectSpec{
		Name:      "sqlite",
		Aliases:   []string{"sqlite3"},
		Dialector: func(dsn string) gorm.Dialector { return sqlite.Open(dsn) },
	})
}

// newH6SqliteDB 构造一个独立 sqlite 文件的 GORM 实例（不走 DefaultManager），
// 用于 fallback 路径（DefaultManager 未初始化）的行为闭环测试。
func newH6SqliteDB(t *testing.T) *gorm.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "test.db")), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&h6User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// namesFrom 提取 h6User 切片的 Name 集合，便于路由断言。
func namesFrom(users []h6User) map[string]struct{} {
	out := make(map[string]struct{}, len(users))
	for i := range users {
		out[users[i].Name] = struct{}{}
	}
	return out
}

// ===== H6c fallback 路径（DefaultManager 未初始化，readConn/writeConn 回退 r.db）=====

// TestH6CrudRoundTrip 验证 CRUD 闭环经 readConn/writeConn 回退 r.db 正常工作。
func TestH6CrudRoundTrip(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	ctx := context.Background()

	u := &h6User{Name: "alice", Age: 30}
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("ID not populated after Create")
	}

	got, err := repo.FindByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Name != "alice" || got.Age != 30 {
		t.Fatalf("unexpected user: %+v", got)
	}

	got.Age = 31
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got2, _ := repo.FindByID(ctx, u.ID)
	if got2.Age != 31 {
		t.Fatalf("Update not persisted, age=%d", got2.Age)
	}

	if err := repo.Delete(ctx, u.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.FindByID(ctx, u.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("after Delete want ErrRecordNotFound, got %v", err)
	}
}

// ===== H6a UpdateFields 局部更新 =====

// TestH6UpdateFieldsStructNonZeroOnly 传 struct 仅更新非零字段（零值 Age=0 不覆盖）。
func TestH6UpdateFieldsStructNonZeroOnly(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	ctx := context.Background()

	u := &h6User{Name: "bob", Age: 30}
	_ = repo.Create(ctx, u)

	// Name="carol"（非零），Age=0（零值，不应写入）
	if err := repo.UpdateFields(ctx, &h6User{Name: "carol"}, "id = ?", u.ID); err != nil {
		t.Fatalf("UpdateFields: %v", err)
	}
	got, _ := repo.FindByID(ctx, u.ID)
	if got.Name != "carol" {
		t.Fatalf("Name should be carol, got %q", got.Name)
	}
	if got.Age != 30 {
		t.Fatalf("Age should remain 30 (struct zero-value not written), got %d", got.Age)
	}
}

// TestH6UpdateFieldsMapExplicitZero 传 map 可显式置零。
func TestH6UpdateFieldsMapExplicitZero(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	ctx := context.Background()

	u := &h6User{Name: "dave", Age: 30}
	_ = repo.Create(ctx, u)

	if err := repo.UpdateFields(ctx, map[string]any{"age": 0}, "id = ?", u.ID); err != nil {
		t.Fatalf("UpdateFields map: %v", err)
	}
	got, _ := repo.FindByID(ctx, u.ID)
	if got.Age != 0 {
		t.Fatalf("Age should be 0 (map explicit zero), got %d", got.Age)
	}
}

// ===== H6c 事务 join：r.tx 字段（WithTransaction）=====

// TestH6WithTransactionRollback fn 返回错误 → 回滚，已写记录不持久化。
func TestH6WithTransactionRollback(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	ctx := context.Background()

	err := repo.WithTransaction(ctx, func(txRepo *BaseRepo[h6User]) error {
		if e := txRepo.Create(ctx, &h6User{Name: "rollback"}); e != nil {
			return e
		}
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("want error from WithTransaction")
	}

	all, _ := repo.FindAll(ctx)
	if _, ok := namesFrom(all)["rollback"]; ok {
		t.Fatal("rollback row should not persist after failed tx")
	}
}

// TestH6WithTransactionCommit fn 成功 → 提交，记录持久化。
func TestH6WithTransactionCommit(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	ctx := context.Background()

	err := repo.WithTransaction(ctx, func(txRepo *BaseRepo[h6User]) error {
		return txRepo.Create(ctx, &h6User{Name: "commit"})
	})
	if err != nil {
		t.Fatalf("WithTransaction: %v", err)
	}

	all, _ := repo.FindAll(ctx)
	if _, ok := namesFrom(all)["commit"]; !ok {
		t.Fatal("commit row should persist after successful tx")
	}
}

// TestH6WithTransactionTxRepoJoinsTx fn 内 txRepo 的写与读都参与同一事务。
// 关键：txRepo.Create 写入后，txRepo.FindOne 在事务内可见；若 txRepo 不 join tx
// （走 r.db 回退），FindOne 仍可见但写已 autocommit，回滚测试（上一用例）会失败。
func TestH6WithTransactionTxRepoJoinsTx(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	ctx := context.Background()

	var seenInTx bool
	err := repo.WithTransaction(ctx, func(txRepo *BaseRepo[h6User]) error {
		if e := txRepo.Create(ctx, &h6User{Name: "in-tx"}); e != nil {
			return e
		}
		got, e := txRepo.FindOne(ctx, "name = ?", "in-tx")
		if e != nil {
			return e
		}
		seenInTx = got != nil && got.Name == "in-tx"
		return nil
	})
	if err != nil {
		t.Fatalf("WithTransaction: %v", err)
	}
	if !seenInTx {
		t.Fatal("txRepo.FindOne should see row created within the same tx")
	}
}

// ===== H6c 事务 join：ctx 携带 tx（database.WithTx 跨层 join）=====

// TestH6WithTxCtxJoinsOuterTx 外层 gorm 事务通过 database.WithTx 注入 ctx，
// repo 方法必须 join 该事务；外层回滚则 repo 写入随之回滚。
// 红绿：修复前 repo 走 r.db（fallback）autocommit，回滚后行仍存在（红）；
// 修复后 repo 取 ctx 的 tx，随外层回滚（绿）。
func TestH6WithTxCtxJoinsOuterTx(t *testing.T) {
	db := newH6SqliteDB(t)
	repo := NewBaseRepo[h6User](db)
	ctx := context.Background()

	err := db.Transaction(func(tx *gorm.DB) error {
		ctx2 := database.WithTx(ctx, tx)
		if e := repo.Create(ctx2, &h6User{Name: "outer-tx"}); e != nil {
			return e
		}
		return errors.New("force rollback")
	})
	if err == nil {
		t.Fatal("want error from outer tx")
	}

	all, _ := repo.FindAll(ctx)
	if _, ok := namesFrom(all)["outer-tx"]; ok {
		t.Fatal("outer-tx row should be rolled back when repo joins outer tx")
	}
}

// TestH6WithTxCtxCommitsWhenOuterCommits 外层提交则 repo 写入持久化（正向闭环）。
func TestH6WithTxCtxCommitsWhenOuterCommits(t *testing.T) {
	db := newH6SqliteDB(t)
	repo := NewBaseRepo[h6User](db)
	ctx := context.Background()

	err := db.Transaction(func(tx *gorm.DB) error {
		ctx2 := database.WithTx(ctx, tx)
		return repo.Create(ctx2, &h6User{Name: "outer-commit"})
	})
	if err != nil {
		t.Fatalf("outer tx: %v", err)
	}

	all, _ := repo.FindAll(ctx)
	if _, ok := namesFrom(all)["outer-commit"]; !ok {
		t.Fatal("outer-commit row should persist when outer tx commits")
	}
}

// ===== H6d 分页 count+list 单事务 + 基本正确性 =====

// TestH6FindPage 验证分页 total 与 items 正确。
func TestH6FindPage(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = repo.Create(ctx, &h6User{Name: "u"})
	}

	p1, err := repo.FindPage(ctx, 1, 2)
	if err != nil {
		t.Fatalf("FindPage: %v", err)
	}
	if p1.Total != 5 || len(p1.Items) != 2 {
		t.Fatalf("page1: total=%d items=%d, want 5/2", p1.Total, len(p1.Items))
	}

	p3, _ := repo.FindPage(ctx, 3, 2)
	if len(p3.Items) != 1 {
		t.Fatalf("page3 items=%d, want 1", len(p3.Items))
	}
}

// TestH6FindPageWhere 验证条件分页。
func TestH6FindPageWhere(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_ = repo.Create(ctx, &h6User{Name: "match"})
	}
	_ = repo.Create(ctx, &h6User{Name: "other"})

	p, err := repo.FindPageWhere(ctx, 1, 10, "name = ?", "match")
	if err != nil {
		t.Fatalf("FindPageWhere: %v", err)
	}
	if p.Total != 3 || len(p.Items) != 3 {
		t.Fatalf("want total=3 items=3, got %d/%d", p.Total, len(p.Items))
	}
}

// ===== H6b 软删除契约 =====

// TestH6DeleteSoftDeletesWithGormModel T 内嵌 gorm.Model 时 Delete 为软删除：
// FindByID 不可见、FindDeleted 可见、Restore 恢复。
func TestH6DeleteSoftDeletesWithGormModel(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	ctx := context.Background()

	u := &h6User{Name: "soft"}
	_ = repo.Create(ctx, u)

	if err := repo.Delete(ctx, u.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.FindByID(ctx, u.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("soft-deleted should be invisible to FindByID, got %v", err)
	}
	deleted, _ := repo.FindDeleted(ctx)
	if _, ok := namesFrom(deleted)["soft"]; !ok {
		t.Fatal("FindDeleted should see soft-deleted row")
	}
	if err := repo.Restore(ctx, u.ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := repo.FindByID(ctx, u.ID); err != nil {
		t.Fatalf("after Restore FindByID should succeed, got %v", err)
	}
}

// ===== H6e QueryBuilder 终结方法克隆 + Count 剥离 Limit/Offset =====

// TestH6QueryBuilderCountStripsLimit Count 不受残留 Limit/Offset 截断。
// 红绿：修复前 Count 沿用 qb.db（带 Limit(2)），返回 2（红）；修复后剥离返回 5（绿）。
func TestH6QueryBuilderCountStripsLimit(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = repo.Create(ctx, &h6User{Name: "q"})
	}

	count, err := repo.NewQueryBuilder().Limit(2).Offset(1).Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 5 {
		t.Fatalf("Count should be 5 (stripped Limit/Offset), got %d", count)
	}
}

// TestH6QueryBuilderFindKeepsLimit Find 仍受 Limit 约束（回归保护）。
func TestH6QueryBuilderFindKeepsLimit(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = repo.Create(ctx, &h6User{Name: "q"})
	}

	got, err := repo.NewQueryBuilder().Limit(2).Find(ctx)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Find with Limit(2) should return 2, got %d", len(got))
	}
}

// TestH6QueryBuilderPageNoAccumulation 连续 Page 调用不因克隆失效而累积污染。
func TestH6QueryBuilderPageNoAccumulation(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		_ = repo.Create(ctx, &h6User{Name: "q"})
	}

	qb := repo.NewQueryBuilder().Where("name = ?", "q")
	p1, err := qb.Page(ctx, 1, 2)
	if err != nil {
		t.Fatalf("Page1: %v", err)
	}
	p2, err := qb.Page(ctx, 2, 2)
	if err != nil {
		t.Fatalf("Page2: %v", err)
	}
	if p1.Total != 4 || len(p1.Items) != 2 {
		t.Fatalf("page1: total=%d items=%d, want 4/2", p1.Total, len(p1.Items))
	}
	if p2.Total != 4 || len(p2.Items) != 2 {
		t.Fatalf("page2: total=%d items=%d, want 4/2", p2.Total, len(p2.Items))
	}
}

// ===== H6c 路由：DefaultManager 主从读写分离（headline）=====

// setupH6Manager 初始化 DefaultManager 为 sqlite 主+从（独立文件），
// 并在两侧建表。返回 cleanup 还原全局状态（CloseAll + 置 nil-master）。
func setupH6Manager(t *testing.T) (masterFile, replicaFile string) {
	t.Helper()
	dir := t.TempDir()
	masterFile = filepath.Join(dir, "master.db")
	replicaFile = filepath.Join(dir, "replica.db")

	cfg := &config.Config{}
	cfg.Database.Driver = "sqlite"
	cfg.Database.CustomDSN = masterFile

	if err := database.InitDBWithReplicas(cfg, []string{replicaFile}); err != nil {
		t.Fatalf("InitDBWithReplicas: %v", err)
	}
	if err := database.GetWriteDB().AutoMigrate(&h6User{}); err != nil {
		t.Fatalf("migrate master: %v", err)
	}
	if err := database.GetReadDB().AutoMigrate(&h6User{}); err != nil {
		t.Fatalf("migrate replica: %v", err)
	}
	t.Cleanup(func() {
		_ = database.CloseAll()
	})
	return masterFile, replicaFile
}

// TestH6RoutingReadWriteSplit 验证读操作路由到从库、写操作路由到主库（H6c 核心）。
// 红绿：修复前 BaseRepo 全部走 r.db（构造捕获的主库），默认读会看到主库标记（红）；
// 修复后默认读路由到从库，仅 UseMaster 读主库，写永远落主库（绿）。
func TestH6RoutingReadWriteSplit(t *testing.T) {
	setupH6Manager(t)
	ctx := context.Background()

	// 主从分别写入不同标记
	_ = database.GetWriteDB().Create(&h6User{Name: "MASTER"})
	_ = database.GetReadDB().Create(&h6User{Name: "REPLICA"})

	// 下游典型用法：r.db = 主库（database.GetDB()）
	repo := NewBaseRepo[h6User](database.GetDB())

	// 默认 ctx（无 UseMaster/UseReplica）→ GetDBFromContext → 从库 → 仅 REPLICA
	all, err := repo.FindAll(ctx)
	if err != nil {
		t.Fatalf("FindAll default: %v", err)
	}
	names := namesFrom(all)
	if _, ok := names["REPLICA"]; !ok {
		t.Errorf("default read should route to replica and see REPLICA, got %v", names)
	}
	if _, ok := names["MASTER"]; ok {
		t.Errorf("default read should NOT see master-only MASTER, got %v", names)
	}

	// UseMaster → 主库 → MASTER
	allM, _ := repo.FindAll(database.UseMaster(ctx))
	namesM := namesFrom(allM)
	if _, ok := namesM["MASTER"]; !ok {
		t.Errorf("UseMaster read should see MASTER, got %v", namesM)
	}

	// UseReplica → 从库 → REPLICA
	allR, _ := repo.FindAll(database.UseReplica(ctx))
	namesR := namesFrom(allR)
	if _, ok := namesR["REPLICA"]; !ok {
		t.Errorf("UseReplica read should see REPLICA, got %v", namesR)
	}

	// 写操作 → 主库：Create 后 UseMaster 可见，默认（从库）不可见
	if err := repo.Create(ctx, &h6User{Name: "WRITE"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	afterWriteM, _ := repo.FindAll(database.UseMaster(ctx))
	if _, ok := namesFrom(afterWriteM)["WRITE"]; !ok {
		t.Errorf("write should land on master (visible via UseMaster), got %v", namesFrom(afterWriteM))
	}
	afterWriteR, _ := repo.FindAll(ctx) // 默认从库
	if _, ok := namesFrom(afterWriteR)["WRITE"]; ok {
		t.Errorf("write should NOT appear on replica, got %v", namesFrom(afterWriteR))
	}
}

// TestH6RoutingWriteConnNeverHitsReplica 写操作即便 UseReplica(ctx) 也走主库。
func TestH6RoutingWriteConnNeverHitsReplica(t *testing.T) {
	setupH6Manager(t)
	ctx := context.Background()

	repo := NewBaseRepo[h6User](database.GetDB())

	// 即便 ctx 标记 UseReplica，写也必须落主库
	if err := repo.Create(database.UseReplica(ctx), &h6User{Name: "W2"}); err != nil {
		t.Fatalf("Create under UseReplica: %v", err)
	}
	onMaster, _ := repo.FindAll(database.UseMaster(ctx))
	if _, ok := namesFrom(onMaster)["W2"]; !ok {
		t.Errorf("write under UseReplica must still land on master, got %v", namesFrom(onMaster))
	}
}
