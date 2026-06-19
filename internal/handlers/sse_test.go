package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

func TestStreamGateCapacityAndRelease(t *testing.T) {
	g := &StreamGate{max: 2}

	r1, ok := g.acquire()
	if !ok {
		t.Fatal("first acquire should succeed")
	}
	if _, ok := g.acquire(); !ok {
		t.Fatal("second acquire should succeed (at cap)")
	}
	if _, ok := g.acquire(); ok {
		t.Fatal("third acquire must fail — over capacity")
	}

	r1() // free one slot
	if _, ok := g.acquire(); !ok {
		t.Fatal("acquire should succeed after a release")
	}

	// release is idempotent: calling it again must not free a second slot.
	r1()
	if g.cur.Load() != 2 {
		t.Fatalf("expected 2 slots held after idempotent release, got %d", g.cur.Load())
	}
}

func TestStreamGateNilIsUnlimited(t *testing.T) {
	var g *StreamGate // nil = disabled
	for i := 0; i < 5; i++ {
		if _, ok := g.acquire(); !ok {
			t.Fatalf("nil gate must always acquire (iter %d)", i)
		}
	}
}

func TestTokenExpired(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	if tokenExpired(time.Time{}, now) {
		t.Error("zero expiry (unknown) must never be treated as expired")
	}
	if tokenExpired(now.Add(time.Minute), now) {
		t.Error("future expiry must not be expired")
	}
	if !tokenExpired(now.Add(-time.Second), now) {
		t.Error("past expiry must be expired")
	}
	if !tokenExpired(now, now) {
		t.Error("expiry exactly at now must be expired")
	}
}

// A full gate makes the SSE handler reject with 503 before writing any stream
// headers — proving the cap is wired into the real handler.
func TestTrackStreamReturns503WhenGateFull(t *testing.T) {
	g := &StreamGate{max: 1}
	if _, ok := g.acquire(); !ok { // consume the only slot
		t.Fatal("setup acquire failed")
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/vehicles/V1/track/stream", nil)

	h := &IoT{Gate: g}
	h.stream(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when gate full, got %d", w.Code)
	}
}
