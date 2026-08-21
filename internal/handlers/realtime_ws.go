package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-iot/iot"
	"github.com/iag/fleet-tool/backend/internal/notifications"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// RealtimeWS multiplexes the fleet realtime channels (fleet-wide positions,
// per-vehicle track pings, and the notification bell) over a single WebSocket,
// matching the wire protocol the frontend realtime-hub already speaks:
//
//	client → { type:"auth", token }            (already authenticated at upgrade;
//	          { type:"subscribe", channels, vehicleId }   we just ack)
//	          { type:"ping" }
//	server → { type:"auth", ok }
//	          { type:"fleet", payload:{vehicles:[…]} }
//	          { type:"ping",  vehicleId, payload }
//	          { type:"bell",  payload:{items,unread} }
//	          { type:"error", message } | { type:"pong" }
//
// Auth is enforced by the upgrade request (the gateway lifts ?token= to a Bearer
// header; see liftWebSocketToken for the direct path), so the principal is in
// the gin context before we upgrade. Per-channel permission is checked here.
type RealtimeWS struct {
	Repo   *store.Repository
	Hub    *iot.Hub
	Store  *iot.Store            // optional: latest-ping seed for track
	Broker *notifications.Broker // optional: bell wake-ups
	// Snap is the process-wide fleet snapshot. nil disables the "fleet"
	// channel rather than falling back to per-connection queries — the
	// fallback is what this field exists to remove.
	Snap *FleetSnapshotter
	// AllowedOrigin is the CORS origin permitted to upgrade; "" or "*" allows any.
	AllowedOrigin string
}

const (
	wsWriteWait  = 10 * time.Second
	wsPongWait   = 60 * time.Second
	wsPingPeriod = 50 * time.Second
	wsSendBuffer = 64
)

func (h *RealtimeWS) Register(rg *gin.RouterGroup) {
	rg.GET("/realtime/ws", auth.RequireUser(), h.handle)
}

func (h *RealtimeWS) upgrader() websocket.Upgrader {
	allowed := h.AllowedOrigin
	return websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			if allowed == "" || allowed == "*" {
				return true
			}
			origin := r.Header.Get("Origin")
			return origin == "" || origin == allowed
		},
	}
}

type wsClientMsg struct {
	Type      string   `json:"type"`
	Channels  []string `json:"channels"`
	VehicleID string   `json:"vehicleId"`
}

func (h *RealtimeWS) handle(c *gin.Context) {
	userID, _ := auth.ActorUserKey(c)
	// Snapshot permissions while the gin context is valid (before goroutines).
	canFleet := auth.HasPerm(c, "view_vehicle") || auth.HasPerm(c, "view_telemetry")
	canTrack := auth.HasPerm(c, "view_telemetry")
	canNotif := auth.HasPerm(c, "view_notification")

	up := h.upgrader()
	ws, err := up.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return // Upgrade already wrote the error response.
	}
	defer ws.Close()

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	out := make(chan []byte, wsSendBuffer)
	go h.writePump(ctx, ws, out, cancel)

	send := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		select {
		case out <- b:
		case <-ctx.Done():
		}
	}

	ws.SetReadDeadline(time.Now().Add(wsPongWait))
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})

	subbed := map[string]func(){} // channel-key -> cancel
	defer func() {
		for _, stop := range subbed {
			stop()
		}
	}()

	for {
		var msg wsClientMsg
		if err := ws.ReadJSON(&msg); err != nil {
			return
		}
		switch msg.Type {
		case "auth":
			// Upgrade was already authenticated; acknowledge.
			send(map[string]any{"type": "auth", "ok": true})
		case "ping":
			send(map[string]any{"type": "pong"})
		case "subscribe":
			for _, ch := range msg.Channels {
				switch ch {
				case "fleet":
					if !canFleet {
						send(map[string]any{"type": "error", "message": "forbidden: fleet"})
						continue
					}
					if _, ok := subbed["fleet"]; ok {
						continue
					}
					subbed["fleet"] = h.runFleet(ctx, send)
				case "track":
					key := "track:" + msg.VehicleID
					if !canTrack || msg.VehicleID == "" {
						send(map[string]any{"type": "error", "message": "forbidden or missing vehicleId: track"})
						continue
					}
					if _, ok := subbed[key]; ok {
						continue
					}
					subbed[key] = h.runTrack(ctx, msg.VehicleID, send)
				case "notifications":
					if !canNotif || userID == "" {
						send(map[string]any{"type": "error", "message": "forbidden: notifications"})
						continue
					}
					if _, ok := subbed["notifications"]; ok {
						continue
					}
					subbed["notifications"] = h.runNotifications(ctx, userID, send)
				}
			}
		case "unsubscribe":
			for _, ch := range msg.Channels {
				for key, stop := range subbed {
					if key == ch || (ch == "track" && len(key) > 6 && key[:6] == "track:") {
						stop()
						delete(subbed, key)
					}
				}
			}
		}
	}
}

func (h *RealtimeWS) writePump(ctx context.Context, ws *websocket.Conn, out <-chan []byte, cancel func()) {
	ticker := time.NewTicker(wsPingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case b, ok := <-out:
			if !ok {
				return
			}
			ws.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := ws.WriteMessage(websocket.TextMessage, b); err != nil {
				cancel()
				return
			}
		case <-ticker.C:
			ws.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				cancel()
				return
			}
		}
	}
}

// runFleet emits an initial full snapshot, then one updated vehicle per live ping.
//
// Both come from the shared FleetSnapshotter, so this connection issues no
// query of its own. It used to run a full `Vehicles.List` on connect and a
// `Vehicles.Get` for every ping it forwarded — with N sockets open and P pings
// a second that was N×P point reads on top of N full scans, for data the
// snapshot already holds and the ping itself already carries.
func (h *RealtimeWS) runFleet(ctx context.Context, send func(any)) func() {
	if h.Snap == nil {
		return func() {}
	}
	if vs := h.Snap.Vehicles(); len(vs) > 0 {
		send(map[string]any{"type": "fleet", "payload": fleetPayload{
			GeneratedAt: time.Now().UTC().Format(time.RFC3339), Vehicles: vs,
		}})
	}
	if h.Hub == nil {
		return func() {}
	}
	ch, stop := h.Hub.SubscribeLive()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case p, ok := <-ch:
				if !ok {
					return
				}
				if p.VehicleID == "" {
					continue
				}
				// Take the identity fields from the snapshot and the position
				// from the ping in hand. This goroutine and the snapshotter are
				// independent hub subscribers, so there is no ordering between
				// them — reading the position back out of the snapshot could
				// forward the PREVIOUS fix. The ping already carries the new one.
				v, known := h.Snap.Vehicle(p.VehicleID)
				if !known {
					// An id the snapshot has never seen: skip rather than emit
					// a row with no plate. The next refresh introduces it.
					continue
				}
				v.Lat, v.Lng = p.Lat, p.Lng
				if p.Heading != nil {
					v.Heading = *p.Heading
				}
				send(map[string]any{"type": "fleet", "payload": fleetPayload{
					GeneratedAt: time.Now().UTC().Format(time.RFC3339),
					Vehicles:    []fleetVehicleSnap{v},
				}})
			}
		}
	}()
	return stop
}

// runTrack streams per-vehicle pings (seeding the latest known fix if available).
func (h *RealtimeWS) runTrack(ctx context.Context, vehicleID string, send func(any)) func() {
	if h.Store != nil {
		if p, err := h.Store.LatestPing(ctx, vehicleID); err == nil && p != nil {
			send(map[string]any{"type": "ping", "vehicleId": vehicleID, "payload": p})
		}
	}
	if h.Hub == nil {
		return func() {}
	}
	ch, stop := h.Hub.Subscribe(vehicleID)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case p, ok := <-ch:
				if !ok {
					return
				}
				send(map[string]any{"type": "ping", "vehicleId": vehicleID, "payload": p})
			}
		}
	}()
	return stop
}

// runNotifications emits an initial bell then one per broker wake-up.
func (h *RealtimeWS) runNotifications(ctx context.Context, userID string, send func(any)) func() {
	emit := func() {
		if h.Repo == nil {
			return
		}
		items, err := h.Repo.Notifications.List(ctx, userID, 0)
		if err != nil {
			return
		}
		unread, _ := h.Repo.Notifications.UnreadCount(ctx, userID)
		send(map[string]any{"type": "bell", "payload": listResponse{Items: items, Unread: unread}})
	}
	emit()
	if h.Broker == nil {
		return func() {}
	}
	ch, stop := h.Broker.Subscribe(userID)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-ch:
				if !ok {
					return
				}
				emit()
			}
		}
	}()
	return stop
}
