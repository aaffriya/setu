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

	"setu/internal/automation"
	"setu/internal/events"
	"setu/internal/manager"
)

// Server wires the manager and event bus to HTTP handlers.
type Server struct {
	mgr        *manager.Manager
	poller     *manager.Poller
	bus        *events.Bus
	automation *automation.Engine
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
	Token      string
	Dist       fs.FS
	Logger     *slog.Logger
}

// New returns a Server.
func New(o Options) *Server {
	return &Server{
		mgr:        o.Manager,
		poller:     o.Poller,
		bus:        o.Bus,
		automation: o.Automation,
		token:      o.Token,
		dist:       o.Dist,
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

	// JSON API (token-protected). Go 1.22+ method+pattern routing.
	mux.Handle("GET /api/devices", s.auth(http.HandlerFunc(s.handleListDevices)))
	mux.Handle("POST /api/activity", s.auth(http.HandlerFunc(s.handleActivity)))
	mux.Handle("POST /api/devices/{id}/command", s.auth(http.HandlerFunc(s.handleCommand)))
	if s.automation != nil {
		mux.Handle("GET /api/automations", s.auth(http.HandlerFunc(s.handleAutomations)))
		mux.Handle("PUT /api/automations", s.auth(http.HandlerFunc(s.handleReplaceAutomations)))
		mux.Handle("GET /api/automations/export", s.auth(http.HandlerFunc(s.handleAutomationExport)))
		mux.Handle("POST /api/automations/{id}/run", s.auth(http.HandlerFunc(s.handleRunAutomation)))
		mux.Handle("POST /api/automations/{id}/token", s.auth(http.HandlerFunc(s.handleRotateWebhook)))
		mux.HandleFunc("POST /api/automation-hooks/{id}", s.handleAutomationWebhook)
	}

	// WebSocket (token-protected; token may also be passed as ?token= for
	// browsers, which cannot set an Authorization header on a WebSocket).
	mux.Handle("GET /ws", s.auth(http.HandlerFunc(s.handleWS)))

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

type errorResponse struct {
	Error string `json:"error"`
}

// writeError writes a clean JSON error body.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}
