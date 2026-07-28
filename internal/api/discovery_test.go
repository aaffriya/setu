package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"setu/internal/config"
	"setu/internal/manager"
	"setu/internal/resolver"
)

// stubScanner stands in for a brand scanner: it answers with a fixed list, and
// can block so a second request meets a scan already in flight.
type stubScanner struct {
	found   []resolver.Candidate
	err     error
	entered chan<- struct{}
	blockOn <-chan struct{}
}

func (s *stubScanner) Scan(ctx context.Context) ([]resolver.Candidate, error) {
	if s.entered != nil {
		close(s.entered)
	}
	if s.blockOn != nil {
		select {
		case <-s.blockOn:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.found, s.err
}

// scanServer is the device server plus the given brand scanners.
func scanServer(t *testing.T, specs []config.DeviceSpec, scanners ...resolver.Scanner) http.Handler {
	t.Helper()
	handler, _, _ := newTestServer(t, specs, scanners)
	return handler
}

func scanRequest(t *testing.T, handler http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/discovery/scan", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// The scan is only useful if the user can tell "already added" from "new" —
// that is what decides whether a result can be added at all. Matching is by
// MAC, in whatever notation each side happens to use.
func TestScanMarksDevicesAlreadyAdded(t *testing.T) {
	handler := scanServer(t,
		[]config.DeviceSpec{lamp("desk", "98:77:d5:a2:34:f2")},
		// Reported bare, as WiZ does — it must still match the stored
		// colon-separated MAC.
		&stubScanner{found: []resolver.Candidate{
			{Brand: "test", Driver: "lamp", MAC: "9877d5a234f2"},
			{Brand: "test", Driver: "lamp", MAC: "d8:a0:11:ff:5e:f0"},
			{Brand: "test", Driver: "", Model: "ESP10_SOCKET_06", MAC: "d8:a0:11:ff:5e:f1"},
		}},
		&stubScanner{err: errors.New("samsung discovery: listen: no route")},
	)

	w := scanRequest(t, handler)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var got discoveryResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(got.Candidates) != 3 {
		t.Fatalf("candidates = %+v, want 3", got.Candidates)
	}
	// One brand failing must not hide what the others found.
	if len(got.Errors) != 1 {
		t.Fatalf("errors = %v, want the failing brand's error", got.Errors)
	}

	byMAC := make(map[string]discoveredDevice, len(got.Candidates))
	for _, c := range got.Candidates {
		mac, err := resolver.NormalizeMAC(c.MAC)
		if err != nil {
			t.Fatalf("candidate %+v has an unusable mac", c)
		}
		byMAC[mac] = c
	}

	if configured := byMAC["9877d5a234f2"]; !configured.Configured || configured.DeviceID != "desk" {
		t.Fatalf("configured candidate = %+v; want it matched to the stored device", configured)
	}
	for _, mac := range []string{"d8a011ff5ef0", "d8a011ff5ef1"} {
		if c := byMAC[mac]; c.Configured || c.DeviceID != "" {
			t.Fatalf("candidate %+v was reported as already added", c)
		}
	}
	// A reply this brand has no driver for is listed, never guessed at — but
	// what the hardware called itself is kept, so the row can still say what
	// answered.
	unsupported := byMAC["d8a011ff5ef1"]
	if unsupported.Driver != "" || unsupported.Label != "" {
		t.Fatalf("unsupported candidate = %+v; want no driver and no label", unsupported)
	}
	if unsupported.Model != "ESP10_SOCKET_06" {
		t.Fatalf("unsupported candidate model = %q; want what the device reported", unsupported.Model)
	}
	// A driver this build does have is named, so the UI never shows a key.
	if label := byMAC["d8a011ff5ef0"].Label; label != "Lamp" {
		t.Fatalf("candidate label = %q; want the registered driver label", label)
	}
}

// The end-to-end path the scan exists for: find something, add it, and have it
// become a real device — including the id the server derives for it.
func TestScannedDeviceCanBeAdded(t *testing.T) {
	handler := scanServer(t, nil, &stubScanner{found: []resolver.Candidate{
		{Brand: "test", Driver: "lamp", Model: "A60", Name: "Hall lamp", MAC: "98:77:d5:a2:34:f2"},
	}})

	var found discoveryResponse
	if err := json.NewDecoder(scanRequest(t, handler).Body).Decode(&found); err != nil {
		t.Fatal(err)
	}
	if len(found.Candidates) != 1 || found.Candidates[0].Configured {
		t.Fatalf("candidates = %+v, want one new device", found.Candidates)
	}

	candidate := found.Candidates[0]
	body, err := json.Marshal(config.DeviceSpec{
		Brand:  candidate.Brand,
		Driver: candidate.Driver,
		Model:  candidate.Model,
		Name:   candidate.Name,
		MAC:    candidate.MAC,
	})
	if err != nil {
		t.Fatal(err)
	}
	added := deviceRequest(t, handler, http.MethodPost, "/api/devices", string(body))
	if added.Code != http.StatusCreated {
		t.Fatalf("adding a scanned device: status = %d: %s", added.Code, added.Body.String())
	}
	// What the scan read off the hardware must survive the add: making the user
	// retype a model number Setu was already told is the bug this guards.
	var view manager.DeviceView
	if err := json.NewDecoder(added.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if view.Model != "A60" {
		t.Fatalf("added device = %+v; want the scanned model kept", view)
	}

	// Scanning again must now recognise it as one of the user's own.
	var again discoveryResponse
	if err := json.NewDecoder(scanRequest(t, handler).Body).Decode(&again); err != nil {
		t.Fatal(err)
	}
	if len(again.Candidates) != 1 || !again.Candidates[0].Configured || again.Candidates[0].DeviceID != "test_a234f2" {
		t.Fatalf("rescanned candidate = %+v; want it marked as added", again.Candidates)
	}
}

// Scanning broadcasts on the LAN. A double-tap, or a second open tab, must not
// multiply that traffic.
func TestScanRefusesConcurrentRuns(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	handler := scanServer(t, nil, &stubScanner{entered: entered, blockOn: release})

	done := make(chan int, 1)
	go func() { done <- scanRequest(t, handler).Code }()
	<-entered // the first scan is now inside Scan, holding the lock

	if code := scanRequest(t, handler).Code; code != http.StatusConflict {
		close(release)
		t.Fatalf("concurrent scan status = %d, want 409", code)
	}

	close(release)
	if first := <-done; first != http.StatusOK {
		t.Fatalf("first scan status = %d, want 200", first)
	}
}

// With no brand able to scan, the endpoint must say so plainly rather than
// answering with an empty list the user would read as "nothing on my network".
func TestScanWithoutScannersIsNotImplemented(t *testing.T) {
	if w := scanRequest(t, scanServer(t, nil)); w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501: %s", w.Code, w.Body.String())
	}
}
