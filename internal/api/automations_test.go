package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"setu/internal/automation"
	"setu/internal/device"
	"setu/internal/events"
	"setu/internal/manager"
)

type automationSwitch struct {
	mu    sync.Mutex
	state device.State
}

type readTracker struct{ read bool }

func (r *readTracker) Read([]byte) (int, error) {
	r.read = true
	return 0, errors.New("body should not be read")
}

func (*automationSwitch) ID() string             { return "lamp" }
func (*automationSwitch) Name() string           { return "Lamp" }
func (*automationSwitch) Brand() string          { return "test" }
func (*automationSwitch) Model() string          { return "switch" }
func (*automationSwitch) MAC() string            { return "02:00:00:00:00:02" }
func (*automationSwitch) Capabilities() []string { return []string{device.CapSwitch} }
func (d *automationSwitch) State() device.State  { d.mu.Lock(); defer d.mu.Unlock(); return d.state }
func (d *automationSwitch) On() error            { d.mu.Lock(); d.state.On = true; d.mu.Unlock(); return nil }
func (d *automationSwitch) Off() error           { d.mu.Lock(); d.state.On = false; d.mu.Unlock(); return nil }

func automationServer(t *testing.T) (*Server, string) {
	t.Helper()
	bus := events.NewBus()
	mgr := manager.New(bus, []device.Device{&automationSwitch{}})
	t.Cleanup(mgr.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine, err := automation.New(mgr, bus, automation.NewStore(filepath.Join(t.TempDir(), "automations.json")), log)
	if err != nil {
		t.Fatal(err)
	}
	update, err := engine.Replace(automation.State{
		Version:  automation.FormatVersion,
		Revision: 0,
		Items: []automation.Rule{{
			ID:      "hook",
			Name:    "Hook",
			Enabled: true,
			Trigger: automation.Trigger{Type: automation.TriggerWebhook, Webhook: &automation.Webhook{}},
			Actions: []automation.Action{{DeviceID: "lamp", Action: "on"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{Manager: mgr, Bus: bus, Automation: engine, Token: "admin", Logger: log}), update.GeneratedTokens["hook"]
}

func TestWebhookUsesOnlyPerRuleHeaderToken(t *testing.T) {
	server, token := automationServer(t)

	for name, request := range map[string]*http.Request{
		"query token": httptest.NewRequest(http.MethodPost, "/api/automation-hooks/hook?token="+token, nil),
		"admin token": func() *http.Request {
			r := httptest.NewRequest(http.MethodPost, "/api/automation-hooks/hook", nil)
			r.Header.Set("Authorization", "Bearer admin")
			return r
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, request)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", w.Code)
			}
		})
	}

	request := httptest.NewRequest(http.MethodPost, "/api/automation-hooks/hook", strings.NewReader(`{"ignored":true}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Idempotency-Key", "event-1")
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, request)
	if w.Code != http.StatusAccepted {
		t.Fatalf("valid webhook status = %d, want 202: %s", w.Code, w.Body.String())
	}
}

func TestWebhookBodyIsBounded(t *testing.T) {
	server, token := automationServer(t)
	request := httptest.NewRequest(http.MethodPost, "/api/automation-hooks/hook", bytes.NewReader(make([]byte, 4097)))
	request.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, request)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
}

func TestWebhookRejectsTokenBeforeReadingBody(t *testing.T) {
	server, _ := automationServer(t)
	body := &readTracker{}
	request := httptest.NewRequest(http.MethodPost, "/api/automation-hooks/hook", body)
	request.Header.Set("Authorization", "Bearer wrong-token-that-is-long-enough")
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, request)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if body.read {
		t.Fatal("unauthorized webhook body was read")
	}
}

func TestAutomationViewRedactsWebhookHashButExportKeepsIt(t *testing.T) {
	server, _ := automationServer(t)
	read := func(path string) automation.State {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer admin")
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, w.Code)
		}
		if path == "/api/automations" {
			var snapshot automation.Snapshot
			if err := json.NewDecoder(w.Body).Decode(&snapshot); err != nil {
				t.Fatal(err)
			}
			return snapshot.State
		}
		var state automation.State
		if err := json.NewDecoder(w.Body).Decode(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	view := read("/api/automations").Items[0].Trigger.Webhook
	export := read("/api/automations/export").Items[0].Trigger.Webhook
	if view.SecretHash != "" || !view.HasSecret {
		t.Fatalf("view webhook = %+v", view)
	}
	if export.SecretHash == "" {
		t.Fatal("export omitted webhook hash")
	}
}
