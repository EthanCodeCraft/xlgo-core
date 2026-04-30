package router_test

import (
	"net/http/httptest"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/router"
	"github.com/gin-gonic/gin"
)

func setupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return gin.New()
}

func TestNewRegistry(t *testing.T) {
	engine := setupTestRouter()
	registry := router.NewRegistry(engine)

	if registry == nil {
		t.Error("NewRegistry should not return nil")
	}
}

func TestRegistryUse(t *testing.T) {
	engine := setupTestRouter()
	registry := router.NewRegistry(engine)

	result := registry.Use(func(c *gin.Context) { c.Next() })
	if result == nil {
		t.Error("Use should return registry for chaining")
	}
}

func TestRegisterModule(t *testing.T) {
	engine := setupTestRouter()
	registry := router.NewRegistry(engine)

	module := &testModule{name: "test"}
	result := registry.RegisterModule(module)
	if result == nil {
		t.Error("RegisterModule should return registry for chaining")
	}
}

func TestRegisterModuleFunc(t *testing.T) {
	engine := setupTestRouter()
	registry := router.NewRegistry(engine)

	result := registry.RegisterModuleFunc("test", func(r *gin.RouterGroup) {
		r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{}) })
	})
	if result == nil {
		t.Error("RegisterModuleFunc should return registry for chaining")
	}
}

func TestNewVersion(t *testing.T) {
	v := router.NewVersion("v1", "/api/v1")

	if v.Version != "v1" {
		t.Errorf("Version = %s, want v1", v.Version)
	}
	if v.BasePath != "/api/v1" {
		t.Errorf("BasePath = %s, want /api/v1", v.BasePath)
	}
}

func TestVersionAddModule(t *testing.T) {
	v := router.NewVersion("v1", "/api/v1")
	module := &testModule{name: "user"}

	result := v.AddModule(module)
	if result == nil {
		t.Error("AddModule should return VersionedAPI for chaining")
	}
}

func TestVersionAddModuleFunc(t *testing.T) {
	v := router.NewVersion("v1", "/api/v1")

	result := v.AddModuleFunc("user", func(r *gin.RouterGroup) {
		r.GET("/users", func(c *gin.Context) {})
	})
	if result == nil {
		t.Error("AddModuleFunc should return VersionedAPI for chaining")
	}
}

func TestRegisterVersion(t *testing.T) {
	engine := setupTestRouter()
	registry := router.NewRegistry(engine)

	v := router.NewVersion("v1", "/api/v1")
	result := registry.RegisterVersion(v)
	if result == nil {
		t.Error("RegisterVersion should return registry for chaining")
	}
}

func TestNewMiddlewareGroup(t *testing.T) {
	group := router.NewMiddlewareGroup("auth", func(c *gin.Context) { c.Next() })

	if group.Name != "auth" {
		t.Errorf("Name = %s, want auth", group.Name)
	}
	if len(group.Middlewares) != 1 {
		t.Errorf("Middlewares length = %d, want 1", len(group.Middlewares))
	}
}

func TestRegisterMiddlewareGroup(t *testing.T) {
	engine := setupTestRouter()
	registry := router.NewRegistry(engine)

	group := router.NewMiddlewareGroup("auth")
	result := registry.RegisterMiddlewareGroup(group)
	if result == nil {
		t.Error("RegisterMiddlewareGroup should return registry for chaining")
	}
}

func TestGetMiddlewareGroup(t *testing.T) {
	engine := setupTestRouter()
	registry := router.NewRegistry(engine)

	middleware := func(c *gin.Context) { c.Next() }
	group := router.NewMiddlewareGroup("auth", middleware)
	registry.RegisterMiddlewareGroup(group)

	mws := registry.GetMiddlewareGroup("auth")
	if len(mws) != 1 {
		t.Errorf("GetMiddlewareGroup length = %d, want 1", len(mws))
	}

	// 不存在的分组
	mws2 := registry.GetMiddlewareGroup("nonexistent")
	if mws2 != nil {
		t.Error("GetMiddlewareGroup nonexistent should return nil")
	}
}

func TestApply(t *testing.T) {
	engine := setupTestRouter()
	registry := router.NewRegistry(engine)

	registry.RegisterModuleFunc("test", func(r *gin.RouterGroup) {
		r.GET("/hello", func(c *gin.Context) {
			c.JSON(200, gin.H{"message": "hello"})
		})
	})

	registry.Apply()

	// 测试路由是否生效
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/hello", nil)
	engine.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("Apply route status = %d, want 200", w.Code)
	}
}

func TestApplyWithVersion(t *testing.T) {
	engine := setupTestRouter()
	registry := router.NewRegistry(engine)

	v1 := router.NewVersion("v1", "/api/v1")
	v1.AddModuleFunc("user", func(r *gin.RouterGroup) {
		r.GET("/users", func(c *gin.Context) {
			c.JSON(200, gin.H{"version": "v1"})
		})
	})
	registry.RegisterVersion(v1)

	registry.Apply()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	engine.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("Versioned route status = %d, want 200", w.Code)
	}
}

func TestApplyWithMiddleware(t *testing.T) {
	engine := setupTestRouter()
	registry := router.NewRegistry(engine)

	// 全局中间件
	registry.Use(func(c *gin.Context) {
		c.Set("global", true)
		c.Next()
	})

	registry.RegisterModuleFunc("test", func(r *gin.RouterGroup) {
		r.GET("/test", func(c *gin.Context) {
			val, _ := c.Get("global")
			c.JSON(200, gin.H{"global": val})
		})
	})

	registry.Apply()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	engine.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("Middleware route status = %d", w.Code)
	}
}

func TestVersionMiddleware(t *testing.T) {
	engine := setupTestRouter()
	registry := router.NewRegistry(engine)

	v1 := router.NewVersion("v1", "/api/v1", func(c *gin.Context) {
		c.Set("version", "v1")
		c.Next()
	})
	v1.AddModuleFunc("user", func(r *gin.RouterGroup) {
		r.GET("/users", func(c *gin.Context) {
			val, _ := c.Get("version")
			c.JSON(200, gin.H{"version": val})
		})
	})
	registry.RegisterVersion(v1)

	registry.Apply()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	engine.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("Version middleware route status = %d", w.Code)
	}
}

func TestMultipleVersions(t *testing.T) {
	engine := setupTestRouter()
	registry := router.NewRegistry(engine)

	v1 := router.NewVersion("v1", "/api/v1")
	v1.AddModuleFunc("user", func(r *gin.RouterGroup) {
		r.GET("/users", func(c *gin.Context) { c.JSON(200, gin.H{"version": "v1"}) })
	})

	v2 := router.NewVersion("v2", "/api/v2")
	v2.AddModuleFunc("user", func(r *gin.RouterGroup) {
		r.GET("/users", func(c *gin.Context) { c.JSON(200, gin.H{"version": "v2"}) })
	})

	registry.RegisterVersion(v1)
	registry.RegisterVersion(v2)
	registry.Apply()

	// 测试 v1
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/api/v1/users", nil)
	engine.ServeHTTP(w1, req1)

	// 测试 v2
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/v2/users", nil)
	engine.ServeHTTP(w2, req2)

	if w1.Code != 200 || w2.Code != 200 {
		t.Error("Multiple versions should both work")
	}
}

func TestInitAndGetRegistry(t *testing.T) {
	engine := setupTestRouter()
	registry := router.Init(engine)

	if registry == nil {
		t.Error("Init should return registry")
	}

	registry2 := router.GetRegistry()
	if registry2 != registry {
		t.Error("GetRegistry should return same registry")
	}
}

func TestGlobalFunctions(t *testing.T) {
	engine := setupTestRouter()
	router.Init(engine)

	router.Use(func(c *gin.Context) { c.Next() })
	router.RegisterModule(&testModule{name: "test"})
	router.RegisterVersion(router.NewVersion("v1", "/api/v1"))

	registry := router.GetRegistry()
	if registry == nil {
		t.Error("Global functions should work")
	}
}

func TestGlobalApply(t *testing.T) {
	engine := setupTestRouter()
	router.Init(engine)

	router.RegisterModuleFunc("hello", func(r *gin.RouterGroup) {
		r.GET("/hello", func(c *gin.Context) {
			c.JSON(200, gin.H{"message": "hello"})
		})
	})

	router.Apply()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/hello", nil)
	engine.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("Global Apply status = %d, want 200", w.Code)
	}
}

func TestModuleFunc(t *testing.T) {
	var mf router.ModuleFunc = func(r *gin.RouterGroup) {
		r.GET("/test", func(c *gin.Context) {})
	}

	// 验证实现了 Module 接口
	var _ router.Module = mf
}

func TestRESTfulRoute(t *testing.T) {
	engine := setupTestRouter()
	group := engine.Group("/api")

	rest := router.NewRESTful(group, "/users")
	rest.GET(func(c *gin.Context) { c.JSON(200, gin.H{}) })
	rest.POST(func(c *gin.Context) { c.JSON(201, gin.H{}) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/users", nil)
	engine.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("RESTful GET status = %d", w.Code)
	}
}

func TestRESTfulCRUD(t *testing.T) {
	engine := setupTestRouter()
	group := engine.Group("/api")

	rest := router.NewRESTful(group, "/items")
	rest.CRUD(
		func(c *gin.Context) { c.JSON(200, gin.H{"action": "list"}) },
		func(c *gin.Context) { c.JSON(200, gin.H{"action": "detail"}) },
		func(c *gin.Context) { c.JSON(201, gin.H{"action": "create"}) },
		func(c *gin.Context) { c.JSON(200, gin.H{"action": "update"}) },
		func(c *gin.Context) { c.JSON(204, gin.H{}) },
	)

	tests := []struct {
		method string
		path   string
		code   int
	}{
		{"GET", "/api/items", 200},
		{"GET", "/api/items/1", 200},
		{"POST", "/api/items", 201},
		{"PUT", "/api/items/1", 200},
		{"DELETE", "/api/items/1", 204},
	}

	for _, tt := range tests {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tt.method, tt.path, nil)
		engine.ServeHTTP(w, req)

		if w.Code != tt.code {
			t.Errorf("%s %s status = %d, want %d", tt.method, tt.path, w.Code, tt.code)
		}
	}
}

func TestRESTfulPartialCRUD(t *testing.T) {
	engine := setupTestRouter()
	group := engine.Group("/api")

	rest := router.NewRESTful(group, "/items")
	// 只注册 list 和 create
	rest.CRUD(
		func(c *gin.Context) { c.JSON(200, gin.H{}) }, // list
		nil, // no detail
		func(c *gin.Context) { c.JSON(201, gin.H{}) }, // create
		nil, // no update
		nil, // no delete
	)

	// list 存在
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/api/items", nil)
	engine.ServeHTTP(w1, req1)
	if w1.Code != 200 {
		t.Error("Partial CRUD list should work")
	}

	// detail 不存在（404）
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/items/1", nil)
	engine.ServeHTTP(w2, req2)
	if w2.Code != 404 {
		t.Errorf("Partial CRUD detail should be 404, got %d", w2.Code)
	}
}

func TestGroup(t *testing.T) {
	engine := setupTestRouter()
	group := router.Group(engine, "/api", func(c *gin.Context) { c.Next() })

	if group == nil {
		t.Error("Group should not return nil")
	}
}

func TestGroupWithMiddlewareGroup(t *testing.T) {
	engine := setupTestRouter()
	registry := router.NewRegistry(engine)

	registry.RegisterMiddlewareGroup(router.NewMiddlewareGroup("auth", func(c *gin.Context) { c.Next() }))

	group := router.GroupWithMiddlewareGroup(engine, "/api", "auth")
	if group == nil {
		t.Error("GroupWithMiddlewareGroup should not return nil")
	}
}

func TestModuleInterface(t *testing.T) {
	module := &testModule{name: "test"}

	// 测试 Name 方法
	if module.Name() != "test" {
		t.Error("Module Name failed")
	}

	// 测试实现了接口
	var _ router.Module = module
}

// 测试模块实现
type testModule struct {
	name string
}

func (m *testModule) Name() string { return m.name }
func (m *testModule) Register(r *gin.RouterGroup) {
	r.GET("/"+m.name, func(c *gin.Context) { c.JSON(200, gin.H{"module": m.name}) })
}