package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"sort"
	"strings"

	"setu/internal/users"
)

// Principal is the authenticated caller behind one request: either the
// administrator, who is whoever presents SETU_TOKEN, or one of the users stored
// in the state file.
//
// The administrator deliberately has no stored record. Their token comes from
// the environment, so no API call, no restore, and no corrupt state file can
// lock them out of their own installation.
type Principal struct {
	// Name is what the app shows.
	Name string
	// Admin grants everything, including managing users.
	Admin bool
	// Role is the stored global permission ("read" or "modify"). It is empty for
	// the administrator, whose Admin flag already covers it.
	Role string
	// UserID identifies a stored user; empty for the administrator.
	UserID string
	// devices is the set of device ids a stored user may see. It is nil for the
	// administrator, for whom CanSee is unconditional.
	devices map[string]struct{}
}

// CanSee reports whether this caller may see and control a device.
func (p Principal) CanSee(deviceID string) bool {
	if p.Admin {
		return true
	}
	_, ok := p.devices[deviceID]
	return ok
}

// CanModify reports whether this caller may change what the installation is —
// add devices, write automations — rather than only operate what they were
// given.
func (p Principal) CanModify() bool { return p.Admin || p.Role == users.RoleModify }

// principalKey carries the resolved caller on the request context. It is a
// private type, so nothing outside this package can put a Principal there.
type principalKey struct{}

// principalOf returns the caller a middleware already authenticated. Handlers
// reach one only through those middlewares, so the zero value — which can see
// nothing and modify nothing — is the safe answer if a route is ever mounted
// without auth by mistake.
func principalOf(r *http.Request) Principal {
	principal, _ := r.Context().Value(principalKey{}).(Principal)
	return principal
}

// auth wraps a handler with bearer-token authentication, accepting the token
// only in the Authorization header ("Bearer <token>"). Any authenticated caller
// passes; what they may then do is decided per route and re-checked per device.
//
// Always serve this behind TLS or a trusted tunnel so the token is not exposed
// on the wire (see the README's secure-context note).
func (s *Server) auth(next http.Handler) http.Handler {
	return s.authenticate(next, false, nil)
}

// authQuery additionally accepts the token as a ?token= query parameter. It is
// reserved for /ws, the one client that cannot set an Authorization header (a
// browser WebSocket). Ordinary API routes deliberately do not allow it: a token
// in a URL is copied into access logs, browser history, and Referer headers.
func (s *Server) authQuery(next http.Handler) http.Handler {
	return s.authenticate(next, true, nil)
}

// authModify guards everything that changes the installation rather than the
// devices in it: adding hardware, scanning, and writing automations.
func (s *Server) authModify(next http.Handler) http.Handler {
	return s.authenticate(next, false, func(p Principal) string {
		if p.CanModify() {
			return ""
		}
		return "this account may only control its devices"
	})
}

// authAdmin guards what only the holder of SETU_TOKEN may do: managing users,
// and the whole-installation export/restore that necessarily reaches past any
// one person's device access.
func (s *Server) authAdmin(next http.Handler) http.Handler {
	return s.authenticate(next, false, func(p Principal) string {
		if p.Admin {
			return ""
		}
		return "only the administrator may do this"
	})
}

// authenticate resolves the caller, applies an optional per-route permission
// check, and hands the Principal to the handler on the request context.
//
// deny returns the reason this caller may not use the route, or "" to let it
// through. Every such refusal is a 403: the token was accepted, so the question
// is only what it is allowed to do.
func (s *Server) authenticate(next http.Handler, allowQuery bool, deny func(Principal) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := s.resolve(r, allowQuery)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if deny != nil {
			if reason := deny(principal); reason != "" {
				writeError(w, http.StatusForbidden, reason)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
	})
}

// resolve turns a request's bearer token into a Principal. The administrator's
// token is compared in constant time; stored users are consulted only when the
// presented token is not the administrator's.
func (s *Server) resolve(r *http.Request, allowQuery bool) (Principal, bool) {
	token := bearerToken(r, allowQuery)
	if token == "" {
		return Principal{}, false
	}
	// Constant-time comparison avoids leaking the token via response timing.
	if s.token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1 {
		return Principal{Name: "Administrator", Admin: true}, true
	}
	if s.users == nil {
		return Principal{}, false
	}
	user, ok := s.users.Authenticate(token)
	if !ok {
		return Principal{}, false
	}
	granted := make(map[string]struct{}, len(user.Devices))
	for _, id := range user.Devices {
		granted[id] = struct{}{}
	}
	return Principal{Name: user.Name, Role: user.Role, UserID: user.ID, devices: granted}, true
}

// bearerToken extracts the presented token from the request. The scheme is
// matched case-insensitively, as RFC 7235 requires; the token itself is not.
func bearerToken(r *http.Request, allowQuery bool) string {
	const scheme = "Bearer "
	header := r.Header.Get("Authorization")
	if len(header) > len(scheme) && strings.EqualFold(header[:len(scheme)], scheme) {
		return header[len(scheme):]
	}
	if allowQuery {
		return r.URL.Query().Get("token")
	}
	return ""
}

// sessionResponse tells the app which account it is signed in as, so it can hide
// what that account cannot do instead of offering actions the server refuses.
//
// It is advisory. Every restriction it describes is enforced again on each
// request; a client that ignores it simply collects 403s.
type sessionResponse struct {
	Name string `json:"name"`
	// Admin is the only flag that unlocks user management in the app.
	Admin bool `json:"admin"`
	// Role is "modify" for the administrator too, so the app has one field to
	// reason about rather than two.
	Role string `json:"role"`
	// AllDevices reports that this account is not limited to a device list.
	AllDevices bool     `json:"all_devices"`
	Devices    []string `json:"devices"`
	// Users reports whether this build can manage users at all, so the app does
	// not offer a screen whose endpoints are not mounted.
	Users bool `json:"users"`
}

// handleSession answers "who am I, and what may I do?".
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	principal := principalOf(r)
	response := sessionResponse{
		Name:       principal.Name,
		Admin:      principal.Admin,
		Role:       users.RoleModify,
		AllDevices: principal.Admin,
		Devices:    []string{},
		Users:      s.users != nil,
	}
	if !principal.Admin {
		response.Role = principal.Role
		for id := range principal.devices {
			response.Devices = append(response.Devices, id)
		}
		// Map iteration order is deliberately random; the app diffs this list, so
		// give it a stable one.
		sort.Strings(response.Devices)
	}
	writeJSON(w, http.StatusOK, response)
}
