package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// auth wraps a handler with bearer-token authentication. The token is accepted
// either in the Authorization header ("Bearer <token>") or, for clients that
// cannot set headers (browser WebSocket), in the ?token= query parameter.
//
// Always serve this behind TLS or a trusted tunnel so the token is not exposed
// on the wire (see the README's secure-context note).
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" || !tokenOK(r, s.token) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// tokenOK reports whether the request carries the expected bearer token.
func tokenOK(r *http.Request, want string) bool {
	got := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		got = strings.TrimPrefix(h, "Bearer ")
	} else if q := r.URL.Query().Get("token"); q != "" {
		got = q
	}
	// Constant-time comparison avoids leaking the token via response timing.
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
