package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

func captureRouterWarnings(t *testing.T) *[]string {
	t.Helper()
	var mu sync.Mutex
	warnings := make([]string, 0)
	prev, hadPrev := routerWarnf.Load().(func(string, ...any))
	routerWarnf.Store(func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		warnings = append(warnings, format)
	})
	t.Cleanup(func() {
		if hadPrev {
			routerWarnf.Store(prev)
			return
		}
		routerWarnf.Store(func(string, ...any) {})
	})
	return &warnings
}

func warningsContain(warnings *[]string, substr string) bool {
	for _, warning := range *warnings {
		if strings.Contains(warning, substr) {
			return true
		}
	}
	return false
}

func TestM7DuplicateGETWarnsAndSkips(t *testing.T) {
	gin.SetMode(gin.TestMode)
	warnings := captureRouterWarnings(t)
	engine := gin.New()

	RegisterHealthRoute(engine)
	RegisterHealthRoute(engine)

	if !warningsContain(warnings, "duplicate GET") {
		t.Fatalf("duplicate registration should warn, got %#v", *warnings)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("/health status = %d, want 200", w.Code)
	}
}

func TestM7RouterGroupWildcardConflictPanics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("")
	group.GET("/files/:id", func(c *gin.Context) {})

	defer func() {
		if rec := recover(); rec == nil {
			t.Fatal("wildcard conflict should panic, not be swallowed as duplicate")
		}
	}()
	registerGETOnce(group, "/files/*path", func(c *gin.Context) {})
}

func TestM7RegistryGroupWithMiddlewareGroupUsesLocalRegistry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	registry := NewRegistry(engine)
	registry.RegisterMiddlewareGroup(NewMiddlewareGroup("auth", func(c *gin.Context) {
		c.Set("auth", true)
		c.Next()
	}))

	group := registry.GroupWithMiddlewareGroup(engine, "/api", "auth")
	if group == nil {
		t.Fatal("GroupWithMiddlewareGroup returned nil")
	}
	group.GET("/check", func(c *gin.Context) {
		v, _ := c.Get("auth")
		c.JSON(http.StatusOK, gin.H{"auth": v})
	})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/check", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"auth":true`) {
		t.Fatalf("local middleware group not applied: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestM7NilGuardsNoPanic(t *testing.T) {
	warnings := captureRouterWarnings(t)
	var nilRegistry *Registry
	var nilEngine *gin.Engine
	var nilGroup *gin.RouterGroup
	var nilRoute *RESTfulRoute

	requireNoPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("%s panicked: %v", name, rec)
			}
		}()
		fn()
	}

	requireNoPanic("RegisterHealthRoute nil engine", func() { RegisterHealthRoute(nilEngine) })
	requireNoPanic("DefaultModule nil group", func() { DefaultModule.Register(nilGroup) })
	requireNoPanic("nil registry Use", func() { nilRegistry.Use(nil) })
	requireNoPanic("nil registry RegisterModule", func() { nilRegistry.RegisterModule(nil) })
	requireNoPanic("nil registry RegisterVersion", func() { nilRegistry.RegisterVersion(nil) })
	requireNoPanic("nil registry RegisterMiddlewareGroup", func() { nilRegistry.RegisterMiddlewareGroup(nil) })
	requireNoPanic("nil registry GetMiddlewareGroup", func() { _ = nilRegistry.GetMiddlewareGroup("x") })
	requireNoPanic("nil registry Apply", func() { nilRegistry.Apply() })
	requireNoPanic("nil engine Group", func() { _ = Group(nil, "/x", nil) })
	requireNoPanic("nil RESTful GET", func() { nilRoute.GET(nil) })
	requireNoPanic("nil RESTful CRUD", func() { nilRoute.CRUD(nil, nil, nil, nil, nil) })

	if len(*warnings) == 0 {
		t.Fatal("nil guards should emit warnings")
	}
}

func TestM7RegistryApplyConcurrentMutationNoRace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = captureRouterWarnings(t)
	engine := gin.New()
	registry := NewRegistry(engine)

	const workers = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(workers + 1)

	for i := 0; i < workers; i++ {
		idx := i
		go func() {
			defer wg.Done()
			<-start
			registry.Use(func(c *gin.Context) { c.Next() })
			registry.RegisterMiddlewareGroup(NewMiddlewareGroup(
				fmt.Sprintf("group-%d", idx),
				func(c *gin.Context) { c.Next() },
			))
			registry.RegisterModuleFunc(fmt.Sprintf("module-%d", idx), func(g *gin.RouterGroup) {
				g.GET(fmt.Sprintf("/m%d", idx), func(c *gin.Context) { c.Status(http.StatusOK) })
			})
			registry.RegisterVersion(NewVersion(
				fmt.Sprintf("v%d", idx),
				fmt.Sprintf("/v%d", idx),
			).AddModuleFunc("ping", func(g *gin.RouterGroup) {
				g.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })
			}))
		}()
	}

	go func() {
		defer wg.Done()
		<-start
		registry.Apply()
	}()

	close(start)
	wg.Wait()
}

func TestM7RegisterAfterApplyWarnsAndDoesNotTakeEffect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	warnings := captureRouterWarnings(t)
	engine := gin.New()
	registry := NewRegistry(engine)
	registry.Apply()

	registry.RegisterModuleFunc("late", func(g *gin.RouterGroup) {
		g.GET("/late", func(c *gin.Context) { c.Status(http.StatusOK) })
	})

	if !warningsContain(warnings, "after Apply") {
		t.Fatalf("late registration should warn, got %#v", *warnings)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/late", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("late route status = %d, want 404", w.Code)
	}
}
