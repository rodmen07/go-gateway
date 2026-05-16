package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// nopHandler returns a 200 OK with no body.
var nopHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func newRequest(path, remoteAddr string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = remoteAddr + ":1234"
	return r
}

func TestRateLimiter_HeadersSetOnEveryResponse(t *testing.T) {
	mw := RateLimiter(100, nil)
	handler := mw(nopHandler)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newRequest("/api/accounts/1", "1.2.3.4"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	for _, h := range []string{"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"} {
		if w.Header().Get(h) == "" {
			t.Errorf("expected header %s to be set", h)
		}
	}
}

func TestRateLimiter_LimitHeaderMatchesConfiguredRPS(t *testing.T) {
	mw := RateLimiter(42, nil)
	handler := mw(nopHandler)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newRequest("/api/search", "1.2.3.4"))

	got, err := strconv.Atoi(w.Header().Get("X-RateLimit-Limit"))
	if err != nil {
		t.Fatalf("X-RateLimit-Limit not a number: %v", err)
	}
	if got != 42 {
		t.Errorf("expected X-RateLimit-Limit=42, got %d", got)
	}
}

func TestRateLimiter_429WhenBurstExceeded(t *testing.T) {
	// 1 rps, burst=2 — three rapid requests should trigger 429
	mw := RateLimiter(1, nil)
	handler := mw(nopHandler)

	var last *httptest.ResponseRecorder
	for i := 0; i < 5; i++ {
		last = httptest.NewRecorder()
		handler.ServeHTTP(last, newRequest("/api/accounts", "10.0.0.1"))
	}

	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after burst exceeded, got %d", last.Code)
	}
	if last.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429 response")
	}
	var body map[string]string
	if err := json.NewDecoder(last.Body).Decode(&body); err != nil {
		t.Fatalf("could not decode 429 body: %v", err)
	}
	if body["code"] != "RATE_LIMITED" {
		t.Errorf("expected code=RATE_LIMITED, got %q", body["code"])
	}
}

func TestRateLimiter_PerClientIsolation(t *testing.T) {
	// 1 rps — exhaust clientA's bucket; clientB should still pass
	mw := RateLimiter(1, nil)
	handler := mw(nopHandler)

	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, newRequest("/api/contacts", "192.168.1.1"))
	}

	// clientB — fresh bucket, should succeed
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newRequest("/api/contacts", "192.168.1.2"))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for isolated client, got %d", w.Code)
	}
}

func TestRateLimiter_RouteTiersApplied(t *testing.T) {
	routeLimits := map[string]float64{
		"/api/auth": 2,
	}
	mw := RateLimiter(100, routeLimits)
	handler := mw(nopHandler)

	// Auth route: limit=2, burst=4 — exhaust it
	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, newRequest("/api/auth/token", "5.5.5.5"))
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newRequest("/api/auth/token", "5.5.5.5"))
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 on auth route after exhaustion, got %d", w.Code)
	}

	// Non-auth route for same IP: different bucket, should not be affected
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, newRequest("/api/accounts/1", "5.5.5.5"))
	if w2.Code != http.StatusOK {
		t.Errorf("expected 200 for non-auth route unaffected by auth exhaustion, got %d", w2.Code)
	}
}

func TestRateLimiter_XForwardedForUsedAsClientIP(t *testing.T) {
	// 1 rps — exhaust for a forwarded IP; direct IP should be unaffected
	mw := RateLimiter(1, nil)
	handler := mw(nopHandler)

	for i := 0; i < 5; i++ {
		r := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
		r.Header.Set("X-Forwarded-For", "203.0.113.1")
		r.RemoteAddr = "10.0.0.1:9999"
		handler.ServeHTTP(httptest.NewRecorder(), r)
	}

	// Same RemoteAddr but different forwarded IP — different bucket
	r := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.2")
	r.RemoteAddr = "10.0.0.1:9999"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for different forwarded IP, got %d", w.Code)
	}
}

func TestExtractIP_XForwardedFor(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.1")
	r.RemoteAddr = "127.0.0.1:8080"
	if got := extractIP(r); got != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %q", got)
	}
}

func TestExtractIP_RemoteAddr(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.168.1.5:4321"
	if got := extractIP(r); got != "192.168.1.5" {
		t.Errorf("expected 192.168.1.5, got %q", got)
	}
}

func TestRouteKey(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/api/accounts/123", "/api/accounts"},
		{"/api/auth/token/refresh", "/api/auth"},
		{"/api/search", "/api/search"},
		{"/health", "/health"},
	}
	for _, c := range cases {
		if got := routeKey(c.path); got != c.want {
			t.Errorf("routeKey(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
