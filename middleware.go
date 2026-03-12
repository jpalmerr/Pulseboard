package pulseboard

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// BasicAuth returns middleware that requires HTTP Basic Authentication.
//
// The validate function is called with the username and password from the
// Authorization header. Return true to allow the request, false to reject it.
//
// Rejected requests receive a 401 Unauthorized response with a
// WWW-Authenticate header prompting the client to supply credentials.
//
// Example:
//
//	mw := pulseboard.BasicAuth(func(u, p string) bool {
//	    return u == "admin" && p == os.Getenv("DASHBOARD_PASSWORD")
//	})
func BasicAuth(validate func(username, password string) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok || !validate(user, pass) {
				w.Header().Set("WWW-Authenticate", `Basic realm="PulseBoard"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// BearerToken returns middleware that requires a valid Bearer token.
//
// The Authorization header must contain "Bearer <token>" where <token> is one
// of the provided validTokens. The scheme prefix check is case-insensitive per
// RFC 7235. Token comparison uses constant-time equality to prevent timing
// side-channels; all tokens are always compared regardless of early match.
//
// Rejected requests receive a 401 Unauthorized response with a
// WWW-Authenticate header.
//
// If no tokens are provided, the middleware is a no-op and all requests pass
// through without authentication.
//
// Example:
//
//	mw := pulseboard.BearerToken("my-secret-token", "another-valid-token")
func BearerToken(validTokens ...string) func(http.Handler) http.Handler {
	if len(validTokens) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	// store as slice so we always iterate all tokens (constant-time behaviour)
	tokens := make([][]byte, len(validTokens))
	for i, t := range validTokens {
		tokens[i] = []byte(t)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if len(auth) <= len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="PulseBoard"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			token := []byte(auth[len(prefix):])
			// Always compare all tokens to avoid leaking which token matched
			// via a timing side-channel.
			var matched int
			for _, valid := range tokens {
				matched |= subtle.ConstantTimeCompare(token, valid)
			}
			if matched != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="PulseBoard"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
