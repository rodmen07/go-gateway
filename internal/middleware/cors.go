package middleware

import (
	"net/http"
	"os"
	"strings"
)

// CORS returns a middleware that sets Cross-Origin Resource Sharing headers.
// Configure allowed origins via the ALLOWED_ORIGINS env var (comma-separated).
// Use "*" to allow all origins (development only).
func CORS() func(http.Handler) http.Handler {
	raw := os.Getenv("ALLOWED_ORIGINS")
	allowAll := strings.TrimSpace(raw) == "*"

	var allowed []string
	if !allowAll {
		for _, o := range strings.Split(raw, ",") {
			if o = strings.TrimSpace(o); o != "" {
				allowed = append(allowed, o)
			}
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if origin != "" && containsOrigin(allowed, origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
			w.Header().Set("Access-Control-Allow-Credentials", "true")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func containsOrigin(allowed []string, origin string) bool {
	for _, a := range allowed {
		if strings.EqualFold(a, origin) {
			return true
		}
	}
	return false
}
