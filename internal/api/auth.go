package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// auth wraps a handler with bearer-token authentication, accepting the token
// only in the Authorization header ("Bearer <token>").
//
// Always serve this behind TLS or a trusted tunnel so the token is not exposed
// on the wire (see the README's secure-context note).
func (s *Server) auth(next http.Handler) http.Handler {
	return s.authenticate(next, false)
}

// authQuery additionally accepts the token as a ?token= query parameter. It is
// reserved for /ws, the one client that cannot set an Authorization header (a
// browser WebSocket). Ordinary API routes deliberately do not allow it: a token
// in a URL is copied into access logs, browser history, and Referer headers.
func (s *Server) authQuery(next http.Handler) http.Handler {
	return s.authenticate(next, true)
}

func (s *Server) authenticate(next http.Handler, allowQuery bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" || !tokenOK(r, s.token, allowQuery) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// tokenOK reports whether the request carries the expected bearer token.
func tokenOK(r *http.Request, want string, allowQuery bool) bool {
	got := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		got = strings.TrimPrefix(h, "Bearer ")
	} else if allowQuery {
		got = r.URL.Query().Get("token")
	}
	// Constant-time comparison avoids leaking the token via response timing.
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
