package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/middleware"
	"github.com/gin-gonic/gin"
)

func performAuthMiddlewareRequest(m gin.HandlerFunc, setup func(*gin.Context)) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if setup != nil {
			setup(c)
		}
	})
	r.Use(m)
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.ServeHTTP(w, req)
	return w
}

func setAuthUser(userType, role string) func(*gin.Context) {
	return func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, uint(1))
		c.Set(middleware.ContextKeyUsername, "tester")
		c.Set(middleware.ContextKeyRole, role)
		c.Set(middleware.ContextKeyUserType, userType)
	}
}

func TestRequireUserTypes(t *testing.T) {
	w := performAuthMiddlewareRequest(middleware.RequireUserTypes("tenant_admin"), setAuthUser("tenant_admin", "owner"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("expected request to pass, got body %s", w.Body.String())
	}
}

func TestRequireUserTypesRejectsOtherTypes(t *testing.T) {
	w := performAuthMiddlewareRequest(middleware.RequireUserTypes("tenant_admin"), setAuthUser("staff", "owner"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected business error over status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"code":403`) {
		t.Fatalf("expected forbidden response, got body %s", w.Body.String())
	}
}

func TestRequireRoles(t *testing.T) {
	w := performAuthMiddlewareRequest(middleware.RequireRoles("owner"), setAuthUser("tenant_admin", "owner"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("expected request to pass, got body %s", w.Body.String())
	}
}

func TestRequireAuthCustomChecker(t *testing.T) {
	checker := func(user middleware.AuthUser, c *gin.Context) bool {
		return user.UserID == 1 && user.UserType == "merchant" && user.Role == "owner"
	}

	w := performAuthMiddlewareRequest(middleware.RequireAuth(checker), setAuthUser("merchant", "owner"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("expected request to pass, got body %s", w.Body.String())
	}

	w = performAuthMiddlewareRequest(middleware.RequireAuth(checker, "需要商户所有者权限"), setAuthUser("merchant", "staff"))
	if !strings.Contains(w.Body.String(), "需要商户所有者权限") {
		t.Fatalf("expected custom forbidden message, got body %s", w.Body.String())
	}
}

func TestDefaultRoleShortcuts(t *testing.T) {
	tests := []struct {
		name     string
		mw       gin.HandlerFunc
		userType string
		wantPass bool
	}{
		{"admin allows super admin", middleware.AdminRequired(), middleware.DefaultUserTypeSuperAdmin, true},
		{"admin allows admin", middleware.AdminRequired(), middleware.DefaultUserTypeAdmin, true},
		{"admin rejects staff", middleware.AdminRequired(), middleware.DefaultUserTypeStaff, false},
		{"super admin allows super admin", middleware.SuperAdminRequired(), middleware.DefaultUserTypeSuperAdmin, true},
		{"super admin rejects admin", middleware.SuperAdminRequired(), middleware.DefaultUserTypeAdmin, false},
		{"staff allows staff", middleware.StaffRequired(), middleware.DefaultUserTypeStaff, true},
		{"any allows staff", middleware.AnyUserRequired(), middleware.DefaultUserTypeStaff, true},
		{"any rejects external", middleware.AnyUserRequired(), "external", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := performAuthMiddlewareRequest(tt.mw, setAuthUser(tt.userType, "role"))
			body := w.Body.String()
			if tt.wantPass && !strings.Contains(body, `"ok":true`) {
				t.Fatalf("expected pass, got body %s", body)
			}
			if !tt.wantPass && !strings.Contains(body, `"code":403`) {
				t.Fatalf("expected forbidden, got body %s", body)
			}
		})
	}
}

func TestRequireAuthRejectsMissingContext(t *testing.T) {
	w := performAuthMiddlewareRequest(middleware.RequireUserTypes("admin"), nil)
	if !strings.Contains(w.Body.String(), `"code":401`) || !strings.Contains(w.Body.String(), "请先登录") {
		t.Fatalf("expected unauthorized login response, got body %s", w.Body.String())
	}
}

func TestRequireAuthRejectsMalformedContext(t *testing.T) {
	w := performAuthMiddlewareRequest(middleware.RequireUserTypes("admin"), func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, uint(1))
		c.Set(middleware.ContextKeyUsername, "tester")
		c.Set(middleware.ContextKeyRole, "owner")
		c.Set(middleware.ContextKeyUserType, 123)
	})
	if !strings.Contains(w.Body.String(), `"code":401`) || !strings.Contains(w.Body.String(), "用户信息异常") {
		t.Fatalf("expected malformed user response, got body %s", w.Body.String())
	}
}

func TestGetAuthUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	setAuthUser("tenant_admin", "owner")(c)

	user, ok := middleware.GetAuthUser(c)
	if !ok {
		t.Fatal("expected auth user")
	}
	if user.UserID != 1 || user.Username != "tester" || user.UserType != "tenant_admin" || user.Role != "owner" {
		t.Fatalf("unexpected auth user: %+v", user)
	}
	if middleware.GetRole(c) != "owner" {
		t.Fatalf("expected role owner, got %q", middleware.GetRole(c))
	}

	c.Set(middleware.ContextKeyUserID, "bad")
	if _, ok := middleware.GetAuthUser(c); ok {
		t.Fatal("expected malformed auth user to fail")
	}
}
