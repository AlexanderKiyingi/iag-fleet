package handlers

import (
	"context"
	"testing"

	"github.com/iag/fleet-tool/backend/internal/models"
	fleetstore "github.com/iag/fleet-tool/backend/internal/store"
	"github.com/iag/fleet-tool/backend/internal/testdb"
)

// seedPODTrip lays down the vehicle, driver and trip a POD needs. trip_pods
// carries a real foreign key to trips, so the trip is not optional scaffolding.
func seedPODTrip(t *testing.T, ctx context.Context, repo *fleetstore.Repository, label string) models.Trip {
	t.Helper()
	v := integrationVehicle(testID("VEH-"+label), "POD-"+label)
	if _, err := repo.Vehicles.Add(ctx, v); err != nil {
		t.Fatalf("seed vehicle: %v", err)
	}
	d := integrationDriver(testID("DRV-" + label))
	if _, err := repo.Drivers.Add(ctx, d); err != nil {
		t.Fatalf("seed driver: %v", err)
	}
	trip := models.Trip{
		ID: testID("TRIP-" + label), DriverID: d.ID, VehicleID: v.ID,
		Date: "2026-09-23", StartLocation: "Kampala", EndLocation: "Jinja",
		Status: "in-progress",
	}
	if _, err := repo.Trips.Add(ctx, trip); err != nil {
		t.Fatalf("seed trip: %v", err)
	}
	return trip
}

// A POD carries two links at once, and both have to survive a write.
//
// TripID is the movement it proves; ShipmentRef (0059) is the last-mile
// shipment it closes on the Logistics desk, which lives in another service and
// so cannot be a foreign key. Before 0059 there was only TripID, and that desk
// put its shipment reference there — which is what made every POD save fail,
// because a shipment reference is not a trip id.
//
// The read-back is the half worth asserting. A column that is written and then
// not selected is invisible to every caller while looking perfectly healthy
// from the write side: exactly how jmps.notes stayed lost through a green
// build, a green suite and a successful deploy.
func TestTripPODCarriesTripAndShipmentRef(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	ctx := context.Background()
	repo := fleetstore.NewRepository(pool)

	trip := seedPODTrip(t, ctx, repo, "REF")

	pod := models.TripPOD{
		ID: testID("POD-REF"), TripID: trip.ID, ShipmentRef: "SHP-0001",
		Reference: "POD-0001", Date: "2026-09-23", Customer: "Café Javas",
		ReceivedBy: "A. Nakato", ReceiverPhone: "+256700000001",
		Condition: "good", Status: "delivered", PhotoIDs: models.PhotoIDs{},
	}
	created, err := repo.TripPODs.Add(ctx, pod)
	if err != nil {
		t.Fatalf("create POD: %v", err)
	}
	if created.ShipmentRef != "SHP-0001" {
		t.Errorf("create echoed shipmentRef %q, want SHP-0001", created.ShipmentRef)
	}

	got, err := repo.TripPODs.Get(ctx, pod.ID)
	if err != nil {
		t.Fatalf("read POD back: %v", err)
	}
	if got.ShipmentRef != "SHP-0001" {
		t.Errorf("shipmentRef read back as %q, want SHP-0001 — the column is written but not selected", got.ShipmentRef)
	}
	if got.TripID != trip.ID {
		t.Errorf("tripId read back as %q, want %q", got.TripID, trip.ID)
	}
	if got.Customer != "Café Javas" || got.ReceivedBy != "A. Nakato" {
		t.Errorf("POD identity fields did not round-trip: %+v", got)
	}
}

// A POD with no last-mile shipment is still a valid POD — one recorded in
// Fleet, against a trip, with nothing on the Logistics desk to close. The
// column defaults to empty rather than NULL so the model's plain string reads
// it without a scan error.
func TestTripPODWithoutShipmentRef(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	ctx := context.Background()
	repo := fleetstore.NewRepository(pool)

	trip := seedPODTrip(t, ctx, repo, "BARE")

	pod := models.TripPOD{
		ID: testID("POD-BARE"), TripID: trip.ID, Date: "2026-09-23",
		ReceivedBy: "J. Okello", Condition: "good", Status: "delivered",
		PhotoIDs: models.PhotoIDs{},
	}
	if _, err := repo.TripPODs.Add(ctx, pod); err != nil {
		t.Fatalf("create POD without a shipment: %v", err)
	}
	got, err := repo.TripPODs.Get(ctx, pod.ID)
	if err != nil {
		t.Fatalf("read POD back: %v", err)
	}
	if got.ShipmentRef != "" {
		t.Errorf("shipmentRef = %q, want empty", got.ShipmentRef)
	}
}
