package repository

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/EthanCodeCraft/xlgo-core/database"
	"gorm.io/gorm"
)

const (
	// DefaultFindAllLimit caps FindAll to avoid accidental full-table scans.
	DefaultFindAllLimit = 1000
	// DefaultPageSize is used when pageSize<=0.
	DefaultPageSize = 20
	// MaxPageSize caps page queries to avoid Limit(0)/huge-limit footguns.
	MaxPageSize = 100
	// MaxPage caps deep-offset queries; deeper traversal should use keyset pagination.
	MaxPage = 10000
)

var (
	ErrUnsafeOrder = errors.New("repository: unsafe order clause")
	ErrUnsafeField = errors.New("repository: unsafe field name")
)

// BaseRepository 基础仓库接口
type BaseRepository[T any] interface {
	// FindByID 根据 ID 查询
	FindByID(ctx context.Context, id uint) (*T, error)
	// Create 创建记录
	Create(ctx context.Context, model *T) error
	// Update 更新记录
	Update(ctx context.Context, model *T) error
	// Delete 删除记录（软删除）
	Delete(ctx context.Context, id uint) error
	// FindByIDs 批量查询
	FindByIDs(ctx context.Context, ids []uint) ([]T, error)
}

// BaseRepo 基础仓库实现。
//
// 连接路由契约（H6c 修复）：
//   - 读操作经 readConn(ctx) 路由：优先 join 外层事务，否则走 database.GetDBFromContext
//     （默认从库，支持 UseMaster/UseReplica 读写分离），DefaultManager 未初始化时回退 r.db。
//   - 写操作经 writeConn(ctx) 路由：优先 join 外层事务，否则走主库 database.GetWriteDB()，
//     回退 r.db。写操作不路由到从库（从库只读）。
//   - 事务内（WithTransaction 创建的 txRepo）所有方法 join 同一事务。
//
// 下游典型用法 NewBaseRepo[T](database.GetDB()) 仍兼容：GetDB 返回主库，DefaultManager
// 初始化后读操作自动路由到从库；未初始化（如单测注入 sqlite）时回退到 r.db。
type BaseRepo[T any] struct {
	db *gorm.DB
	// tx 在事务内（WithTransaction）非 nil，使 txRepo 的方法 join 该事务而非另开连接/路由。
	tx *gorm.DB
}

// NewBaseRepo 创建基础仓库
func NewBaseRepo[T any](db *gorm.DB) *BaseRepo[T] {
	return &BaseRepo[T]{db: db}
}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (r *BaseRepo[T]) requireDB() *gorm.DB {
	if r.db == nil {
		panic("repository.BaseRepo: db is nil. Use NewBaseRepo with a valid *gorm.DB")
	}
	return r.db
}

// readConn 返回读连接：先校验 repo 构造时注入的 r.db 非 nil，再按
// 外层 ctx 事务 > 本 repo 事务 > 读写分离路由 > r.db 回退。
// RP1 修复：r.db 为 nil 时 panic 并给出明确消息，即使 ctx 携带外层事务也要求 repo 构造合法。
func (r *BaseRepo[T]) readConn(ctx context.Context) *gorm.DB {
	ctx = normalizeContext(ctx)
	base := r.requireDB()
	if tx := database.TxFromContext(ctx); tx != nil {
		return tx.WithContext(ctx)
	}
	if r.tx != nil {
		return r.tx.WithContext(ctx)
	}
	if gdb := database.GetDBFromContext(ctx); gdb != nil {
		return gdb.WithContext(ctx)
	}
	return base.WithContext(ctx)
}

// writeConn 返回写连接：先校验 repo 构造时注入的 r.db 非 nil，再按
// 外层 ctx 事务 > 本 repo 事务 > 主库 > r.db 回退。
// 写操作始终走主库，不路由到只读从库。
// RP1 修复：r.db 为 nil 时 panic 并给出明确消息，即使 ctx 携带外层事务也要求 repo 构造合法。
func (r *BaseRepo[T]) writeConn(ctx context.Context) *gorm.DB {
	ctx = normalizeContext(ctx)
	base := r.requireDB()
	if tx := database.TxFromContext(ctx); tx != nil {
		return tx.WithContext(ctx)
	}
	if r.tx != nil {
		return r.tx.WithContext(ctx)
	}
	if mdb := database.GetWriteDB(); mdb != nil {
		return mdb.WithContext(ctx)
	}
	return base.WithContext(ctx)
}

// FindByID 根据 ID 查询
func (r *BaseRepo[T]) FindByID(ctx context.Context, id uint) (*T, error) {
	var model T
	err := r.readConn(ctx).First(&model, id).Error
	if err != nil {
		return nil, err
	}
	return &model, nil
}

// Create 创建记录
func (r *BaseRepo[T]) Create(ctx context.Context, model *T) error {
	return r.writeConn(ctx).Create(model).Error
}

// Update 更新记录（全列覆写，基于 Save）。
//
// 注意（H6a）：Save 会写入所有字段（包括零值），无法区分"未设置"与"清零"，
// 且可能用零值覆盖并发更新。需要局部更新（仅更新非零字段或指定字段）请用 UpdateFields。
func (r *BaseRepo[T]) Update(ctx context.Context, model *T) error {
	return r.writeConn(ctx).Save(model).Error
}

// UpdateFields 局部更新（H6a）：基于 gorm.Updates，仅更新非零字段（struct）或指定字段（map）。
//
//   - 传 struct：仅更新非零字段（零值被忽略，避免覆盖）。
//   - 传 map[string]any：更新指定字段（可显式置零）。
//
// 示例：
//
//	repo.UpdateFields(ctx, &User{Name: "new"}, "name")        // 仅更新 name
//	repo.UpdateFields(ctx, map[string]any{"status": 0}, "id = ?", id) // 显式置零
func (r *BaseRepo[T]) UpdateFields(ctx context.Context, model any, conds ...any) error {
	db := r.writeConn(ctx).Model(new(T))
	if len(conds) > 0 {
		db = db.Where(conds[0], conds[1:]...)
	}
	return db.Updates(model).Error
}

// Delete 删除记录。
//
// 行为契约（H6b）：若 T 内嵌 gorm.DeletedAt（或 gorm.Model），为软删除；
// 否则为硬删除（泛型类型约束无法在编译期强制）。需硬删用 HardDelete，需恢复用 Restore。
func (r *BaseRepo[T]) Delete(ctx context.Context, id uint) error {
	return r.writeConn(ctx).Delete(new(T), id).Error
}

// HardDelete 硬删除记录（物理删除）
func (r *BaseRepo[T]) HardDelete(ctx context.Context, id uint) error {
	return r.writeConn(ctx).Unscoped().Delete(new(T), id).Error
}

// FindByIDs 批量查询
func (r *BaseRepo[T]) FindByIDs(ctx context.Context, ids []uint) ([]T, error) {
	if len(ids) == 0 {
		return []T{}, nil
	}
	var models []T
	err := r.readConn(ctx).Where("id IN ?", ids).Find(&models).Error
	return models, err
}

// FindAll 查询记录，默认最多返回 DefaultFindAllLimit 条，避免误用导致全表扫描。
// 需要明确全量扫描时使用 FindAllUnbounded。
func (r *BaseRepo[T]) FindAll(ctx context.Context) ([]T, error) {
	return r.FindLimited(ctx, DefaultFindAllLimit)
}

// FindAllUnbounded 查询所有记录（无默认 limit）。
// 仅用于明确需要全表扫描的后台任务/小表场景；在线请求优先使用分页或 FindAll。
func (r *BaseRepo[T]) FindAllUnbounded(ctx context.Context) ([]T, error) {
	var models []T
	err := r.readConn(ctx).Find(&models).Error
	return models, err
}

// Count 统计数量
func (r *BaseRepo[T]) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.readConn(ctx).Model(new(T)).Count(&count).Error
	return count, err
}

// CountWhere 条件统计
func (r *BaseRepo[T]) CountWhere(ctx context.Context, query string, args ...any) (int64, error) {
	var count int64
	err := r.readConn(ctx).Model(new(T)).Where(query, args...).Count(&count).Error
	return count, err
}

// GetDB 获取数据库实例。
// 事务内（txRepo）返回当前事务的 tx；否则返回构造时注入的 db。
// 注意：此方法无 ctx 参数，不参与读写分离路由；需要路由请用具体方法（FindByID/FindPage 等）。
func (r *BaseRepo[T]) GetDB() *gorm.DB {
	if r.tx != nil {
		return r.tx
	}
	return r.db
}

// ===== 扩展查询功能 =====

// FindOne 条件查询单条记录
func (r *BaseRepo[T]) FindOne(ctx context.Context, query string, args ...any) (*T, error) {
	var model T
	err := r.readConn(ctx).Where(query, args...).First(&model).Error
	if err != nil {
		return nil, err
	}
	return &model, nil
}

// FindWhere 条件查询多条记录
func (r *BaseRepo[T]) FindWhere(ctx context.Context, query string, args ...any) ([]T, error) {
	var models []T
	err := r.readConn(ctx).Where(query, args...).Find(&models).Error
	return models, err
}

// FindWhereOrdered 条件查询并排序。
//
// H-15 修复（breaking）：签名由 (ctx, query, args []any, order) 改为
// (ctx, order, query string, args ...any)，与 FindWhere 等同类方法统一为变长 args。
// Go 语法要求变长参数必须为最后一个，故 order 前置于 query。
func (r *BaseRepo[T]) FindWhereOrdered(ctx context.Context, order, query string, args ...any) ([]T, error) {
	if err := validateOrderClause(order); err != nil {
		return nil, err
	}
	var models []T
	err := r.readConn(ctx).Where(query, args...).Order(order).Find(&models).Error
	return models, err
}

// FindOrdered 查询并排序
func (r *BaseRepo[T]) FindOrdered(ctx context.Context, order string, limit int) ([]T, error) {
	if err := validateOrderClause(order); err != nil {
		return nil, err
	}
	var models []T
	query := r.readConn(ctx).Order(order)
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&models).Error
	return models, err
}

// FindLimited 查询指定数量记录
func (r *BaseRepo[T]) FindLimited(ctx context.Context, limit int) ([]T, error) {
	if limit <= 0 {
		return []T{}, nil
	}
	var models []T
	err := r.readConn(ctx).Limit(limit).Find(&models).Error
	return models, err
}

// ===== 分页查询 =====

// PageResult 分页结果
type PageResult[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

func normalizePage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if page > MaxPage {
		page = MaxPage
	}
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}
	return page, pageSize
}

// pageOffset 计算 (page-1)*pageSize，负值归零。
func pageOffset(page, pageSize int) int {
	page, pageSize = normalizePage(page, pageSize)
	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}
	return offset
}

// FindPage 分页查询。
//
// count 与 list 包进单事务（H6d），保证 total 与 items 在同一快照下一致，
// 避免高并发下两条独立语句间数据变动致 total/items 不一致。
//
// 若 ctx 已携带外层事务（database.WithTx），readConn 返回该 tx，此处 .Transaction
// 在其上开 savepoint（gorm 行为），count+list 仍同快照一致；无外层事务则开独立读事务。
func (r *BaseRepo[T]) FindPage(ctx context.Context, page, pageSize int) (*PageResult[T], error) {
	var models []T
	var total int64
	page, pageSize = normalizePage(page, pageSize)
	offset := pageOffset(page, pageSize)
	err := r.readConn(ctx).Transaction(func(tx *gorm.DB) error {
		if e := tx.Model(new(T)).Count(&total).Error; e != nil {
			return e
		}
		return tx.Offset(offset).Limit(pageSize).Find(&models).Error
	})
	if err != nil {
		return nil, err
	}
	return &PageResult[T]{Items: models, Total: total, Page: page, PageSize: pageSize}, nil
}

// FindPageOrdered 分页查询并排序（count+list 单事务，H6d）
func (r *BaseRepo[T]) FindPageOrdered(ctx context.Context, page, pageSize int, order string) (*PageResult[T], error) {
	if err := validateOrderClause(order); err != nil {
		return nil, err
	}
	var models []T
	var total int64
	page, pageSize = normalizePage(page, pageSize)
	offset := pageOffset(page, pageSize)
	err := r.readConn(ctx).Transaction(func(tx *gorm.DB) error {
		if e := tx.Model(new(T)).Count(&total).Error; e != nil {
			return e
		}
		return tx.Order(order).Offset(offset).Limit(pageSize).Find(&models).Error
	})
	if err != nil {
		return nil, err
	}
	return &PageResult[T]{Items: models, Total: total, Page: page, PageSize: pageSize}, nil
}

// FindPageWhere 条件分页查询（count+list 单事务，H6d）
func (r *BaseRepo[T]) FindPageWhere(ctx context.Context, page, pageSize int, query string, args ...any) (*PageResult[T], error) {
	var models []T
	var total int64
	page, pageSize = normalizePage(page, pageSize)
	offset := pageOffset(page, pageSize)
	err := r.readConn(ctx).Transaction(func(tx *gorm.DB) error {
		if e := tx.Model(new(T)).Where(query, args...).Count(&total).Error; e != nil {
			return e
		}
		return tx.Where(query, args...).Offset(offset).Limit(pageSize).Find(&models).Error
	})
	if err != nil {
		return nil, err
	}
	return &PageResult[T]{Items: models, Total: total, Page: page, PageSize: pageSize}, nil
}

// FindPageWhereOrdered 条件分页查询并排序（count+list 单事务，H6d）。
//
// H-15 修复（breaking）：签名由 (ctx, page, pageSize, query, args []any, order) 改为
// (ctx, page, pageSize, order, query string, args ...any)，与 FindPageWhere 统一为变长 args。
// Go 语法要求变长参数必须为最后一个，故 order 前置于 query。
func (r *BaseRepo[T]) FindPageWhereOrdered(ctx context.Context, page, pageSize int, order, query string, args ...any) (*PageResult[T], error) {
	if err := validateOrderClause(order); err != nil {
		return nil, err
	}
	var models []T
	var total int64
	page, pageSize = normalizePage(page, pageSize)
	offset := pageOffset(page, pageSize)
	err := r.readConn(ctx).Transaction(func(tx *gorm.DB) error {
		if e := tx.Model(new(T)).Where(query, args...).Count(&total).Error; e != nil {
			return e
		}
		return tx.Where(query, args...).Order(order).Offset(offset).Limit(pageSize).Find(&models).Error
	})
	if err != nil {
		return nil, err
	}
	return &PageResult[T]{Items: models, Total: total, Page: page, PageSize: pageSize}, nil
}

// ===== 批量操作 =====

// CreateBatch 批量创建
func (r *BaseRepo[T]) CreateBatch(ctx context.Context, models []T) error {
	return r.writeConn(ctx).Create(models).Error
}

// UpdateBatch 批量更新（指定字段）
func (r *BaseRepo[T]) UpdateBatch(ctx context.Context, ids []uint, field string, value any) error {
	if len(ids) == 0 {
		return nil
	}
	if err := validateIdentifier(field); err != nil {
		return err
	}
	return r.writeConn(ctx).Model(new(T)).Where("id IN ?", ids).Update(field, value).Error
}

// DeleteBatch 批量删除
func (r *BaseRepo[T]) DeleteBatch(ctx context.Context, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return r.writeConn(ctx).Delete(new(T), ids).Error
}

// HardDeleteBatch 批量硬删除
func (r *BaseRepo[T]) HardDeleteBatch(ctx context.Context, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return r.writeConn(ctx).Unscoped().Delete(new(T), ids).Error
}

// ===== 存在性检查 =====

// Exists 检查是否存在
func (r *BaseRepo[T]) Exists(ctx context.Context, id uint) (bool, error) {
	var count int64
	err := r.readConn(ctx).Model(new(T)).Where("id = ?", id).Count(&count).Error
	return count > 0, err
}

// ExistsWhere 条件检查是否存在
func (r *BaseRepo[T]) ExistsWhere(ctx context.Context, query string, args ...any) (bool, error) {
	var count int64
	err := r.readConn(ctx).Model(new(T)).Where(query, args...).Limit(1).Count(&count).Error
	return count > 0, err
}

// ===== 软删除操作 =====

// SoftDeleteColumn 返回软删除列名（RP2 修复：可被子类覆盖以适配自定义列名）。
// 默认 "deleted_at"，与 GORM gorm.DeletedAt 的默认列名一致。
func (r *BaseRepo[T]) SoftDeleteColumn() string {
	return "deleted_at"
}

// Restore 恢复软删除记录
func (r *BaseRepo[T]) Restore(ctx context.Context, id uint) error {
	if err := validateIdentifier(r.SoftDeleteColumn()); err != nil {
		return err
	}
	return r.writeConn(ctx).Model(new(T)).Unscoped().Where("id = ?", id).Update(r.SoftDeleteColumn(), nil).Error
}

// RestoreBatch 批量恢复软删除记录
func (r *BaseRepo[T]) RestoreBatch(ctx context.Context, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	if err := validateIdentifier(r.SoftDeleteColumn()); err != nil {
		return err
	}
	return r.writeConn(ctx).Model(new(T)).Unscoped().Where("id IN ?", ids).Update(r.SoftDeleteColumn(), nil).Error
}

// FindDeleted 查询已软删除的记录
func (r *BaseRepo[T]) FindDeleted(ctx context.Context) ([]T, error) {
	if err := validateIdentifier(r.SoftDeleteColumn()); err != nil {
		return nil, err
	}
	var models []T
	err := r.readConn(ctx).Unscoped().Where(r.SoftDeleteColumn() + " IS NOT NULL").Find(&models).Error
	return models, err
}

// FindAllWithDeleted 查询所有记录（包括软删除）
func (r *BaseRepo[T]) FindAllWithDeleted(ctx context.Context) ([]T, error) {
	var models []T
	err := r.readConn(ctx).Unscoped().Find(&models).Error
	return models, err
}

// ===== 事务支持 =====

// WithTransaction 在事务中执行操作。
//
// fn 收到的 txRepo 处于事务内，其所有方法（FindByID/Create/Update/...）自动 join 该事务，
// 不再路由到主从库（H6c）。事务在主库上开启。
//
// 跨 repo / 跨层 join：若 fn 内需调用其它 repo 或 database 层方法参与同一事务，
// 用 database.WithTx(ctx, tx) 把事务注入 ctx 后传递：
//
//	return repo.WithTransaction(ctx, func(txRepo *BaseRepo[T]) error {
//	    if err := txRepo.Create(ctx, a); err != nil { return err }
//	    // 另一个 repo 也加入同一事务：
//	    ctx2 := database.WithTx(ctx, txRepo.GetDB())
//	    return otherRepo.Update(ctx2, b)
//	})
//
// 注：fn 内捕获的 ctx 不会自动携带 tx；仅 txRepo 的方法 join 事务。
func (r *BaseRepo[T]) WithTransaction(ctx context.Context, fn func(txRepo *BaseRepo[T]) error) error {
	return r.writeConn(ctx).Transaction(func(tx *gorm.DB) error {
		txRepo := &BaseRepo[T]{db: r.db, tx: tx}
		return fn(txRepo)
	})
}

// ===== QueryBuilder 链式查询 =====
//
// QueryBuilder 为单次使用、非并发安全（H6e）：链式方法 Where/Or/Order/Limit/Offset 会
// mutate qb.db，禁止跨 goroutine 共用同一实例或多次终结调用复用累积条件。
// 终结方法（Find/First/Count/Page）基于 Session 克隆，不污染 qb.db；Count 额外剥离
// Limit/Offset 避免残留分页条件截断统计。
//
// 路由：QueryBuilder 查询经构造时注入的 db（通常主库），不参与读写分离路由；
// 需读写分离请用具体方法（FindPage/FindWhere 等）。

// QueryBuilder 链式查询构建器
type QueryBuilder[T any] struct {
	db    *gorm.DB
	limit int
	err   error
}

// NewQueryBuilder 创建查询构建器
func (r *BaseRepo[T]) NewQueryBuilder() *QueryBuilder[T] {
	return &QueryBuilder[T]{db: r.requireDB().Model(new(T))}
}

// Where 添加条件
func (qb *QueryBuilder[T]) Where(query string, args ...any) *QueryBuilder[T] {
	qb.db = qb.db.Where(query, args...)
	return qb
}

// Or 添加 OR 条件
func (qb *QueryBuilder[T]) Or(query string, args ...any) *QueryBuilder[T] {
	qb.db = qb.db.Or(query, args...)
	return qb
}

// Order 设置排序
func (qb *QueryBuilder[T]) Order(order string) *QueryBuilder[T] {
	if qb.err != nil {
		return qb
	}
	if err := validateOrderClause(order); err != nil {
		qb.err = err
		return qb
	}
	qb.db = qb.db.Order(order)
	return qb
}

// Limit 设置数量限制
func (qb *QueryBuilder[T]) Limit(limit int) *QueryBuilder[T] {
	if limit < 0 {
		limit = 0
	}
	qb.limit = limit
	qb.db = qb.db.Limit(limit)
	return qb
}

// Offset 设置偏移量
func (qb *QueryBuilder[T]) Offset(offset int) *QueryBuilder[T] {
	qb.db = qb.db.Offset(offset)
	return qb
}

// clone 返回 qb.db 的 Session 克隆，确保终结方法不污染 qb.db（H6e）。
func (qb *QueryBuilder[T]) clone() *gorm.DB {
	return qb.db.Session(&gorm.Session{})
}

// Find 执行查询
func (qb *QueryBuilder[T]) Find(ctx context.Context) ([]T, error) {
	if qb.err != nil {
		return nil, qb.err
	}
	ctx = normalizeContext(ctx)
	var models []T
	err := qb.clone().WithContext(ctx).Find(&models).Error
	return models, err
}

// First 执行查询并返回第一条
func (qb *QueryBuilder[T]) First(ctx context.Context) (*T, error) {
	if qb.err != nil {
		return nil, qb.err
	}
	ctx = normalizeContext(ctx)
	var model T
	err := qb.clone().WithContext(ctx).First(&model).Error
	if err != nil {
		return nil, err
	}
	return &model, nil
}

// Count 执行统计。
// 克隆并剥离 Limit/Offset（H6e），避免先前 Limit()/Offset() 残留截断统计行数。
func (qb *QueryBuilder[T]) Count(ctx context.Context) (int64, error) {
	if qb.err != nil {
		return 0, qb.err
	}
	ctx = normalizeContext(ctx)
	var count int64
	err := qb.clone().Limit(-1).Offset(-1).WithContext(ctx).Count(&count).Error
	return count, err
}

// Page 执行分页查询。
// count 与 list 基于同一 Session 克隆，count 剥离 Limit/Offset（H6e）。
// 注意：与 FindPage 不同，Page 的 count+list 不包单事务（QueryBuilder 为轻量构建器）；
// 需要 total/items 快照一致请用 BaseRepo.FindPage。
func (qb *QueryBuilder[T]) Page(ctx context.Context, page, pageSize int) (*PageResult[T], error) {
	if qb.err != nil {
		return nil, qb.err
	}
	ctx = normalizeContext(ctx)
	var models []T
	var total int64
	page, pageSize = normalizePage(page, pageSize)

	// count 克隆并剥离 Limit/Offset
	countDB := qb.clone().Limit(-1).Offset(-1)
	if err := countDB.WithContext(ctx).Count(&total).Error; err != nil {
		return nil, err
	}

	offset := pageOffset(page, pageSize)
	if err := qb.clone().WithContext(ctx).Offset(offset).Limit(pageSize).Find(&models).Error; err != nil {
		return nil, err
	}

	return &PageResult[T]{
		Items:    models,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func validateIdentifier(field string) error {
	if !isIdentifierPath(field) {
		return ErrUnsafeField
	}
	return nil
}

func isIdentifierPath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	parts := strings.Split(s, ".")
	for _, part := range parts {
		if !isIdentifier(part) {
			return false
		}
	}
	return true
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func validateOrderClause(order string) error {
	order = strings.TrimSpace(order)
	if order == "" {
		return ErrUnsafeOrder
	}
	for _, raw := range strings.Split(order, ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			return ErrUnsafeOrder
		}
		fields := strings.Fields(item)
		if len(fields) == 0 || len(fields) > 2 {
			return ErrUnsafeOrder
		}
		if !isIdentifierPath(fields[0]) {
			return ErrUnsafeOrder
		}
		if len(fields) == 2 {
			dir := strings.ToUpper(fields[1])
			if dir != "ASC" && dir != "DESC" {
				return ErrUnsafeOrder
			}
		}
	}
	return nil
}
