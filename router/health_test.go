package router_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func TestRegisterHealthRouteTimesOutStuckCheck_M7(t *testing.T) {
	r := setupTestRouter()
	router.RegisterHealthRoute(r, router.HealthCheck{
		Name:    "stuck",
		Timeout: 10 * time.Millisecond,
		Check: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})

	w := httptest.NewRecorder()
	start := time.Now()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("health check took %s, want bounded timeout", elapsed)
	}
	if !strings.Contains(w.Body.String(), `"stuck":"timeout"`) {
		t.Fatalf("body = %s, want timeout status", w.Body.String())
	}
}

func TestRegisterHealthRouteRecoversCheckPanic_M7(t *testing.T) {
	r := setupTestRouter()
	router.RegisterHealthRoute(r, router.HealthCheck{
		Name: "panic",
		Check: func(context.Context) error {
			panic("boom")
		},
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"panic":"error"`) {
		t.Fatalf("body = %s, want panic check error status", w.Body.String())
	}
}

func TestRegisterHealthRouteLimitsNonCooperativeCheckConcurrency_M7(t *testing.T) {
	var started atomic.Int32
	release := make(chan struct{})
	defer close(release)

	r := setupTestRouter()
	router.RegisterHealthRoute(r, router.HealthCheck{
		Name:    "stuck",
		Timeout: 10 * time.Millisecond,
		Check: func(context.Context) error {
			started.Add(1)
			<-release
			return nil
		},
	})

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("request %d status = %d, want 503", i+1, w.Code)
		}
	}

	if got := started.Load(); got != 1 {
		t.Fatalf("check started %d times, want 1 while first non-cooperative check is still running", got)
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
