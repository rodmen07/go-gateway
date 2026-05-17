package middleware_test

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rodmen07/go-gateway/internal/middleware"
)

// makeHS256Token crafts a signed HS256 JWT with the supplied claims.
func makeHS256Token(secret, subject, issuer string, roles []string, exp int64) string {
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload, _ := json.Marshal(map[string]any{
		"sub":   subject,
		"iss":   issuer,
		"exp":   exp,
		"roles": roles,
	})

	h64 := base64.RawURLEncoding.EncodeToString(header)
	p64 := base64.RawURLEncoding.EncodeToString(payload)
	sigInput := h64 + "." + p64

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(sigInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return sigInput + "." + sig
}

// makeRS256Token crafts a signed RS256 JWT with the supplied claims.
func makeRS256Token(privKey *rsa.PrivateKey, subject, issuer string, roles []string, exp int64) string {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	payload, _ := json.Marshal(map[string]any{
		"sub":   subject,
		"iss":   issuer,
		"exp":   exp,
		"roles": roles,
	})

	h64 := base64.RawURLEncoding.EncodeToString(header)
	p64 := base64.RawURLEncoding.EncodeToString(payload)
	sigInput := h64 + "." + p64

	h := sha256.New()
	h.Write([]byte(sigInput))
	digest := h.Sum(nil)

	sig, _ := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, digest)
	return sigInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// captureHandler returns an http.HandlerFunc that records the X-Auth-Subject
// and X-Auth-Roles headers from the request it receives.
func captureHandler(subject, roles *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*subject = r.Header.Get("X-Auth-Subject")
		*roles = r.Header.Get("X-Auth-Roles")
		w.WriteHeader(http.StatusOK)
	}
}

// ─── HS256 tests ─────────────────────────────────────────────────────────────

func TestJWTAuth_HS256_ValidToken(t *testing.T) {
	const secret = "test-secret-long-enough-32chars!!"
	const subject = "user-123"
	const issuer = "auth-service"
	token := makeHS256Token(secret, subject, issuer, []string{"admin", "viewer"}, time.Now().Add(time.Hour).Unix())

	var gotSubject, gotRoles string
	handler := middleware.JWTAuth(secret, nil, issuer, nil)(captureHandler(&gotSubject, &gotRoles))

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if gotSubject != subject {
		t.Errorf("X-Auth-Subject: want %q, got %q", subject, gotSubject)
	}
	if gotRoles != "admin,viewer" {
		t.Errorf("X-Auth-Roles: want %q, got %q", "admin,viewer", gotRoles)
	}
}

func TestJWTAuth_HS256_ExpiredToken(t *testing.T) {
	const secret = "test-secret-long-enough-32chars!!"
	token := makeHS256Token(secret, "user", "auth-service", nil, time.Now().Add(-time.Hour).Unix())

	handler := middleware.JWTAuth(secret, nil, "auth-service", nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "token expired") {
		t.Errorf("expected 'token expired' in body, got: %s", rr.Body.String())
	}
}

func TestJWTAuth_HS256_WrongSignature(t *testing.T) {
	const secret = "test-secret-long-enough-32chars!!"
	// Sign with a different secret.
	token := makeHS256Token("wrong-secret-long-enough-32chars!", "user", "auth-service", nil, time.Now().Add(time.Hour).Unix())

	handler := middleware.JWTAuth(secret, nil, "auth-service", nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid token signature") {
		t.Errorf("expected 'invalid token signature' in body, got: %s", rr.Body.String())
	}
}

func TestJWTAuth_HS256_WrongIssuer(t *testing.T) {
	const secret = "test-secret-long-enough-32chars!!"
	token := makeHS256Token(secret, "user", "rogue-issuer", nil, time.Now().Add(time.Hour).Unix())

	handler := middleware.JWTAuth(secret, nil, "auth-service", nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "token issuer mismatch") {
		t.Errorf("expected 'token issuer mismatch' in body, got: %s", rr.Body.String())
	}
}

func TestJWTAuth_MissingAuthorizationHeader(t *testing.T) {
	const secret = "test-secret-long-enough-32chars!!"
	handler := middleware.JWTAuth(secret, nil, "auth-service", nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestJWTAuth_MalformedBearerToken(t *testing.T) {
	const secret = "test-secret-long-enough-32chars!!"
	handler := middleware.JWTAuth(secret, nil, "auth-service", nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer not.a.valid.jwt.token.at.all")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// ─── Skip-prefix tests ───────────────────────────────────────────────────────

func TestJWTAuth_SkipHealthPrefix(t *testing.T) {
	const secret = "test-secret-long-enough-32chars!!"
	handler := middleware.JWTAuth(secret, nil, "auth-service", []string{"/health", "/api/auth"})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	// No Authorization header — should still succeed for skipped paths.
	for _, path := range []string{"/health", "/api/auth/login"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("path %q: expected 200, got %d", path, rr.Code)
		}
	}
}

func TestJWTAuth_SkipPrefix_NonSkippedPathStillRequiresToken(t *testing.T) {
	const secret = "test-secret-long-enough-32chars!!"
	handler := middleware.JWTAuth(secret, nil, "auth-service", []string{"/health"})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// ─── No-op mode ───────────────────────────────────────────────────────────────

func TestJWTAuth_EmptySecret_NoOp(t *testing.T) {
	// Empty secret and nil pubKey — all requests pass through.
	handler := middleware.JWTAuth("", nil, "auth-service", nil)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (no-op mode should pass all requests)", rr.Code)
	}
}

// ─── RS256 tests ──────────────────────────────────────────────────────────────

func TestJWTAuth_RS256_ValidToken(t *testing.T) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	pubKey := &privKey.PublicKey

	const subject = "user-rs256"
	const issuer = "auth-service"
	token := makeRS256Token(privKey, subject, issuer, []string{"viewer"}, time.Now().Add(time.Hour).Unix())

	var gotSubject, gotRoles string
	handler := middleware.JWTAuth("", pubKey, issuer, nil)(captureHandler(&gotSubject, &gotRoles))

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	if gotSubject != subject {
		t.Errorf("X-Auth-Subject: want %q, got %q", subject, gotSubject)
	}
	if gotRoles != "viewer" {
		t.Errorf("X-Auth-Roles: want %q, got %q", "viewer", gotRoles)
	}
}

func TestJWTAuth_RS256_WrongKey(t *testing.T) {
	signingKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate signing key: %v", err)
	}
	verifyKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate verify key: %v", err)
	}

	token := makeRS256Token(signingKey, "user", "auth-service", nil, time.Now().Add(time.Hour).Unix())

	handler := middleware.JWTAuth("", &verifyKey.PublicKey, "auth-service", nil)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid token signature") {
		t.Errorf("expected 'invalid token signature' in body, got: %s", rr.Body.String())
	}
}

func TestJWTAuth_RS256_KeyMismatch_HS256TokenRejected(t *testing.T) {
	// RS256 public key configured but HS256 token presented without secret.
	privKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	hs256Token := makeHS256Token("some-secret", "user", "auth-service", nil, time.Now().Add(time.Hour).Unix())

	handler := middleware.JWTAuth("", &privKey.PublicKey, "auth-service", nil)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+hs256Token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// HS256 token presented when only RS256 key is configured → 401
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}
