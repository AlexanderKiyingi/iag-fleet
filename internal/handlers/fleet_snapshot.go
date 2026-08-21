package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/iag/fleet-iot/iot"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// FleetSnapshotter owns the one shared view of vehicle hot-state that every
// /vehicles/live/stream connection reads from.
//
// Before this existed, each SSE connection ran its own three-second timer and
// its own unbounded `SELECT … FROM vehicles` on every tick, plus a
// `Vehicles.Get` and a full re-serialization of the whole fleet on every
// telemetry ping. The database cost was therefore
//
//	clients × (fleet_size ÷ 3s + pings/s × fleet_size)
//
// — at the default stream cap of 1000 that is 333 full scans of `vehicles` per
// second before a single ping arrives, against the one Postgres every other
// service shares. Worse, `vehicles` is the table the IoT ingest pipeline
// UPDATEs on every ping, so the scans were fighting the dead tuples those
// writes leave behind.
//
// Now: one goroutine, one timer, one query per interval for the whole process,
// and the JSON marshalled once per version rather than once per client per
// tick. The wire format is unchanged — the same `event: fleet` frame with the
// full vehicle array — so no client has to learn a merge protocol. What changed
// is that a frame is only sent when something actually moved.
type FleetSnapshotter struct {
	repo     *store.Repository
	hub      *iot.Hub
	interval time.Duration
	log      *slog.Logger

	mu       sync.RWMutex
	snaps    map[string]fleetVehicleSnap
	payload  []byte // marshalled fleetPayload for the current version
	version  uint64
	loadErr  error
	loadFail int

	subMu sync.Mutex
	subs  map[chan struct{}]struct{}
}

// maxSnapshotVehicles bounds the shared refresh. `vehicles` is a master table
// sized by the fleet, not an append-only log, so this is a guard against a
// runaway import rather than an expected limit — it is logged when it bites.
const maxSnapshotVehicles = 20000

// NewFleetSnapshotter builds the shared snapshot. Call Run once; it returns
// when ctx is cancelled.
func NewFleetSnapshotter(repo *store.Repository, hub *iot.Hub, interval time.Duration, log *slog.Logger) *FleetSnapshotter {
	if interval <= 0 {
		interval = 3 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &FleetSnapshotter{
		repo:     repo,
		hub:      hub,
		interval: interval,
		log:      log,
		snaps:    map[string]fleetVehicleSnap{},
		subs:     map[chan struct{}]struct{}{},
	}
}

// Run drives the refresh loop and the ping fan-in until ctx is cancelled.
func (s *FleetSnapshotter) Run(ctx context.Context) {
	s.refresh(ctx)

	var liveCh <-chan iot.Ping
	if s.hub != nil {
		ch, cancel := s.hub.SubscribeLive()
		defer cancel()
		liveCh = ch
	}

	tick := time.NewTicker(s.interval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case p, ok := <-liveCh:
			if !ok {
				liveCh = nil
				continue
			}
			s.applyPing(p)
		case <-tick.C:
			s.refresh(ctx)
		}
	}
}

// refresh reloads vehicle hot-state. One query for the whole process.
func (s *FleetSnapshotter) refresh(ctx context.Context) {
	vs, err := s.repo.Vehicles.ListCapped(ctx, maxSnapshotVehicles)
	if err != nil {
		s.mu.Lock()
		s.loadErr = err
		s.loadFail++
		fails := s.loadFail
		s.mu.Unlock()
		s.log.Warn("fleet snapshot refresh failed", "err", err, "consecutiveFailures", fails)
		s.notify() // wake subscribers so they can surface the error and close
		return
	}
	if len(vs) == maxSnapshotVehicles {
		s.log.Warn("fleet snapshot hit its vehicle cap; the live map is showing a subset",
			"cap", maxSnapshotVehicles)
	}

	next := make(map[string]fleetVehicleSnap, len(vs))
	for _, v := range vs {
		next[v.ID] = vehicleSnap(v)
	}

	s.mu.Lock()
	s.loadErr = nil
	s.loadFail = 0
	changed := !sameSnaps(s.snaps, next)
	s.snaps = next
	if changed {
		s.rebuildLocked()
	}
	s.mu.Unlock()

	if changed {
		s.notify()
	}
}

// applyPing folds one telemetry ping into the snapshot without touching the
// database. The ping already carries position and heading, which is the whole
// reason the old per-ping `Vehicles.Get` was redundant. Status, plate and
// location come from the vehicles row and are picked up by the next refresh —
// bounded staleness of one interval, which is what the poll gave anyway.
func (s *FleetSnapshotter) applyPing(p iot.Ping) {
	if p.VehicleID == "" {
		return
	}
	s.mu.Lock()
	cur, ok := s.snaps[p.VehicleID]
	if !ok {
		// A vehicle we have not loaded yet — the next refresh will introduce it
		// with its plate and status rather than inventing a half-populated row.
		s.mu.Unlock()
		return
	}
	updated := cur
	updated.Lat, updated.Lng = p.Lat, p.Lng
	if p.Heading != nil {
		updated.Heading = *p.Heading
	}
	if updated == cur {
		s.mu.Unlock()
		return
	}
	s.snaps[p.VehicleID] = updated
	s.rebuildLocked()
	s.mu.Unlock()
	s.notify()
}

// rebuildLocked re-marshals the payload. Caller holds s.mu for writing.
//
// Once per version, for every subscriber — the old code marshalled the entire
// fleet array separately for each connected client on every tick and on every
// ping.
func (s *FleetSnapshotter) rebuildLocked() {
	list := make([]fleetVehicleSnap, 0, len(s.snaps))
	for _, v := range s.snaps {
		list = append(list, v)
	}
	blob, err := json.Marshal(fleetPayload{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Vehicles:    list,
	})
	if err != nil {
		s.log.Error("fleet snapshot marshal failed", "err", err)
		return
	}
	s.payload = blob
	s.version++
}

// Current returns the payload and its version, plus the consecutive-failure
// count so a stream can close rather than send keepalives over a dead database.
func (s *FleetSnapshotter) Current() (payload []byte, version uint64, consecutiveFailures int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.payload, s.version, s.loadFail
}

// Vehicles returns the current snapshot as a slice, for callers that need the
// values rather than the pre-marshalled payload (the WebSocket multiplexer
// wraps them in its own envelope).
func (s *FleetSnapshotter) Vehicles() []fleetVehicleSnap {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]fleetVehicleSnap, 0, len(s.snaps))
	for _, v := range s.snaps {
		out = append(out, v)
	}
	return out
}

// Vehicle returns one vehicle's current snapshot. The bool is false when the
// snapshot has not seen that id yet — a caller should skip rather than emit a
// half-populated row.
func (s *FleetSnapshotter) Vehicle(id string) (fleetVehicleSnap, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.snaps[id]
	return v, ok
}

// Subscribe returns a channel that receives a signal whenever the snapshot
// changes. The channel is buffered depth 1 and signals are coalesced: a
// subscriber that is slow gets one wake-up, not a backlog, and always reads the
// newest payload when it does wake.
func (s *FleetSnapshotter) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.subMu.Lock()
	s.subs[ch] = struct{}{}
	s.subMu.Unlock()
	return ch, func() {
		s.subMu.Lock()
		delete(s.subs, ch)
		s.subMu.Unlock()
	}
}

func (s *FleetSnapshotter) notify() {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default: // already has a pending wake-up; coalesce
		}
	}
}

// sameSnaps reports whether two snapshot maps are identical. fleetVehicleSnap
// is all comparable fields, so == is enough and avoids reflect.DeepEqual on a
// path that runs every interval.
func sameSnaps(a, b map[string]fleetVehicleSnap) bool {
	if len(a) != len(b) {
		return false
	}
	for id, av := range a {
		bv, ok := b[id]
		if !ok || av != bv {
			return false
		}
	}
	return true
}
