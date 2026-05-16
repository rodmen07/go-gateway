package observer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
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
// When pubsubProject and pubsubTopic are set it publishes to Pub/Sub via the
// REST API instead of calling observaboard directly, enabling async delivery
// with built-in retry and dead-lettering.
type Observer struct {
	url    string
	apiKey string
	client *http.Client

	// Pub/Sub config (optional — zero values disable the Pub/Sub path).
	pubsubProject string
	pubsubTopic   string

	// Metadata-server token cache (populated lazily on Cloud Run).
	tokenMu     sync.Mutex
	cachedToken string
	tokenExpiry time.Time
}

// New returns an Observer.
// pubsubProject and pubsubTopic are optional; when both are non-empty, mutation
// events are published to Pub/Sub instead of calling observaboard over HTTP.
// If apiKey is empty AND pubsubProject/pubsubTopic are also empty, returns nil
// (no-op observer).
func New(url, apiKey, pubsubProject, pubsubTopic string) *Observer {
	if apiKey == "" && (pubsubProject == "" || pubsubTopic == "") {
		return nil
	}
	return &Observer{
		url:           url,
		apiKey:        apiKey,
		pubsubProject: pubsubProject,
		pubsubTopic:   pubsubTopic,
		client:        &http.Client{Timeout: 5 * time.Second},
	}
}

// Observe sends a fire-and-forget ingest event for a CRM mutation.
// When pubsubProject and pubsubTopic are configured the event is published to
// Pub/Sub (async, retryable); otherwise it POSTs directly to observaboard.
// The call is non-blocking; errors are silently discarded.
func (o *Observer) Observe(method, path string) {
	source := sourceForPath(path)
	if source == "" {
		return
	}
	eventType := source + "." + eventActionForMethod(method)

	body, err := json.Marshal(map[string]any{
		"source":     source,
		"event_type": eventType,
		"payload":    map[string]string{"method": method, "path": path},
	})
	if err != nil {
		return
	}

	if o.pubsubProject != "" && o.pubsubTopic != "" {
		go o.publishToPubSub(body)
	} else {
		go o.postToObservaboard(body)
	}
}

// postToObservaboard sends a direct HTTP POST to observaboard's ingest endpoint.
func (o *Observer) postToObservaboard(body []byte) {
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
}

// publishToPubSub publishes body as a base64-encoded Pub/Sub message using the
// REST API. On Cloud Run, an OIDC access token is fetched from the metadata
// server and cached for up to 55 minutes.
func (o *Observer) publishToPubSub(body []byte) {
	token, err := o.metadataToken()
	if err != nil {
		// Metadata server unavailable (local dev) — fall back to HTTP.
		o.postToObservaboard(body)
		return
	}

	payload, err := json.Marshal(map[string]any{
		"messages": []map[string]any{
			{"data": base64.StdEncoding.EncodeToString(body)},
		},
	})
	if err != nil {
		return
	}

	url := fmt.Sprintf(
		"https://pubsub.googleapis.com/v1/projects/%s/topics/%s:publish",
		o.pubsubProject, o.pubsubTopic,
	)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// metadataToken returns a cached GCP access token fetched from the instance
// metadata server. The token is refreshed 5 minutes before expiry.
func (o *Observer) metadataToken() (string, error) {
	o.tokenMu.Lock()
	defer o.tokenMu.Unlock()

	if time.Now().Before(o.tokenExpiry) {
		return o.cachedToken, nil
	}

	req, err := http.NewRequest(
		http.MethodGet,
		"http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token",
		nil,
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Metadata-Flavor", "Google")

	resp, err := o.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	// Cache the token, expiring 5 minutes early to avoid clock skew issues.
	o.cachedToken = result.AccessToken
	o.tokenExpiry = time.Now().Add(time.Duration(result.ExpiresIn-300) * time.Second)
	return o.cachedToken, nil
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
