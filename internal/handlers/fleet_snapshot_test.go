package handlers

import (
	"encoding/json"
	"testing"
	"time"

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

// The live map draws "42 km/h, 3 min ago" beside each marker. Both fields used
// to be missing from this payload, so a client driven by the stream moved a dot
// while its label came from a separate poll of /api/vehicles — a marker that
// moves under a stale timestamp reads as a bug. The ping carries both, so the
// frame must too.
func TestSnapshotPingCarriesSpeedAndLastSeen(t *testing.T) {
	s := newTestSnapshotter(map[string]fleetVehicleSnap{
		"V1": {ID: "V1", Plate: "UAA 001", Lat: 1, Lng: 2, Status: "moving"},
	})

	speed := 42.5
	reported := time.Date(2026, 9, 1, 8, 30, 15, 250_000_000, time.UTC)
	s.applyPing(iot.Ping{VehicleID: "V1", Lat: 3, Lng: 4, TS: reported, SpeedKmh: &speed})

	payload, _, _ := s.Current()
	var got fleetPayload
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Vehicles[0].Speed != speed {
		t.Errorf("speed = %v, want %v", got.Vehicles[0].Speed, speed)
	}
	// Must match how the store formats timestamptz columns, or the shape of the
	// field changes between a ping-driven frame and a refresh-driven one and
	// the client's Date.parse gets an inconsistent input.
	const want = "2026-09-01T08:30:15.250Z"
	if got.Vehicles[0].LastSeen != want {
		t.Errorf("lastSeen = %q, want %q", got.Vehicles[0].LastSeen, want)
	}
}

// A ping that moves nothing but the speed is still a change worth publishing —
// a truck slowing from 80 to 0 at the same coordinates is exactly the moment an
// operator cares about.
func TestSnapshotSpeedChangeAlonePublishes(t *testing.T) {
	s := newTestSnapshotter(map[string]fleetVehicleSnap{
		"V1": {ID: "V1", Lat: 1, Lng: 2, Speed: 80},
	})
	_, before, _ := s.Current()

	stopped := 0.0
	s.applyPing(iot.Ping{VehicleID: "V1", Lat: 1, Lng: 2, SpeedKmh: &stopped})

	if _, after, _ := s.Current(); after == before {
		t.Error("a vehicle that stopped at the same coordinates did not publish a new version")
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

// last_seen is rewritten by every ping. Comparing it exactly would make a
// parked-but-still-reporting fleet republish the whole vehicle array on every
// tick, which is the churn the version check exists to prevent.
func TestSnapshotLastSeenChurnDoesNotPublish(t *testing.T) {
	s := newTestSnapshotter(map[string]fleetVehicleSnap{
		"V1": {ID: "V1", Lat: 1, Lng: 2, LastSeen: "2026-09-01T08:00:00.000Z"},
	})
	_, before, _ := s.Current()

	// Same position, ten seconds later: the truck is parked with a live tracker.
	s.applyPing(iot.Ping{
		VehicleID: "V1", Lat: 1, Lng: 2,
		TS: time.Date(2026, 9, 1, 8, 0, 10, 0, time.UTC),
	})
	if _, after, _ := s.Current(); after != before {
		t.Error("a ten-second last_seen step published a frame; a parked fleet would stream continuously")
	}
}

// The flip side: last_seen must not freeze. Once the accumulated drift passes
// the granularity window it has to publish, or "3 min ago" stops advancing on a
// vehicle that is reporting perfectly well.
func TestSnapshotLastSeenPublishesOncePastGranularity(t *testing.T) {
	s := newTestSnapshotter(map[string]fleetVehicleSnap{
		"V1": {ID: "V1", Lat: 1, Lng: 2, LastSeen: "2026-09-01T08:00:00.000Z"},
	})
	_, before, _ := s.Current()

	base := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	// Ten-second steps, none of which crosses the window on its own. Comparing
	// against the last SEEN value rather than the last PUBLISHED one would
	// never cross it and last_seen would be stuck forever.
	for i := 1; i <= 5; i++ {
		s.applyPing(iot.Ping{
			VehicleID: "V1", Lat: 1, Lng: 2,
			TS: base.Add(time.Duration(i*10) * time.Second),
		})
	}

	payload, after, _ := s.Current()
	if after == before {
		t.Fatal("last_seen never published; the age label would freeze on a reporting vehicle")
	}
	var got fleetPayload
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Vehicles[0].LastSeen != "2026-09-01T08:00:40.000Z" {
		t.Errorf("published lastSeen = %q, want the first step past the window", got.Vehicles[0].LastSeen)
	}
}

// A move is a move regardless of how little time passed — the granularity
// applies to last_seen alone, never to position.
func TestSnapshotPositionChangePublishesImmediately(t *testing.T) {
	s := newTestSnapshotter(map[string]fleetVehicleSnap{
		"V1": {ID: "V1", Lat: 1, Lng: 2, LastSeen: "2026-09-01T08:00:00.000Z"},
	})
	_, before, _ := s.Current()

	s.applyPing(iot.Ping{
		VehicleID: "V1", Lat: 1.001, Lng: 2,
		TS: time.Date(2026, 9, 1, 8, 0, 1, 0, time.UTC),
	})
	if _, after, _ := s.Current(); after == before {
		t.Error("a vehicle that moved one second after the last fix did not publish")
	}
}

// A vehicle reporting for the first time has no previous timestamp at all.
// That is a change, not a sub-threshold step to absorb.
func TestSnapshotFirstEverFixPublishes(t *testing.T) {
	s := newTestSnapshotter(map[string]fleetVehicleSnap{
		"V1": {ID: "V1", Lat: 1, Lng: 2, LastSeen: ""},
	})
	_, before, _ := s.Current()

	s.applyPing(iot.Ping{
		VehicleID: "V1", Lat: 1, Lng: 2,
		TS: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC),
	})
	if _, after, _ := s.Current(); after == before {
		t.Error("the first fix on a never-reported vehicle did not publish")
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
