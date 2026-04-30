package repository

import (
	"context"

	"gorm.io/gorm"
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

// BaseRepo 基础仓库实现
type BaseRepo[T any] struct {
	db *gorm.DB
}

// NewBaseRepo 创建基础仓库
func NewBaseRepo[T any](db *gorm.DB) *BaseRepo[T] {
	return &BaseRepo[T]{db: db}
}

// FindByID 根据 ID 查询
func (r *BaseRepo[T]) FindByID(ctx context.Context, id uint) (*T, error) {
	var model T
	err := r.db.WithContext(ctx).First(&model, id).Error
	if err != nil {
		return nil, err
	}
	return &model, nil
}

// Create 创建记录
func (r *BaseRepo[T]) Create(ctx context.Context, model *T) error {
	return r.db.WithContext(ctx).Create(model).Error
}

// Update 更新记录
func (r *BaseRepo[T]) Update(ctx context.Context, model *T) error {
	return r.db.WithContext(ctx).Save(model).Error
}

// Delete 删除记录（软删除）
func (r *BaseRepo[T]) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(new(T), id).Error
}

// HardDelete 硬删除记录（物理删除）
func (r *BaseRepo[T]) HardDelete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Unscoped().Delete(new(T), id).Error
}

// FindByIDs 批量查询
func (r *BaseRepo[T]) FindByIDs(ctx context.Context, ids []uint) ([]T, error) {
	var models []T
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&models).Error
	return models, err
}

// FindAll 查询所有记录
func (r *BaseRepo[T]) FindAll(ctx context.Context) ([]T, error) {
	var models []T
	err := r.db.WithContext(ctx).Find(&models).Error
	return models, err
}

// Count 统计数量
func (r *BaseRepo[T]) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(new(T)).Count(&count).Error
	return count, err
}

// CountWhere 条件统计
func (r *BaseRepo[T]) CountWhere(ctx context.Context, query string, args ...any) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(new(T)).Where(query, args...).Count(&count).Error
	return count, err
}

// GetDB 获取数据库实例
func (r *BaseRepo[T]) GetDB() *gorm.DB {
	return r.db
}

// ===== 扩展查询功能 =====

// FindOne 条件查询单条记录
// 评分: ⭐⭐⭐⭐⭐
// 理由: 灵活的条件查询，避免每次写原生 SQL
func (r *BaseRepo[T]) FindOne(ctx context.Context, query string, args ...any) (*T, error) {
	var model T
	err := r.db.WithContext(ctx).Where(query, args...).First(&model).Error
	if err != nil {
		return nil, err
	}
	return &model, nil
}

// FindWhere 条件查询多条记录
func (r *BaseRepo[T]) FindWhere(ctx context.Context, query string, args ...any) ([]T, error) {
	var models []T
	err := r.db.WithContext(ctx).Where(query, args...).Find(&models).Error
	return models, err
}

// FindWhereOrdered 条件查询并排序
func (r *BaseRepo[T]) FindWhereOrdered(ctx context.Context, query string, args []any, order string) ([]T, error) {
	var models []T
	err := r.db.WithContext(ctx).Where(query, args...).Order(order).Find(&models).Error
	return models, err
}

// FindOrdered 查询并排序
func (r *BaseRepo[T]) FindOrdered(ctx context.Context, order string, limit int) ([]T, error) {
	var models []T
	query := r.db.WithContext(ctx).Order(order)
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&models).Error
	return models, err
}

// FindLimited 查询指定数量记录
func (r *BaseRepo[T]) FindLimited(ctx context.Context, limit int) ([]T, error) {
	var models []T
	err := r.db.WithContext(ctx).Limit(limit).Find(&models).Error
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

// FindPage 分页查询
// 评分: ⭐⭐⭐⭐⭐
// 理由: 最常用的分页查询封装
func (r *BaseRepo[T]) FindPage(ctx context.Context, page, pageSize int) (*PageResult[T], error) {
	var models []T
	var total int64

	// 统计总数
	if err := r.db.WithContext(ctx).Model(new(T)).Count(&total).Error; err != nil {
		return nil, err
	}

	// 计算偏移量
	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}

	// 查询数据
	if err := r.db.WithContext(ctx).Offset(offset).Limit(pageSize).Find(&models).Error; err != nil {
		return nil, err
	}

	return &PageResult[T]{
		Items:    models,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// FindPageOrdered 分页查询并排序
func (r *BaseRepo[T]) FindPageOrdered(ctx context.Context, page, pageSize int, order string) (*PageResult[T], error) {
	var models []T
	var total int64

	if err := r.db.WithContext(ctx).Model(new(T)).Count(&total).Error; err != nil {
		return nil, err
	}

	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}

	if err := r.db.WithContext(ctx).Order(order).Offset(offset).Limit(pageSize).Find(&models).Error; err != nil {
		return nil, err
	}

	return &PageResult[T]{
		Items:    models,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// FindPageWhere 条件分页查询
func (r *BaseRepo[T]) FindPageWhere(ctx context.Context, page, pageSize int, query string, args ...any) (*PageResult[T], error) {
	var models []T
	var total int64

	if err := r.db.WithContext(ctx).Model(new(T)).Where(query, args...).Count(&total).Error; err != nil {
		return nil, err
	}

	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}

	if err := r.db.WithContext(ctx).Where(query, args...).Offset(offset).Limit(pageSize).Find(&models).Error; err != nil {
		return nil, err
	}

	return &PageResult[T]{
		Items:    models,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// FindPageWhereOrdered 条件分页查询并排序
func (r *BaseRepo[T]) FindPageWhereOrdered(ctx context.Context, page, pageSize int, query string, args []any, order string) (*PageResult[T], error) {
	var models []T
	var total int64

	if err := r.db.WithContext(ctx).Model(new(T)).Where(query, args...).Count(&total).Error; err != nil {
		return nil, err
	}

	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}

	if err := r.db.WithContext(ctx).Where(query, args...).Order(order).Offset(offset).Limit(pageSize).Find(&models).Error; err != nil {
		return nil, err
	}

	return &PageResult[T]{
		Items:    models,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// ===== 批量操作 =====

// CreateBatch 批量创建
func (r *BaseRepo[T]) CreateBatch(ctx context.Context, models []T) error {
	return r.db.WithContext(ctx).Create(models).Error
}

// UpdateBatch 批量更新（指定字段）
func (r *BaseRepo[T]) UpdateBatch(ctx context.Context, ids []uint, field string, value any) error {
	return r.db.WithContext(ctx).Model(new(T)).Where("id IN ?", ids).Update(field, value).Error
}

// DeleteBatch 批量删除
func (r *BaseRepo[T]) DeleteBatch(ctx context.Context, ids []uint) error {
	return r.db.WithContext(ctx).Delete(new(T), ids).Error
}

// HardDeleteBatch 批量硬删除
func (r *BaseRepo[T]) HardDeleteBatch(ctx context.Context, ids []uint) error {
	return r.db.WithContext(ctx).Unscoped().Delete(new(T), ids).Error
}

// ===== 存在性检查 =====

// Exists 检查是否存在
func (r *BaseRepo[T]) Exists(ctx context.Context, id uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(new(T)).Where("id = ?", id).Count(&count).Error
	return count > 0, err
}

// ExistsWhere 条件检查是否存在
func (r *BaseRepo[T]) ExistsWhere(ctx context.Context, query string, args ...any) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(new(T)).Where(query, args...).Limit(1).Count(&count).Error
	return count > 0, err
}

// ===== 软删除操作 =====

// Restore 恢复软删除记录
func (r *BaseRepo[T]) Restore(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Model(new(T)).Unscoped().Where("id = ?", id).Update("deleted_at", nil).Error
}

// RestoreBatch 批量恢复软删除记录
func (r *BaseRepo[T]) RestoreBatch(ctx context.Context, ids []uint) error {
	return r.db.WithContext(ctx).Model(new(T)).Unscoped().Where("id IN ?", ids).Update("deleted_at", nil).Error
}

// FindDeleted 查询已软删除的记录
func (r *BaseRepo[T]) FindDeleted(ctx context.Context) ([]T, error) {
	var models []T
	err := r.db.WithContext(ctx).Unscoped().Where("deleted_at IS NOT NULL").Find(&models).Error
	return models, err
}

// FindAllWithDeleted 查询所有记录（包括软删除）
func (r *BaseRepo[T]) FindAllWithDeleted(ctx context.Context) ([]T, error) {
	var models []T
	err := r.db.WithContext(ctx).Unscoped().Find(&models).Error
	return models, err
}

// ===== 事务支持 =====

// WithTransaction 在事务中执行操作
func (r *BaseRepo[T]) WithTransaction(ctx context.Context, fn func(txRepo *BaseRepo[T]) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txRepo := NewBaseRepo[T](tx)
		return fn(txRepo)
	})
}

// ===== QueryBuilder 链式查询 =====

// QueryBuilder 链式查询构建器
type QueryBuilder[T any] struct {
	db    *gorm.DB
	limit int
}

// NewQueryBuilder 创建查询构建器
func (r *BaseRepo[T]) NewQueryBuilder() *QueryBuilder[T] {
	return &QueryBuilder[T]{db: r.db.Model(new(T))}
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
	qb.db = qb.db.Order(order)
	return qb
}

// Limit 设置数量限制
func (qb *QueryBuilder[T]) Limit(limit int) *QueryBuilder[T] {
	qb.limit = limit
	qb.db = qb.db.Limit(limit)
	return qb
}

// Offset 设置偏移量
func (qb *QueryBuilder[T]) Offset(offset int) *QueryBuilder[T] {
	qb.db = qb.db.Offset(offset)
	return qb
}

// Find 执行查询
func (qb *QueryBuilder[T]) Find(ctx context.Context) ([]T, error) {
	var models []T
	err := qb.db.WithContext(ctx).Find(&models).Error
	return models, err
}

// First 执行查询并返回第一条
func (qb *QueryBuilder[T]) First(ctx context.Context) (*T, error) {
	var model T
	err := qb.db.WithContext(ctx).First(&model).Error
	if err != nil {
		return nil, err
	}
	return &model, nil
}

// Count 执行统计
func (qb *QueryBuilder[T]) Count(ctx context.Context) (int64, error) {
	var count int64
	err := qb.db.WithContext(ctx).Count(&count).Error
	return count, err
}

// Page 执行分页查询
func (qb *QueryBuilder[T]) Page(ctx context.Context, page, pageSize int) (*PageResult[T], error) {
	var models []T
	var total int64

	// 复制 query 用于统计（避免影响原查询）
	countDB := qb.db.Session(&gorm.Session{})
	if err := countDB.WithContext(ctx).Count(&total).Error; err != nil {
		return nil, err
	}

	offset := (page - 1) * pageSize
	if err := qb.db.WithContext(ctx).Offset(offset).Limit(pageSize).Find(&models).Error; err != nil {
		return nil, err
	}

	return &PageResult[T]{
		Items:    models,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}