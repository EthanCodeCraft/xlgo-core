package model

import (
	"time"

	"gorm.io/gorm"
)

// BaseModel 基础模型。CreatedAt/UpdatedAt 不显式指定 type，由 GORM 按驱动选默认
// 时间列类型（MySQL 通常为 datetime(0) 或 timestamp，保留亚秒精度）。
type BaseModel struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// BaseModelWithTime 显式指定时间列为 datetime 类型。
//
// 命名注意（N1）：本类型与 BaseModel 的唯一区别是 CreatedAt/UpdatedAt 带 `gorm:"type:datetime"`，
// 二者都有时间戳——名字里的 "WithTime" 易误导。datetime 类型在部分 MySQL 版本下精度为秒
// （丢毫秒），需毫秒精度请用 BaseModel 或显式 `type:datetime(3)`。保留此类型仅为向后兼容。
type BaseModelWithTime struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time      `gorm:"type:datetime" json:"created_at"`
	UpdatedAt time.Time      `gorm:"type:datetime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}
