package middleware

import "net/http"

// SecurityHeaders returns a middleware that attaches a standard set of
// security-hardening response headers to every reply.
//
//   - Strict-Transport-Security: max-age=63072000 (2 years), includeSubDomains
//   - X-Content-Type-Options: nosniff
//   - X-Frame-Options: DENY
//   - Referrer-Policy: strict-origin-when-cross-origin
//   - Content-Security-Policy: restrictive default that allows only same-origin
//   - Permissions-Policy: disables geolocation, microphone, camera, payment
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=()")
		next.ServeHTTP(w, r)
	})
}
