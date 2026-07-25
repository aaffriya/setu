package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"setu/internal/device"
	"setu/internal/events"
	"setu/internal/manager"
)

type refreshDevice struct {
	polls      atomic.Int64
	on         atomic.Bool
	commandErr bool
	pollErr    bool
}

type unpollableDevice struct{}

func (*unpollableDevice) ID() string             { return "wake-only" }
func (*unpollableDevice) Name() string           { return "Wake only" }
func (*unpollableDevice) Brand() string          { return "test" }
func (*unpollableDevice) Model() string          { return "wake" }
func (*unpollableDevice) MAC() string            { return "02:00:00:00:00:02" }
func (*unpollableDevice) Capabilities() []string { return []string{device.CapWoL} }
func (*unpollableDevice) State() device.State    { return device.State{Online: true} }

func (*refreshDevice) ID() string             { return "refreshable" }
func (*refreshDevice) Name() string           { return "Refreshable" }
func (*refreshDevice) Brand() string          { return "test" }
func (*refreshDevice) Model() string          { return "test" }
func (*refreshDevice) MAC() string            { return "02:00:00:00:00:01" }
func (*refreshDevice) Capabilities() []string { return []string{device.CapSwitch} }
func (d *refreshDevice) State() device.State {
	return device.State{Online: true, On: d.on.Load(), Brightness: int(d.polls.Load())}
}
func (d *refreshDevice) Poll() (device.State, error) {
	if d.pollErr {
		return device.State{
			Online:     false,
			On:         d.on.Load(),
			Brightness: int(d.polls.Load()),
		}, fmt.Errorf("test poll failed: %w", device.ErrPollNoResponse)
	}
	return device.State{Online: true, On: d.on.Load(), Brightness: int(d.polls.Add(1))}, nil
}
func (d *refreshDevice) On() error {
	d.on.Store(true)
	if d.commandErr {
		return errors.New("acknowledgement lost")
	}
	return nil
}
func (d *refreshDevice) Off() error {
	d.on.Store(false)
	return nil
}

func TestListDevicesHardwareRefresh(t *testing.T) {
	bus := events.NewBus()
	dev := &refreshDevice{}
	mgr := manager.New(bus, []device.Device{dev})
	defer mgr.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	poller := manager.NewPoller(mgr, 0, log)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		poller.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	srv := New(Options{Manager: mgr, Poller: poller, Bus: bus, Token: "secret", Logger: log})
	req := httptest.NewRequest(http.MethodGet, "/api/devices?refresh=true", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var views []manager.DeviceView
	if err := json.NewDecoder(w.Body).Decode(&views); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(views) != 1 || views[0].State.Brightness != 1 {
		t.Fatalf("refreshed views = %+v, want brightness 1", views)
	}
}

func TestCommandErrorIncludesReconciledDevice(t *testing.T) {
	bus := events.NewBus()
	dev := &refreshDevice{commandErr: true}
	mgr := manager.New(bus, []device.Device{dev})
	defer mgr.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(Options{Manager: mgr, Bus: bus, Token: "secret", Logger: log})

	req := httptest.NewRequest(http.MethodPost, "/api/devices/refreshable/command", strings.NewReader(`{"action":"on"}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: %s", w.Code, w.Body.String())
	}
	var response errorResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Device == nil || !response.Device.State.On {
		t.Fatalf("reconciled command error = %+v", response)
	}
}

func TestPerDeviceRefreshAndDiagnostics(t *testing.T) {
	bus := events.NewBus()
	dev := &refreshDevice{}
	mgr := manager.New(bus, []device.Device{dev})
	defer mgr.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(Options{Manager: mgr, Bus: bus, Token: "secret", Logger: log})
	handler := srv.Handler()

	refreshReq := httptest.NewRequest(http.MethodPost, "/api/devices/refreshable/refresh", nil)
	refreshReq.Header.Set("Authorization", "Bearer secret")
	refreshW := httptest.NewRecorder()
	handler.ServeHTTP(refreshW, refreshReq)
	if refreshW.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200: %s", refreshW.Code, refreshW.Body.String())
	}
	var view manager.DeviceView
	if err := json.NewDecoder(refreshW.Body).Decode(&view); err != nil {
		t.Fatalf("decode refresh: %v", err)
	}
	if view.ID != dev.ID() || view.State.Brightness != 1 {
		t.Fatalf("refreshed view = %+v", view)
	}

	diagnosticsReq := httptest.NewRequest(http.MethodGet, "/api/diagnostics", nil)
	diagnosticsReq.Header.Set("Authorization", "Bearer secret")
	diagnosticsW := httptest.NewRecorder()
	handler.ServeHTTP(diagnosticsW, diagnosticsReq)
	if diagnosticsW.Code != http.StatusOK {
		t.Fatalf("diagnostics status = %d, want 200: %s", diagnosticsW.Code, diagnosticsW.Body.String())
	}
	var diagnostics []manager.DeviceDiagnostics
	if err := json.NewDecoder(diagnosticsW.Body).Decode(&diagnostics); err != nil {
		t.Fatalf("decode diagnostics: %v", err)
	}
	if len(diagnostics) != 1 || !diagnostics[0].Pollable || diagnostics[0].LastPollAt == 0 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}

func TestPerDeviceRefreshFailureIsRecorded(t *testing.T) {
	bus := events.NewBus()
	dev := &refreshDevice{pollErr: true}
	mgr := manager.New(bus, []device.Device{dev})
	defer mgr.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(Options{Manager: mgr, Bus: bus, Token: "secret", Logger: log})
	handler := srv.Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/devices/refreshable/refresh", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("refresh status = %d, want 502: %s", w.Code, w.Body.String())
	}
	var response errorResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode refresh error: %v", err)
	}
	if response.Device == nil || response.Device.State.Online {
		t.Fatalf("refresh error did not include fallback state: %+v", response)
	}

	diagnostics := mgr.Diagnostics()
	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0].LastPollError, "test poll failed") {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}

func TestPerDeviceRefreshRejectsUnpollableDevice(t *testing.T) {
	bus := events.NewBus()
	dev := &unpollableDevice{}
	mgr := manager.New(bus, []device.Device{dev})
	defer mgr.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(Options{Manager: mgr, Bus: bus, Token: "secret", Logger: log})

	req := httptest.NewRequest(http.MethodPost, "/api/devices/wake-only/refresh", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("refresh status = %d, want 400: %s", w.Code, w.Body.String())
	}

	diagnostics := mgr.Diagnostics()
	if len(diagnostics) != 1 || diagnostics[0].Pollable || diagnostics[0].LastPollAt != 0 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}
