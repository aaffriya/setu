package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestStaticHandlerCacheControl(t *testing.T) {
	t.Parallel()

	dist := fstest.MapFS{
		"index.html":              {Data: []byte("<!doctype html><title>Setu</title>")},
		"service-worker.js":       {Data: []byte("self.addEventListener('fetch', () => {})")},
		"assets/index-content.js": {Data: []byte("console.log('setu')")},
	}
	handler := (&Server{dist: dist}).staticHandler()

	tests := []struct {
		name         string
		path         string
		wantStatus   int
		wantControl  string
		wantLocation string
	}{
		{name: "root HTML", path: "/", wantStatus: http.StatusOK, wantControl: "no-cache"},
		{name: "index HTML", path: "/index.html", wantStatus: http.StatusOK, wantControl: "no-cache"},
		{name: "service worker", path: "/service-worker.js", wantStatus: http.StatusOK, wantControl: "no-cache"},
		{name: "hashed asset", path: "/assets/index-content.js", wantStatus: http.StatusOK, wantControl: "public, max-age=31536000, immutable"},
		{name: "missing hashed asset", path: "/assets/index-stale.js", wantStatus: http.StatusNotFound, wantControl: "no-store"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			res := httptest.NewRecorder()

			handler.ServeHTTP(res, req)

			if res.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", res.Code, tt.wantStatus)
			}
			if got := res.Header().Get("Cache-Control"); got != tt.wantControl {
				t.Errorf("Cache-Control = %q, want %q", got, tt.wantControl)
			}
			if got := res.Header().Get("Location"); got != tt.wantLocation {
				t.Errorf("Location = %q, want %q", got, tt.wantLocation)
			}
		})
	}
}

func TestAppRecoveryPreservesLocalPreferences(t *testing.T) {
	t.Parallel()

	handler := (&Server{dist: fstest.MapFS{}}).Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/recover", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-store")
	}
	body := res.Body.String()
	for _, want := range []string{
		"getRegistrations",
		"var rootScope = new URL('/', location.href).href",
		"return registration.scope === rootScope",
		"unregister",
		"setu-shell-",
		"location.replace('/')",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("recovery body missing %q", want)
		}
	}
	filterAt := strings.Index(body, "registrations.filter(function (registration)")
	unregisterAt := strings.Index(body, "registration.unregister()")
	if filterAt < 0 || unregisterAt < 0 || filterAt > unregisterAt {
		t.Error("recovery must filter registrations by Setu's root scope before unregistering")
	}
	if strings.Contains(body, "localStorage.clear") {
		t.Error("recovery must not clear localStorage preferences")
	}
}
