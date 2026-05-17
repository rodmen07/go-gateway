package middleware

import (
	"bytes"
	"container/list"
	"net/http"
	"strings"
	"sync"
	"time"
)

type cacheEntry struct {
	key       string
	status    int
	header    http.Header
	body      []byte
	expiresAt time.Time
}

type cacheRecord struct {
	status int
	header http.Header
	body   bytes.Buffer
}

func (cr *cacheRecord) Header() http.Header {
	if cr.header == nil {
		cr.header = make(http.Header)
	}
	return cr.header
}

func (cr *cacheRecord) Write(b []byte) (int, error) {
	if cr.status == 0 {
		cr.status = http.StatusOK
	}
	return cr.body.Write(b)
}

func (cr *cacheRecord) WriteHeader(statusCode int) {
	cr.status = statusCode
}

type lruCache struct {
	mu         sync.Mutex
	maxEntries int
	ll         *list.List
	entries    map[string]*list.Element
}

type lruItem struct {
	key   string
	value cacheEntry
}

func newLRU(maxEntries int) *lruCache {
	if maxEntries < 1 {
		maxEntries = 1
	}
	return &lruCache{
		maxEntries: maxEntries,
		ll:         list.New(),
		entries:    make(map[string]*list.Element),
	}
}

func (c *lruCache) get(key string) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	ele, ok := c.entries[key]
	if !ok {
		return cacheEntry{}, false
	}

	item := ele.Value.(*lruItem)
	if time.Now().After(item.value.expiresAt) {
		c.ll.Remove(ele)
		delete(c.entries, key)
		return cacheEntry{}, false
	}

	c.ll.MoveToFront(ele)
	return item.value, true
}

func (c *lruCache) set(key string, val cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ele, ok := c.entries[key]; ok {
		item := ele.Value.(*lruItem)
		item.value = val
		c.ll.MoveToFront(ele)
		return
	}

	ele := c.ll.PushFront(&lruItem{key: key, value: val})
	c.entries[key] = ele

	for c.ll.Len() > c.maxEntries {
		back := c.ll.Back()
		if back == nil {
			return
		}
		item := back.Value.(*lruItem)
		delete(c.entries, item.key)
		c.ll.Remove(back)
	}
}

// ResponseCache caches short-lived successful GET responses for read endpoints.
//
// Cache key includes path + query + X-Auth-Subject to keep per-user responses
// isolated. Responses include X-Cache: HIT/MISS when cacheable.
func ResponseCache(ttl time.Duration, maxEntries int) func(http.Handler) http.Handler {
	cache := newLRU(maxEntries)
	if ttl <= 0 {
		ttl = 5 * time.Second
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isCacheableRequest(r) {
				next.ServeHTTP(w, r)
				return
			}

			key := cacheKey(r)
			if entry, ok := cache.get(key); ok {
				copyHeaders(w.Header(), entry.header)
				w.Header().Set("X-Cache", "HIT")
				w.WriteHeader(entry.status)
				_, _ = w.Write(entry.body)
				return
			}

			rec := &cacheRecord{}
			next.ServeHTTP(rec, r)

			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}

			copyHeaders(w.Header(), rec.Header())
			w.Header().Set("X-Cache", "MISS")
			w.WriteHeader(status)
			_, _ = w.Write(rec.body.Bytes())

			if status >= 200 && status < 300 {
				cache.set(key, cacheEntry{
					key:       key,
					status:    status,
					header:    cloneHeader(rec.Header()),
					body:      append([]byte(nil), rec.body.Bytes()...),
					expiresAt: time.Now().Add(ttl),
				})
			}
		})
	}
}

func isCacheableRequest(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	path := r.URL.Path
	return strings.HasPrefix(path, "/api/reporting") ||
		strings.HasPrefix(path, "/api/search") ||
		strings.HasPrefix(path, "/api/events") ||
		strings.HasPrefix(path, "/api/v1/projects")
}

func cacheKey(r *http.Request) string {
	subject := r.Header.Get("X-Auth-Subject")
	return r.URL.Path + "?" + r.URL.RawQuery + "|sub=" + subject
}

func cloneHeader(src http.Header) http.Header {
	dst := make(http.Header, len(src))
	for k, vv := range src {
		copied := make([]string, len(vv))
		copy(copied, vv)
		dst[k] = copied
	}
	return dst
}

func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		copied := make([]string, len(vv))
		copy(copied, vv)
		dst[k] = copied
	}
}
