package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/iag/fleet-tool/backend/internal/events"
	"github.com/iag/fleet-iot/iot"
)

func publishTelemetryFuelEvents(ctx context.Context, bus *events.Bus, fuelEvents []iot.FuelEvent) {
	if bus == nil || !bus.Enabled() || len(fuelEvents) == 0 {
		return
	}
	for _, ev := range fuelEvents {
		data := events.FleetEventData(map[string]string{
			"vehicleId":  ev.VehicleID,
			"kind":       ev.Kind,
			"ts":         ev.TS.UTC().Format(time.RFC3339),
			"deltaPct":   fmt.Sprintf("%.2f", ev.DeltaPct),
			"beforePct":  fmt.Sprintf("%.2f", ev.BeforePct),
			"afterPct":   fmt.Sprintf("%.2f", ev.AfterPct),
			"confidence": ev.Confidence,
			"notes":      ev.Notes,
		})
		if ev.DeltaLitres != nil {
			data["deltaLitres"] = fmt.Sprintf("%.2f", *ev.DeltaLitres)
		}
		key := fmt.Sprintf("%s:%d", ev.VehicleID, ev.TS.UTC().Unix())

		switch {
		case ev.Kind == "refuel" && ev.Confidence != "low":
			bus.PublishFleet(ctx, events.TypeTelemetryRefuelDetected, data, key, key)
		case ev.Kind == "drop" || (ev.Kind == "refuel" && ev.Confidence == "low"):
			bus.PublishFleet(ctx, events.TypeTelemetryFuelAnomaly, data, key, key)
		}
	}
}
