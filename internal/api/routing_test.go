package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"setu/internal/device"
	"setu/internal/events"
	"setu/internal/manager"
)

func routingServer(t *testing.T) *Server {
	t.Helper()
	bus := events.NewBus()
	mgr := manager.New(bus, []device.Device{&refreshDevice{}})
	t.Cleanup(mgr.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(Options{Manager: mgr, Bus: bus, Token: "secret", Logger: log})
}

// The admin token must not travel in a URL, where it is copied into access logs,
// browser history, and Referer headers. Only /ws may take it that way, because a
// browser WebSocket cannot set an Authorization header.
func TestQueryTokenIsAcceptedOnlyByTheWebSocket(t *testing.T) {
	srv := routingServer(t)

	for name, target := range map[string]string{
		"device list": "/api/devices?token=secret",
		"diagnostics": "/api/diagnostics?token=secret",
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401: %s", w.Code, w.Body.String())
			}
		})
	}

	// The header remains the supported way to reach the same endpoint.
	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("header-authenticated status = %d, want 200: %s", w.Code, w.Body.String())
	}
}

// A /ws request carrying the query token gets past authentication: httptest's
// recorder cannot be hijacked for the upgrade, so anything other than 401 proves
// the handler itself was reached.
func TestWebSocketStillAcceptsQueryToken(t *testing.T) {
	srv := routingServer(t)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ws?token=secret", nil))
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("status = 401; the browser WebSocket must still authenticate with ?token=")
	}
}

// An unrouted /api path is an API mistake, not a page. Falling through to the
// SPA would answer 200 with index.html, which every JSON client reports as an
// unparseable body instead of a plain "not found".
func TestUnknownAPIPathReturnsJSONNotFound(t *testing.T) {
	srv := routingServer(t)

	// Automation routes are mounted only when an engine is configured; this
	// server has none, so they must read as missing endpoints.
	for name, target := range map[string]string{
		"typo":               "/api/devicez",
		"absent automations": "/api/automations",
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, target, nil)
			req.Header.Set("Authorization", "Bearer secret")
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404: %s", w.Code, w.Body.String())
			}
			if body := w.Body.String(); strings.Contains(body, "<!doctype") {
				t.Fatalf("body = %q, want JSON rather than the SPA shell", body)
			}
			var payload struct {
				Error string `json:"error"`
			}
			if err := json.NewDecoder(w.Body).Decode(&payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload.Error == "" {
				t.Fatal("404 response carried no error message")
			}
		})
	}
}

// The SPA fallback itself must keep working for real navigations.
func TestNonAPIPathStillServesTheApp(t *testing.T) {
	srv := routingServer(t)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/some/deep/link", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type = %q, want HTML", ct)
	}
}

// RFC 7235 makes the auth scheme case-insensitive, and clients do vary ("bearer"
// from some HTTP libraries). The token after it stays exact.
func TestBearerSchemeIsCaseInsensitive(t *testing.T) {
	srv := routingServer(t)
	handler := srv.Handler()

	for _, header := range []string{"Bearer secret", "bearer secret", "BEARER secret"} {
		req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
		req.Header.Set("Authorization", header)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("Authorization %q = %d, want 200", header, rec.Code)
		}
	}

	for _, header := range []string{"Bearer SECRET", "Bearer", "Bearer ", "Basic secret"} {
		req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
		req.Header.Set("Authorization", header)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Authorization %q = %d, want 401", header, rec.Code)
		}
	}
}
