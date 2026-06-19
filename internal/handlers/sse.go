package handlers

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
)

const (
	defaultMaxSSEStreams = 1000
	// maxConsecutivePollFails bounds silent DB-poll failures before a stream
	// surfaces an error frame and closes, instead of hanging alive-but-empty.
	maxConsecutivePollFails = 5
)

// StreamGate caps concurrent SSE streams so a flood of long-lived connections
// can't exhaust goroutines or the DB pool. A nil *StreamGate is unlimited —
// handlers built without one (e.g. in tests) keep their old behaviour.
type StreamGate struct {
	max int64
	cur atomic.Int64
}

// NewStreamGate reads the cap from FLEET_MAX_SSE_STREAMS (default 1000); a value
// <= 0 disables the gate (returns nil = unlimited).
func NewStreamGate() *StreamGate {
	max := int64(defaultMaxSSEStreams)
	if v := os.Getenv("FLEET_MAX_SSE_STREAMS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			max = n
		}
	}
	if max <= 0 {
		return nil
	}
	return &StreamGate{max: max}
}

// acquire reserves a slot; ok=false when at capacity. release is idempotent and
// must be deferred when ok. A nil gate always succeeds.
func (g *StreamGate) acquire() (release func(), ok bool) {
	if g == nil {
		return func() {}, true
	}
	if g.cur.Add(1) > g.max {
		g.cur.Add(-1)
		return nil, false
	}
	var done atomic.Bool
	return func() {
		if done.CompareAndSwap(false, true) {
			g.cur.Add(-1)
		}
	}, true
}

// reserveStream reserves a slot or writes 503. When ok, the caller must defer
// release(). Must be called before any SSE headers are written.
func (g *StreamGate) reserveStream(c *gin.Context) (release func(), ok bool) {
	release, ok = g.acquire()
	if !ok {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "too many live streams; retry shortly"})
	}
	return
}

// tokenExpiry returns the bearer token's exp, or zero when unknown (no claims /
// no exp). Used to terminate a long-lived stream when its token lapses.
func tokenExpiry(c *gin.Context) time.Time {
	claims, ok := auth.PlatformClaimsFromContext(c)
	if !ok || claims == nil || claims.ExpiresAt == nil {
		return time.Time{}
	}
	return claims.ExpiresAt.Time
}

// tokenExpired reports whether a (non-zero) expiry is at or before now.
func tokenExpired(expiry, now time.Time) bool {
	return !expiry.IsZero() && !now.Before(expiry)
}

// sseEvent writes one named SSE frame and flushes.
func sseEvent(w io.Writer, flusher http.Flusher, event, data string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	flusher.Flush()
}
