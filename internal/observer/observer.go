package observer

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// sourceMap maps gateway path prefixes to observaboard source names.
var sourceMap = map[string]string{
	"/api/accounts":      "accounts",
	"/api/contacts":      "contacts",
	"/api/opportunities": "opportunities",
	"/api/activities":    "activities",
	"/api/automation":    "automation",
	"/api/integrations":  "integrations",
}

// Observer fires fire-and-forget ingest events to observaboard for CRM mutations.
type Observer struct {
	url    string
	apiKey string
	client *http.Client
}

// New returns an Observer if apiKey is non-empty, otherwise nil.
// Passing a nil Observer to Observe is safe; the call becomes a no-op.
func New(url, apiKey string) *Observer {
	if apiKey == "" {
		return nil
	}
	return &Observer{
		url:    url,
		apiKey: apiKey,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Observe sends a fire-and-forget ingest event to observaboard.
// It derives the source from the request path and the event_type from the HTTP method.
// The call is non-blocking; errors are silently discarded.
func (o *Observer) Observe(method, path string) {
	source := sourceForPath(path)
	if source == "" {
		return
	}
	eventType := source + "." + eventActionForMethod(method)

	go func() {
		body, err := json.Marshal(map[string]any{
			"source":     source,
			"event_type": eventType,
			"payload":    map[string]string{"method": method, "path": path},
		})
		if err != nil {
			return
		}
		req, err := http.NewRequest(http.MethodPost, o.url+"/api/ingest/", bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Api-Key "+o.apiKey)
		resp, err := o.client.Do(req)
		if err != nil {
			return
		}
		_ = resp.Body.Close()
	}()
}

func sourceForPath(path string) string {
	for prefix, source := range sourceMap {
		if strings.HasPrefix(path, prefix) {
			return source
		}
	}
	return ""
}

func eventActionForMethod(method string) string {
	switch strings.ToUpper(method) {
	case http.MethodPost:
		return "created"
	case http.MethodPatch, http.MethodPut:
		return "updated"
	case http.MethodDelete:
		return "deleted"
	default:
		return "changed"
	}
}
