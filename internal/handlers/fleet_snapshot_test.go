package handlers

import (
	"encoding/json"
	"testing"

	"github.com/iag/fleet-iot/iot"
)

// newTestSnapshotter builds a snapshotter with state already loaded, without a
// repository — these cases exercise the publish/coalesce logic, not the query.
// It takes s.mu even though nothing else is running yet, because rebuildLocked
// documents that as its precondition and a helper that quietly ignores it is
// how that contract stops being true.
func newTestSnapshotter(seed map[string]fleetVehicleSnap) *FleetSnapshotter {
	s := NewFleetSnapshotter(nil, nil, 0, nil)
	s.mu.Lock()
	s.snaps = seed
	s.rebuildLocked()
	s.mu.Unlock()
	return s
}

// The version is what tells a connection "there is something new to send". If
// it advances when nothing moved, every subscriber re-serializes and re-sends
// the whole fleet on every tick — which is the bandwidth half of the bug this
// type exists to fix.
func TestSnapshotVersionOnlyMovesOnChange(t *testing.T) {
	s := newTestSnapshotter(map[string]fleetVehicleSnap{
		"V1": {ID: "V1", Plate: "UAA 001", Lat: 1, Lng: 2, Status: "moving"},
	})
	_, v0, _ := s.Current()

	// A ping carrying the position it already has must not publish a version.
	s.applyPing(iot.Ping{VehicleID: "V1", Lat: 1, Lng: 2})
	if _, v1, _ := s.Current(); v1 != v0 {
		t.Errorf("identical ping bumped version %d → %d; subscribers would re-send unchanged state", v0, v1)
	}

	// A real move must.
	s.applyPing(iot.Ping{VehicleID: "V1", Lat: 3, Lng: 4})
	_, v2, _ := s.Current()
	if v2 == v0 {
		t.Fatal("a moved vehicle did not bump the version; the map would freeze")
	}

	payload, _, _ := s.Current()
	var got fleetPayload
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("payload is not valid fleetPayload JSON: %v", err)
	}
	if len(got.Vehicles) != 1 || got.Vehicles[0].Lat != 3 || got.Vehicles[0].Lng != 4 {
		t.Fatalf("payload did not carry the new fix: %+v", got.Vehicles)
	}
	// The identity fields must survive a position-only update — they come from
	// the vehicles row, not from the ping.
	if got.Vehicles[0].Plate != "UAA 001" || got.Vehicles[0].Status != "moving" {
		t.Errorf("ping merge dropped identity fields: %+v", got.Vehicles[0])
	}
}

// A ping for a vehicle the snapshot has not loaded must not invent a row: the
// map would show an unlabelled marker with no plate or status.
func TestSnapshotIgnoresUnknownVehicle(t *testing.T) {
	s := newTestSnapshotter(map[string]fleetVehicleSnap{})
	_, before, _ := s.Current()
	s.applyPing(iot.Ping{VehicleID: "GHOST", Lat: 9, Lng: 9})
	if _, after, _ := s.Current(); after != before {
		t.Error("a ping for an unknown vehicle created a snapshot entry")
	}
}

// Subscribers get a coalesced wake-up, never a backlog: a slow reader must not
// build a queue of stale signals that it then drains one payload at a time.
func TestSnapshotSubscribersCoalesce(t *testing.T) {
	s := newTestSnapshotter(map[string]fleetVehicleSnap{
		"V1": {ID: "V1", Lat: 1, Lng: 1},
	})
	ch, unsubscribe := s.Subscribe()
	defer unsubscribe()

	for i := 0; i < 5; i++ {
		s.applyPing(iot.Ping{VehicleID: "V1", Lat: float64(i + 2), Lng: 1})
	}

	signals := 0
	for draining := true; draining; {
		select {
		case <-ch:
			signals++
		default:
			draining = false
		}
	}
	if signals != 1 {
		t.Errorf("got %d queued signals after 5 changes, want 1 coalesced wake-up", signals)
	}

	// And after waking, the subscriber sees the NEWEST state, not the first.
	payload, _, _ := s.Current()
	var got fleetPayload
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Vehicles[0].Lat != 6 {
		t.Errorf("coalesced wake-up exposed lat %v, want the latest (6)", got.Vehicles[0].Lat)
	}
}

// Unsubscribing must stop the signal, or a closed connection keeps the
// snapshotter writing into a channel nobody reads.
func TestSnapshotUnsubscribeStopsSignals(t *testing.T) {
	s := newTestSnapshotter(map[string]fleetVehicleSnap{"V1": {ID: "V1"}})
	ch, unsubscribe := s.Subscribe()
	unsubscribe()

	s.applyPing(iot.Ping{VehicleID: "V1", Lat: 5, Lng: 5})
	select {
	case <-ch:
		t.Error("received a signal after unsubscribing")
	default:
	}
}
