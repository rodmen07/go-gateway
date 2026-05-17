package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"
)

type upstreamStatus struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Status string `json:"status"`          // "ok" | "degraded" | "unreachable"
	Code   int    `json:"code,omitempty"` // HTTP status code from upstream /health
}

// UpstreamsHandler returns an http.HandlerFunc that concurrently probes
// each upstream's /health endpoint with a 3-second timeout and returns an
// aggregated JSON response. The overall gateway status is "ok" only when all
// upstreams respond with a 2xx. If any upstream is unreachable or degraded the
// gateway returns HTTP 502 with status "degraded".
func UpstreamsHandler(upstreams map[string]string) http.HandlerFunc {
	client := &http.Client{Timeout: 3 * time.Second}
	return func(w http.ResponseWriter, r *http.Request) {
		results := make([]upstreamStatus, 0, len(upstreams))
		var mu sync.Mutex
		var wg sync.WaitGroup

		for name, baseURL := range upstreams {
			name, baseURL := name, baseURL
			wg.Add(1)
			go func() {
				defer wg.Done()
				s := probeUpstream(client, name, baseURL)
				mu.Lock()
				results = append(results, s)
				mu.Unlock()
			}()
		}
		wg.Wait()

		// Deterministic ordering for stable responses.
		sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })

		overall := "ok"
		for _, s := range results {
			if s.Status != "ok" {
				overall = "degraded"
				break
			}
		}

		code := http.StatusOK
		if overall != "ok" {
			code = http.StatusBadGateway
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    overall,
			"upstreams": results,
		})
	}
}

func probeUpstream(client *http.Client, name, baseURL string) upstreamStatus {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return upstreamStatus{Name: name, URL: baseURL, Status: "unreachable"}
	}
	resp, err := client.Do(req)
	if err != nil {
		return upstreamStatus{Name: name, URL: baseURL, Status: "unreachable"}
	}
	defer resp.Body.Close()

	status := "ok"
	if resp.StatusCode >= 400 {
		status = "degraded"
	}
	return upstreamStatus{Name: name, URL: baseURL, Status: status, Code: resp.StatusCode}
}
