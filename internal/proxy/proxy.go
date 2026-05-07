package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/rodmen07/go-gateway/internal/observer"
)

// New builds an httputil.ReverseProxy that forwards requests to upstream,
// stripping prefixToStrip from the path before forwarding.
//
// If obs is non-nil, successful mutation responses (POST/PATCH/PUT/DELETE → 2xx)
// trigger a fire-and-forget ingest event to observaboard.
//
// Example: upstream="https://accounts-service.fly.dev", prefixToStrip="/api/accounts"
//
//	/api/accounts/api/v1/accounts → https://accounts-service.fly.dev/api/v1/accounts
func New(upstream, prefixToStrip string, obs *observer.Observer) http.Handler {
	target, err := url.Parse(upstream)
	if err != nil {
		panic("go-gateway: invalid upstream URL: " + upstream)
	}

	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host

			// Strip the gateway-specific prefix so the upstream sees its own paths.
			req.URL.Path = strings.TrimPrefix(req.URL.Path, prefixToStrip)
			if req.URL.Path == "" {
				req.URL.Path = "/"
			}

			// Standard forwarding headers.
			if clientIP := req.RemoteAddr; clientIP != "" {
				req.Header.Set("X-Forwarded-For", clientIP)
			}
			req.Header.Set("X-Forwarded-Host", req.Host)
		},

		ModifyResponse: func(resp *http.Response) error {
			if obs == nil {
				return nil
			}
			method := resp.Request.Method
			isMutation := method == http.MethodPost || method == http.MethodPatch ||
				method == http.MethodPut || method == http.MethodDelete
			if isMutation && resp.StatusCode < 300 {
				obs.Observe(method, resp.Request.URL.Path)
			}
			return nil
		},

		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"code":    "UPSTREAM_ERROR",
				"message": err.Error(),
			})
		},
	}

	return rp
}
