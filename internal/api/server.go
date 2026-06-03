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

	"setu/internal/events"
	"setu/internal/manager"
)

// Server wires the manager and event bus to HTTP handlers.
type Server struct {
	mgr   *manager.Manager
	bus   *events.Bus
	token string
	dist  fs.FS // embedded frontend, rooted at the dist dir
	log   *slog.Logger
}

// Options configures a Server.
type Options struct {
	Manager *manager.Manager
	Bus     *events.Bus
	Token   string
	Dist    fs.FS
	Logger  *slog.Logger
}

// New returns a Server.
func New(o Options) *Server {
	return &Server{
		mgr:   o.Manager,
		bus:   o.Bus,
		token: o.Token,
		dist:  o.Dist,
		log:   o.Logger,
	}
}

// Handler builds the http.Handler with all routes mounted. The JSON API and the
// WebSocket require the bearer token; the static app shell is public (it is just
// the client; all data flows through the protected endpoints).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// JSON API (token-protected). Go 1.22+ method+pattern routing.
	mux.Handle("GET /api/devices", s.auth(http.HandlerFunc(s.handleListDevices)))
	mux.Handle("POST /api/devices/{id}/command", s.auth(http.HandlerFunc(s.handleCommand)))

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
