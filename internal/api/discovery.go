package api

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"setu/internal/resolver"
)

// scanBudget caps one whole scan. Brand scanners wait ~1.5–2.5 s for replies and
// run in parallel, so this is headroom, not the expected duration.
const scanBudget = 8 * time.Second

// discoveredDevice is one scan result as the UI needs it: what the device
// reported, plus the one fact only the server can supply — whether this MAC is
// already one of the user's devices.
type discoveredDevice struct {
	resolver.Candidate
	// Configured reports that a device with this MAC is already added;
	// DeviceID then names it. Such a candidate is still shown — seeing your own
	// hardware answer is how you tell a quiet scan from a broken one — but it
	// cannot be added again.
	Configured bool   `json:"configured"`
	DeviceID   string `json:"device_id,omitempty"`
}

// discoveryResponse keeps failures beside results on purpose: one brand's
// scanner failing (no multicast route, a firewall) must not hide the devices the
// others did find.
type discoveryResponse struct {
	Candidates []discoveredDevice `json:"candidates"`
	Errors     []string           `json:"errors,omitempty"`
}

// handleScan asks every registered brand scanner what is on the local network
// and answers with candidates annotated against the devices already added. It
// is read-only: nothing is stored until the user adds a candidate through
// POST /api/devices.
func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	if len(s.scanners) == 0 {
		writeError(w, http.StatusNotImplemented, "no device scanners are registered")
		return
	}
	// Scanning broadcasts on the LAN; one at a time is plenty and keeps a
	// double-tap (or a second tab) from multiplying the traffic.
	if !s.scanMu.TryLock() {
		writeError(w, http.StatusConflict, "a scan is already running")
		return
	}
	defer s.scanMu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), scanBudget)
	defer cancel()

	candidates, errs := s.scan(ctx)
	writeJSON(w, http.StatusOK, discoveryResponse{
		Candidates: s.annotate(candidates),
		Errors:     errs,
	})
}

// scan runs the brand scanners concurrently — they are all waiting on a reply
// window, so in sequence the user would wait for the sum of them.
func (s *Server) scan(ctx context.Context) ([]resolver.Candidate, []string) {
	var (
		mu         sync.Mutex
		candidates []resolver.Candidate
		errs       []string
		wg         sync.WaitGroup
	)
	for _, sc := range s.scanners {
		wg.Add(1)
		go func(sc resolver.Scanner) {
			defer wg.Done()
			found, err := sc.Scan(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				s.log.Warn("device scan failed", "err", err)
				errs = append(errs, err.Error())
				return
			}
			candidates = append(candidates, found...)
		}(sc)
	}
	wg.Wait()

	// Stable output: brand, then model, then MAC. Goroutine completion order is
	// not something the UI should see.
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.Brand != b.Brand {
			return a.Brand < b.Brand
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.MAC < b.MAC
	})
	sort.Strings(errs)
	return candidates, errs
}

// annotate marks the candidates the user has already added, matched by MAC.
func (s *Server) annotate(candidates []resolver.Candidate) []discoveredDevice {
	out := make([]discoveredDevice, 0, len(candidates))
	for _, c := range candidates {
		found := discoveredDevice{Candidate: c}
		if id, ok := s.inventory.Configured(c.Brand, c.MAC); ok {
			found.Configured = true
			found.DeviceID = id
		}
		out = append(out, found)
	}
	return out
}
