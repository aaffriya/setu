// Package api exposes Setu over HTTP: a JSON API under /api, a WebSocket at /ws
// for live state events, and the embedded Svelte frontend at /. It is the
// "front-end protocol" layer; device code knows nothing about it.
//
// A second front-end protocol (e.g. an Apple HomeKit bridge) would be added
// alongside this package and would talk to the same manager + event bus — that
// is the bridge/transport seam the constraints call for, reachable without
// touching any device code.
package api

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"

	"setu/internal/automation"
	"setu/internal/events"
	"setu/internal/inventory"
	"setu/internal/manager"
	"setu/internal/resolver"
	"setu/internal/users"
)

// Server wires the manager and event bus to HTTP handlers.
type Server struct {
	mgr        *manager.Manager
	poller     *manager.Poller
	bus        *events.Bus
	automation *automation.Engine
	inventory  *inventory.Inventory
	users      *users.Registry
	scanners   []resolver.Scanner
	scanMu     sync.Mutex // one LAN scan at a time (see discovery.go)
	commands   *limiter   // per-caller, per-device command budget (see ratelimit.go)
	token      string
	dist       fs.FS // embedded frontend, rooted at the dist dir
	log        *slog.Logger
}

// Options configures a Server.
type Options struct {
	Manager    *manager.Manager
	Poller     *manager.Poller
	Bus        *events.Bus
	Automation *automation.Engine
	// Inventory owns the stored device list. Without one the device-management
	// endpoints are not mounted (the manager still serves what it holds).
	Inventory *inventory.Inventory
	// Users owns the accounts that exist besides the administrator. Without one
	// the user endpoints are not mounted and Token is the only way in.
	Users *users.Registry
	// Scanners list the brands that can enumerate their devices on the LAN, in
	// the order the composition root registered them. Empty = no scan endpoint.
	Scanners []resolver.Scanner
	// Token is the administrator's bearer token, from the environment.
	Token  string
	Dist   fs.FS
	Logger *slog.Logger
}

// emptyFS stands in for an absent frontend. Serving from a nil fs.FS would
// dereference nil on the first static request; with this the same paths fall
// through to the built-in placeholder, which is how a Setu binary whose embed
// holds no index.html already behaves (see static.go).
type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }

// New returns a Server.
func New(o Options) *Server {
	dist := o.Dist
	if dist == nil {
		dist = emptyFS{}
	}
	return &Server{
		mgr:        o.Manager,
		poller:     o.Poller,
		bus:        o.Bus,
		automation: o.Automation,
		inventory:  o.Inventory,
		users:      o.Users,
		scanners:   o.Scanners,
		commands:   newLimiter(),
		token:      o.Token,
		dist:       dist,
		log:        o.Logger,
	}
}

// Handler builds the http.Handler with all routes mounted. The JSON API and the
// WebSocket require the bearer token; the static app shell is public (it is just
// the client; all data flows through the protected endpoints).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public, self-contained escape hatch for a broken service-worker cache. The
	// worker deliberately bypasses /api, so this remains reachable even when the
	// normal cached app shell cannot navigate. It clears no token/preferences.
	mux.HandleFunc("GET /api/recover", s.handleAppRecovery)

	// Public liveness probe for systemd, Docker and uptime checks. It answers
	// only "this process is serving HTTP" and deliberately discloses nothing
	// about the installation, so it needs no token (see handlers.go).
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("HEAD /healthz", s.handleHealth)

	// JSON API (token-protected). Go 1.22+ method+pattern routing.
	//
	// Three levels guard these routes: auth accepts any account, authModify adds
	// "may change the installation", and authAdmin means the SETU_TOKEN holder
	// only. Routes naming a device additionally check that this account was
	// granted it — the middleware knows the account, not the device.
	mux.Handle("GET /api/session", s.auth(http.HandlerFunc(s.handleSession)))
	mux.Handle("GET /api/devices", s.auth(http.HandlerFunc(s.handleListDevices)))
	mux.Handle("GET /api/diagnostics", s.auth(http.HandlerFunc(s.handleDiagnostics)))
	mux.Handle("POST /api/activity", s.auth(http.HandlerFunc(s.handleActivity)))
	mux.Handle("POST /api/devices/{id}/command", s.auth(http.HandlerFunc(s.handleCommand)))
	mux.Handle("POST /api/devices/{id}/refresh", s.auth(http.HandlerFunc(s.handleRefreshDevice)))
	if s.inventory != nil {
		// Managing which devices exist: added from a scan or by hand, renamed,
		// removed, and exported/replaced as one list for backup and restore.
		mux.Handle("GET /api/device-types", s.auth(http.HandlerFunc(s.handleDeviceTypes)))
		mux.Handle("POST /api/devices", s.authModify(http.HandlerFunc(s.handleAddDevice)))
		mux.Handle("PATCH /api/devices/{id}", s.authModify(http.HandlerFunc(s.handleUpdateDevice)))
		mux.Handle("DELETE /api/devices/{id}", s.authModify(http.HandlerFunc(s.handleDeleteDevice)))
		// Export and restore are whole-installation operations: they read and
		// rewrite devices no single user may be able to see, so they stay with
		// the administrator even when other accounts may add hardware.
		mux.Handle("GET /api/devices/export", s.authAdmin(http.HandlerFunc(s.handleExportDevices)))
		mux.Handle("PUT /api/devices", s.authAdmin(http.HandlerFunc(s.handleReplaceDevices)))
		// A scan actively broadcasts on the LAN, so it is a POST: never
		// prefetched, never cached, never replayed by a bored browser.
		mux.Handle("POST /api/discovery/scan", s.authModify(http.HandlerFunc(s.handleScan)))
	}
	if s.users != nil {
		mux.Handle("GET /api/users", s.authAdmin(http.HandlerFunc(s.handleListUsers)))
		mux.Handle("POST /api/users", s.authAdmin(http.HandlerFunc(s.handleCreateUser)))
		mux.Handle("PATCH /api/users/{id}", s.authAdmin(http.HandlerFunc(s.handleUpdateUser)))
		mux.Handle("DELETE /api/users/{id}", s.authAdmin(http.HandlerFunc(s.handleDeleteUser)))
		mux.Handle("POST /api/users/{id}/token", s.authAdmin(http.HandlerFunc(s.handleRotateUserToken)))
	}
	if s.automation != nil {
		mux.Handle("GET /api/automations", s.auth(http.HandlerFunc(s.handleAutomations)))
		mux.Handle("PUT /api/automations", s.authModify(http.HandlerFunc(s.handleReplaceAutomations)))
		mux.Handle("GET /api/automations/export", s.authAdmin(http.HandlerFunc(s.handleAutomationExport)))
		mux.Handle("POST /api/automations/{id}/run", s.auth(http.HandlerFunc(s.handleRunAutomation)))
		mux.Handle("POST /api/automations/{id}/token", s.authModify(http.HandlerFunc(s.handleRotateWebhook)))
		mux.HandleFunc("POST /api/automation-hooks/{id}", s.handleAutomationWebhook)
	}

	// WebSocket (token-protected; token may also be passed as ?token= for
	// browsers, which cannot set an Authorization header on a WebSocket).
	mux.Handle("GET /ws", s.authQuery(http.HandlerFunc(s.handleWS)))

	// Any other /api path is a mistake, not a page: answer it as the API rather
	// than letting the SPA fallback below return index.html with a 200, which
	// surfaces to callers as an unparseable JSON body. More specific patterns
	// above still win, so this only catches genuinely unrouted paths — including
	// the automation routes when no engine is configured.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "unknown endpoint")
	})

	// Everything else → the embedded SPA (public).
	mux.Handle("/", s.staticHandler())

	return mux
}

// writeJSON writes v as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// publishInventoryChanged carries no inventory data. Each socket re-resolves
// its own token and each browser re-reads its permission-filtered views, so one
// broadcast stays small and cannot disclose another account's device names.
func (s *Server) publishInventoryChanged() {
	s.bus.Publish(events.Event{Type: events.InventoryChanged})
}

// publishDeviceInventoryChanged adds the authoritative membership only to the
// in-process event. WebSocket clients still receive the same metadata-free
// invalidation, while the automation engine can forget clocks for removed
// devices without racing a quick remove-and-re-add of the same MAC-derived id.
func (s *Server) publishDeviceInventoryChanged(reset bool) {
	specs := s.inventory.Specs()
	ids := make([]string, 0, len(specs))
	for _, spec := range specs {
		ids = append(ids, spec.ID)
	}
	s.bus.Publish(events.Event{Type: events.InventoryChanged, DeviceIDs: ids, ResetDevices: reset})
}

type errorResponse struct {
	Error  string              `json:"error"`
	Device *manager.DeviceView `json:"device,omitempty"`
}

// writeError writes a clean JSON error body.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// writeDeviceError includes an authoritative state obtained after an ambiguous
// transport failure. Clients can reconcile without a second all-device poll.
func writeDeviceError(w http.ResponseWriter, status int, msg string, view manager.DeviceView) {
	writeJSON(w, status, errorResponse{Error: msg, Device: &view})
}
