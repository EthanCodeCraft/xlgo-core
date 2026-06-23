package router_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/router"
)

func TestRegisterLivenessRoute(t *testing.T) {
	r := setupTestRouter()
	router.RegisterLivenessRoute(r)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("livez status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Fatalf("livez body = %s", w.Body.String())
	}
}

func TestRegisterReadinessRouteHealthy(t *testing.T) {
	r := setupTestRouter()
	router.RegisterReadinessRoute(r,
		router.HealthCheck{Name: "mysql", Check: func(context.Context) error { return nil }},
	)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("readyz status = %d, want 200", w.Code)
	}
}

func TestRegisterReadinessRouteUnhealthy(t *testing.T) {
	r := setupTestRouter()
	router.RegisterReadinessRoute(r,
		router.HealthCheck{Name: "redis", Check: func(context.Context) error { return errors.New("down") }},
	)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz status = %d, want 503", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"redis":"error"`) {
		t.Fatalf("readyz body = %s", w.Body.String())
	}
}
