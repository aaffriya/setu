package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"setu/internal/device"
	"setu/internal/events"
	"setu/internal/manager"
)

type refreshDevice struct {
	polls atomic.Int64
}

func (*refreshDevice) ID() string             { return "refreshable" }
func (*refreshDevice) Name() string           { return "Refreshable" }
func (*refreshDevice) Brand() string          { return "test" }
func (*refreshDevice) Model() string          { return "test" }
func (*refreshDevice) MAC() string            { return "02:00:00:00:00:01" }
func (*refreshDevice) Capabilities() []string { return []string{device.CapSwitch} }
func (d *refreshDevice) State() device.State {
	return device.State{Online: true, Brightness: int(d.polls.Load())}
}
func (d *refreshDevice) Poll() (device.State, error) {
	return device.State{Online: true, Brightness: int(d.polls.Add(1))}, nil
}

func TestListDevicesHardwareRefresh(t *testing.T) {
	bus := events.NewBus()
	dev := &refreshDevice{}
	mgr := manager.New(bus, []device.Device{dev})
	defer mgr.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	poller := manager.NewPoller(mgr, bus, 0, log)
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
