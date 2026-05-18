package security

import (
	"bytes"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// keyFunc derives a rate-limit bucket key from the request. By default
// we bucket per remote IP, but auth-sensitive routes (forgot-password)
// bucket per-email/per-user where applicable.
type keyFunc func(c *gin.Context) string

// limiterMap is the in-memory store of token-bucket limiters keyed by
// keyFunc output. NOT durable: each API instance has its own counters.
// Sufficient for single-replica deployments and a sensible default for
// most fleet-management installs. For HA, swap for redis_rate or similar.
type limiterMap struct {
	mu       sync.Mutex
	buckets  map[string]*rate.Limiter
	rate     rate.Limit
	burst    int
	lastSeen map[string]time.Time
}

func newLimiterMap(perSecond rate.Limit, burst int) *limiterMap {
	return &limiterMap{
		buckets:  map[string]*rate.Limiter{},
		lastSeen: map[string]time.Time{},
		rate:     perSecond,
		burst:    burst,
	}
}

func (lm *limiterMap) get(key string) *rate.Limiter {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	l, ok := lm.buckets[key]
	if !ok {
		l = rate.NewLimiter(lm.rate, lm.burst)
		lm.buckets[key] = l
	}
	lm.lastSeen[key] = time.Now()
	// Best-effort eviction every ~1024 entries: drop buckets we haven't
	// seen for an hour. Keeps memory bounded under sustained traffic.
	if len(lm.buckets) > 1024 {
		cutoff := time.Now().Add(-time.Hour)
		for k, t := range lm.lastSeen {
			if t.Before(cutoff) {
				delete(lm.buckets, k)
				delete(lm.lastSeen, k)
			}
		}
	}
	return l
}

// RateLimit returns middleware that enforces (perMinute, burst) on the
// bucket key derived from kf. On exhaustion the response is 429 with a
// Retry-After header.
func RateLimit(perMinute float64, burst int, kf keyFunc) gin.HandlerFunc {
	lm := newLimiterMap(rate.Limit(perMinute/60.0), burst)
	return func(c *gin.Context) {
		key := kf(c)
		if key == "" {
			c.Next()
			return
		}
		if !lm.get(key).Allow() {
			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
			})
			return
		}
		c.Next()
	}
}

// ByIP buckets per client IP. Use for routes where the request body is
// untrusted and there's no other natural key.
func ByIP(c *gin.Context) string {
	return c.ClientIP()
}

// ByIPAndPath buckets per (IP, path). Useful when one IP legitimately
// hits multiple endpoints — separate counters keep one busy endpoint
// from starving the others.
func ByIPAndPath(c *gin.Context) string {
	return c.ClientIP() + "|" + c.FullPath()
}

// ByJSONField buckets by a top-level string field in the JSON body.
// Used for /auth/forgot-password to throttle per-email rather than
// per-IP (an attacker rotating IPs can still hit one address).
//
// The body is read fully and rewrapped so downstream handlers can still
// bind it; the cost is one extra small alloc per request — fine compared
// to the SQL roundtrip on the protected endpoint.
func ByJSONField(field string) keyFunc {
	return func(c *gin.Context) string {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			return c.ClientIP()
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))

		val := extractJSONString(body, field)
		if val == "" {
			return c.ClientIP()
		}
		return field + ":" + val
	}
}

// extractJSONString does a permissive scan for `"<field>": "<value>"`
// without fully unmarshaling. Cheap and good enough for rate-limit keys —
// false negatives just bucket per-IP, which is the safe fallback.
func extractJSONString(body []byte, field string) string {
	needle := []byte(`"` + field + `"`)
	i := indexOf(body, needle)
	if i < 0 {
		return ""
	}
	j := i + len(needle)
	// skip whitespace + colon + whitespace
	for j < len(body) && (body[j] == ' ' || body[j] == '\t' || body[j] == ':' || body[j] == '\n' || body[j] == '\r') {
		j++
	}
	if j >= len(body) || body[j] != '"' {
		return ""
	}
	j++
	start := j
	for j < len(body) && body[j] != '"' {
		if body[j] == '\\' && j+1 < len(body) {
			j += 2
			continue
		}
		j++
	}
	if j >= len(body) {
		return ""
	}
	return string(body[start:j])
}

func indexOf(haystack, needle []byte) int {
outer:
	for i := 0; i+len(needle) <= len(haystack); i++ {
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				continue outer
			}
		}
		return i
	}
	return -1
}

