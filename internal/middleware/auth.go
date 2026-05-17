package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// JWTAuth returns a middleware that validates a Bearer JWT on every request
// whose path does not start with a prefix in skipPrefixes. On success it
// injects X-Auth-Subject and X-Auth-Roles headers so upstream services can
// trust the caller identity without re-verifying the token.
//
// Only HS256 (HMAC-SHA256) tokens are accepted. This matches the auth-service
// default; set AUTH_JWT_SECRET in both services to the same value.
//
// When secret is empty the middleware is a no-op - all requests pass through.
// This lets local development work without configuring JWT validation.
func JWTAuth(secret, issuer string, skipPrefixes []string) func(http.Handler) http.Handler {
	if secret == "" {
		return func(next http.Handler) http.Handler { return next }
	}
	key := []byte(secret)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, p := range skipPrefixes {
				if strings.HasPrefix(r.URL.Path, p) {
					next.ServeHTTP(w, r)
					return
				}
			}

			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				jwtDeny(w, "missing or malformed Authorization header")
				return
			}
			token := authHeader[len("Bearer "):]

			subject, roles, err := verifyHS256(token, key, issuer)
			if err != nil {
				jwtDeny(w, err.Error())
				return
			}

			// Forward identity headers to upstream services.
			r.Header.Set("X-Auth-Subject", subject)
			if len(roles) > 0 {
				r.Header.Set("X-Auth-Roles", strings.Join(roles, ","))
			}

			next.ServeHTTP(w, r)
		})
	}
}

// verifyHS256 parses and verifies a HS256 JWT signed with key.
// Returns the "sub" and "roles" claims on success.
func verifyHS256(token string, key []byte, expectedIssuer string) (string, []string, error) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return "", nil, fmt.Errorf("invalid token structure")
	}

	// Verify HMAC-SHA256 signature over "header.payload".
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	want := mac.Sum(nil)

	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(want, got) {
		return "", nil, fmt.Errorf("invalid token signature")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", nil, fmt.Errorf("malformed token payload")
	}

	var claims struct {
		Sub   string   `json:"sub"`
		Exp   int64    `json:"exp"`
		Iss   string   `json:"iss"`
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return "", nil, fmt.Errorf("malformed token payload")
	}

	if time.Now().Unix() > claims.Exp {
		return "", nil, fmt.Errorf("token expired")
	}

	if expectedIssuer != "" && claims.Iss != expectedIssuer {
		return "", nil, fmt.Errorf("token issuer mismatch")
	}

	return claims.Sub, claims.Roles, nil
}

func jwtDeny(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":  "unauthorized",
		"reason": reason,
	})
}
