package repository_test

import (
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/repository"
	"gorm.io/gorm"
)

// N2：原 repository_test.go 全为空注释壳，CRUD 零覆盖。
// 此处用编译期断言锁定 BaseRepo 实现 BaseRepository 接口契约——
// 接口一旦与实现漂移，编译即失败（比运行期空壳更有价值）。

type testModel struct {
	gorm.Model
	Name string
}

// 编译期保证 *BaseRepo[T] 满足 BaseRepository[T] 接口（N2 接口契约守卫）。
var _ repository.BaseRepository[testModel] = (*repository.BaseRepo[testModel])(nil)

func TestBaseRepoSatisfiesInterface(t *testing.T) {
	// 运行期占位：编译期 var _ 已是主断言，此处仅保证测试函数不被 lint 清理。
	var r repository.BaseRepository[testModel] = repository.NewBaseRepo[testModel](nil)
	_ = r
}
