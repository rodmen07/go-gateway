package middleware

import (
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
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
// HS256 tokens are verified with secret (HMAC-SHA256). RS256 tokens are
// verified with pubKey (RSA-PKCS1v15-SHA256). The algorithm is read from the
// JWT header and must match the configured key type.
//
// When both secret is empty and pubKey is nil the middleware is a no-op -
// all requests pass through. This allows local development without JWT setup.
func JWTAuth(secret string, pubKey *rsa.PublicKey, issuer string, skipPrefixes []string) func(http.Handler) http.Handler {
	if secret == "" && pubKey == nil {
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

			alg, err := peekAlgorithm(token)
			if err != nil {
				jwtDeny(w, err.Error())
				return
			}

			var subject string
			var roles []string
			switch alg {
			case "HS256":
				if len(key) == 0 {
					jwtDeny(w, "HS256 token received but no HMAC secret configured")
					return
				}
				subject, roles, err = verifyHS256(token, key, issuer)
			case "RS256":
				if pubKey == nil {
					jwtDeny(w, "RS256 token received but no RSA public key configured")
					return
				}
				subject, roles, err = verifyRS256(token, pubKey, issuer)
			default:
				jwtDeny(w, "unsupported token algorithm: "+alg)
				return
			}

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

// peekAlgorithm decodes the JWT header to read the "alg" field without
// verifying the signature. The algorithm is validated later during verification.
func peekAlgorithm(token string) (string, error) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid token structure")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("malformed token header")
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil || header.Alg == "" {
		return "", fmt.Errorf("malformed token header")
	}
	return header.Alg, nil
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

	return parseClaims(parts[1], expectedIssuer)
}

// parseClaims decodes the base64url-encoded payload and validates exp and iss.
func parseClaims(payloadB64 string, expectedIssuer string) (string, []string, error) {
	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadB64)
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

// verifyRS256 parses and verifies an RS256 JWT signed with the given RSA public key.
// Returns the "sub" and "roles" claims on success.
func verifyRS256(token string, pubKey *rsa.PublicKey, expectedIssuer string) (string, []string, error) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return "", nil, fmt.Errorf("invalid token structure")
	}

	// SHA-256 digest of "header.payload" is the RS256 signing input.
	h := sha256.New()
	h.Write([]byte(parts[0] + "." + parts[1]))
	digest := h.Sum(nil)

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", nil, fmt.Errorf("malformed token signature")
	}

	if err := rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, digest, sig); err != nil {
		return "", nil, fmt.Errorf("invalid token signature")
	}

	return parseClaims(parts[1], expectedIssuer)
}

func jwtDeny(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":  "unauthorized",
		"reason": reason,
	})
}
