package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alvor-technologies/iag-platform-go/authclient"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/iag/fleet-tool/backend/internal/ctxkeys"
	"github.com/iag/fleet-iot/iot"
)

// principalMW injects an authenticated principal so the route's RequireUser and
// per-channel HasPerm checks resolve without real JWT verification.
func principalMW(claims *authclient.Claims) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(ctxkeys.UserID, uuid.MustParse("00000000-0000-0000-0000-000000000001"))
		c.Set(ctxkeys.Claims, claims)
		if len(claims.Permissions) > 0 {
			c.Set(ctxkeys.Permissions, claims.Permissions)
		}
		c.Next()
	}
}

func wsServer(t *testing.T, claims *authclient.Claims, h *RealtimeWS) (*httptest.Server, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(principalMW(claims))
	h.Register(r.Group("/api"))
	srv := httptest.NewServer(r)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/realtime/ws"
	return srv, url
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ws, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		t.Fatalf("dial: %v (status %d)", err, code)
	}
	return ws
}

func readUntil(t *testing.T, ws *websocket.Conn, typ string) map[string]any {
	t.Helper()
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		var m map[string]any
		if err := ws.ReadJSON(&m); err != nil {
			t.Fatalf("waiting for %q: %v", typ, err)
		}
		if m["type"] == typ {
			return m
		}
	}
}

var superClaims = &authclient.Claims{IsSuperuser: true, PrincipalType: authclient.PrincipalUser}

func TestWSAuthAck(t *testing.T) {
	srv, url := wsServer(t, superClaims, &RealtimeWS{})
	defer srv.Close()
	ws := dial(t, url)
	defer ws.Close()

	if err := ws.WriteJSON(map[string]any{"type": "auth", "token": "x"}); err != nil {
		t.Fatal(err)
	}
	m := readUntil(t, ws, "auth")
	if m["ok"] != true {
		t.Fatalf("expected auth ok=true, got %v", m)
	}
}

func TestWSPingPong(t *testing.T) {
	srv, url := wsServer(t, superClaims, &RealtimeWS{})
	defer srv.Close()
	ws := dial(t, url)
	defer ws.Close()

	if err := ws.WriteJSON(map[string]any{"type": "ping"}); err != nil {
		t.Fatal(err)
	}
	readUntil(t, ws, "pong") // fails the test if not received within deadline
}

func TestWSTrackStreamsPings(t *testing.T) {
	hub := iot.NewHubFromEnv() // no REDIS_URL → in-process broker
	srv, url := wsServer(t, superClaims, &RealtimeWS{Hub: hub})
	defer srv.Close()
	ws := dial(t, url)
	defer ws.Close()

	if err := ws.WriteJSON(map[string]any{"type": "subscribe", "channels": []string{"track"}, "vehicleId": "V1"}); err != nil {
		t.Fatal(err)
	}

	// Publish repeatedly until the subscription is live and a ping is delivered.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tk := time.NewTicker(20 * time.Millisecond)
		defer tk.Stop()
		speed := 12.0
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				hub.Publish(iot.Ping{VehicleID: "V1", TS: time.Now().UTC(), Lat: 0.3, Lng: 32.5, SpeedKmh: &speed})
			}
		}
	}()

	m := readUntil(t, ws, "ping")
	if m["vehicleId"] != "V1" {
		t.Fatalf("expected ping for V1, got %v", m["vehicleId"])
	}
	if m["payload"] == nil {
		t.Fatalf("expected a ping payload, got %v", m)
	}
}

func TestWSForbiddenChannelWithoutPerm(t *testing.T) {
	// A non-super principal whose perms don't include telemetry must be refused
	// the track channel (and not crash the connection).
	claims := &authclient.Claims{PrincipalType: authclient.PrincipalUser, Permissions: []string{"view_driver"}}
	srv, url := wsServer(t, claims, &RealtimeWS{Hub: iot.NewHubFromEnv()})
	defer srv.Close()
	ws := dial(t, url)
	defer ws.Close()

	if err := ws.WriteJSON(map[string]any{"type": "subscribe", "channels": []string{"track"}, "vehicleId": "V1"}); err != nil {
		t.Fatal(err)
	}
	m := readUntil(t, ws, "error")
	if msg, _ := m["message"].(string); !strings.Contains(msg, "track") {
		t.Fatalf("expected a track-forbidden error, got %v", m)
	}
}

func TestWSRequiresAuthenticatedUpgrade(t *testing.T) {
	// No principal in context → RequireUser rejects the upgrade (dial fails 401).
	gin.SetMode(gin.TestMode)
	r := gin.New() // no principalMW
	(&RealtimeWS{}).Register(r.Group("/api"))
	srv := httptest.NewServer(r)
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/realtime/ws"

	_, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("expected upgrade to be rejected without auth")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		got := 0
		if resp != nil {
			got = resp.StatusCode
		}
		t.Fatalf("expected 401, got %d", got)
	}
}
