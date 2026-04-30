package wire

import (
	"github.com/EthanCodeCraft/xlgo-core/cache"
)

// ServiceContainer 服务容器
type ServiceContainer struct {
	// 应用可以在这里添加自己的服务
}

// 全局服务容器
var container *ServiceContainer

// InitServices 初始化所有服务
func InitServices() *ServiceContainer {
	// 初始化缓存
	cache.Init()

	// 创建服务容器
	container = &ServiceContainer{}

	return container
}

// GetContainer 获取服务容器
func GetContainer() *ServiceContainer {
	if container == nil {
		InitServices()
	}
	return container
}
