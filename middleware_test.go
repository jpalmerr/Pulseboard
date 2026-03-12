package pulseboard

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

// okHandler is a simple handler that always responds 200 OK.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// --- BasicAuth tests ---

func TestBasicAuth_ValidCredentials(t *testing.T) {
	mw := BasicAuth(func(u, p string) bool {
		return u == "admin" && p == "secret"
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:secret")))
	rec := httptest.NewRecorder()

	mw(okHandler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestBasicAuth_InvalidCredentials(t *testing.T) {
	mw := BasicAuth(func(u, p string) bool {
		return u == "admin" && p == "secret"
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:wrong")))
	rec := httptest.NewRecorder()

	mw(okHandler).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("expected WWW-Authenticate header in 401 response")
	}
}

func TestBasicAuth_MissingAuthorizationHeader(t *testing.T) {
	mw := BasicAuth(func(u, p string) bool {
		return true
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	mw(okHandler).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("expected WWW-Authenticate header in 401 response")
	}
}

func TestBasicAuth_WWWAuthenticateHeaderValue(t *testing.T) {
	mw := BasicAuth(func(u, p string) bool { return false })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	mw(okHandler).ServeHTTP(rec, req)

	got := rec.Header().Get("WWW-Authenticate")
	want := `Basic realm="PulseBoard"`
	if got != want {
		t.Errorf("WWW-Authenticate = %q, want %q", got, want)
	}
}

// --- BearerToken tests ---

func TestBearerToken_ValidToken(t *testing.T) {
	mw := BearerToken("valid-token")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()

	mw(okHandler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestBearerToken_InvalidToken(t *testing.T) {
	mw := BearerToken("valid-token")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()

	mw(okHandler).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("expected WWW-Authenticate header in 401 response")
	}
}

func TestBearerToken_MissingAuthorizationHeader(t *testing.T) {
	mw := BearerToken("valid-token")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	mw(okHandler).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestBearerToken_MalformedHeader_NoPrefix(t *testing.T) {
	mw := BearerToken("valid-token")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "valid-token") // missing "Bearer " prefix
	rec := httptest.NewRecorder()

	mw(okHandler).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestBearerToken_MalformedHeader_WrongScheme(t *testing.T) {
	mw := BearerToken("valid-token")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic valid-token") // wrong scheme
	rec := httptest.NewRecorder()

	mw(okHandler).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestBearerToken_EmptyTokenList_NoOp(t *testing.T) {
	mw := BearerToken() // no tokens = no-op

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// no Authorization header
	rec := httptest.NewRecorder()

	mw(okHandler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 (no-op), got %d", rec.Code)
	}
}

func TestBearerToken_MultipleValidTokens(t *testing.T) {
	mw := BearerToken("token-a", "token-b", "token-c")

	for _, tok := range []string{"token-a", "token-b", "token-c"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()

		mw(okHandler).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("token %q: expected 200, got %d", tok, rec.Code)
		}
	}
}

func TestBearerToken_WWWAuthenticateHeaderValue(t *testing.T) {
	mw := BearerToken("tok")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	mw(okHandler).ServeHTTP(rec, req)

	got := rec.Header().Get("WWW-Authenticate")
	want := `Bearer realm="PulseBoard"`
	if got != want {
		t.Errorf("WWW-Authenticate = %q, want %q", got, want)
	}
}

// --- Middleware chain ordering tests ---

func TestWithMiddleware_ChainOrdering_FirstAddedIsOutermost(t *testing.T) {
	// record the order in which middleware is entered
	var order []string

	makeMiddleware := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	cfg := &pbConfig{}
	_ = WithMiddleware(makeMiddleware("first"))(cfg)
	_ = WithMiddleware(makeMiddleware("second"))(cfg)
	_ = WithMiddleware(makeMiddleware("third"))(cfg)

	// simulate what server.Start does: reverse-iterate to build chain
	var handler http.Handler = okHandler
	for i := len(cfg.middleware) - 1; i >= 0; i-- {
		handler = cfg.middleware[i](handler)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if len(order) != 3 {
		t.Fatalf("expected 3 middleware invocations, got %d", len(order))
	}
	if order[0] != "first" || order[1] != "second" || order[2] != "third" {
		t.Errorf("middleware order = %v, want [first second third]", order)
	}
}

// --- WithMiddleware option validation ---

func TestWithMiddleware_NilMiddlewareReturnsError(t *testing.T) {
	cfg := &pbConfig{}
	err := WithMiddleware(nil)(cfg)
	if err == nil {
		t.Error("expected error when passing nil middleware, got nil")
	}
}

func TestWithMiddleware_CanAddMultiple(t *testing.T) {
	cfg := &pbConfig{}
	_ = WithMiddleware(func(next http.Handler) http.Handler { return next })(cfg)
	_ = WithMiddleware(func(next http.Handler) http.Handler { return next })(cfg)

	if len(cfg.middleware) != 2 {
		t.Errorf("expected 2 middleware, got %d", len(cfg.middleware))
	}
}
