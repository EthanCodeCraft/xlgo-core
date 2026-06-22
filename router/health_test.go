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

func TestRegisterHealthRoute(t *testing.T) {
	r := setupTestRouter()
	router.RegisterHealthRoute(r)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Fatalf("expected ok health response, got %s", w.Body.String())
	}
}

func TestRegisterHealthRouteWithChecks(t *testing.T) {
	r := setupTestRouter()
	router.RegisterHealthRoute(r,
		router.HealthCheck{Name: "mysql", Check: func(context.Context) error { return nil }},
		router.HealthCheck{Name: "redis", Disabled: true},
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"mysql":"ok"`) || !strings.Contains(body, `"redis":"disabled"`) {
		t.Fatalf("expected check statuses, got %s", body)
	}
}

func TestRegisterHealthRouteWithFailingCheck(t *testing.T) {
	r := setupTestRouter()
	router.RegisterHealthRoute(r, router.HealthCheck{Name: "mysql", Check: func(context.Context) error { return errors.New("down") }})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"status":"error"`) {
		t.Fatalf("expected error health response, got %s", w.Body.String())
	}
}

func TestRegisterDefaultRoutes(t *testing.T) {
	r := setupTestRouter()
	router.RegisterDefaultRoutes(r)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected health status 200, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil)
	r.ServeHTTP(w, req)
	if w.Code == http.StatusNotFound {
		t.Fatal("expected swagger route to be registered")
	}
}
