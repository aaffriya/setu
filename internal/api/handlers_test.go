package api

import (
	"context"
	"encoding/json"
	"errors"
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
}

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
