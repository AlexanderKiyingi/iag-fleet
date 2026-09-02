package handlers

import (
	"encoding/json"
	"testing"
)

// The operator UI has always sent `model` on device create and update — see the
// iotDevices adapter's fromRecord in the frontend — and this struct had no such
// field, so gin bound the request, dropped it, and returned 201. Nothing failed
// and nothing logged.
//
// The consequences were both silent: iot_devices.model stayed empty, which is
// what keys the HQ status-word bit map and the immobilise command encoder, so
// alarms were never decoded and EncodeCommand refused every command with
// ErrNoEncoder — for every device, however carefully it was provisioned.
//
// A struct field is exactly what was missing, so a test that asserts the field
// binds is the regression guard that would have caught it. It is not a
// substitute for exercising the write against the service, which needs a
// database; see docs/TELEMETRY_INGRESS.md for the live walkthrough.
func TestCreateDeviceBodyBindsModelAndFuelSensor(t *testing.T) {
	const payload = `{
		"serial": "0123456789",
		"label": "cab unit",
		"vehicleId": "V1",
		"issueKey": true,
		"model": "ST-901",
		"fuelIoId": 201,
		"fuelScale": 0.0244200244,
		"fuelOffset": -1.5
	}`

	var body createDeviceBody
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if body.Model != "ST-901" {
		t.Errorf("model = %q, want ST-901 — the field the UI sends is being dropped again", body.Model)
	}
	if body.FuelIOID == nil || *body.FuelIOID != 201 {
		t.Errorf("fuelIoId = %v, want 201", body.FuelIOID)
	}
	if body.FuelScale == nil || *body.FuelScale != 0.0244200244 {
		t.Errorf("fuelScale = %v, want 0.0244200244", body.FuelScale)
	}
	if body.FuelOffset == nil || *body.FuelOffset != -1.5 {
		t.Errorf("fuelOffset = %v, want -1.5", body.FuelOffset)
	}
}

// Omitted fuel fields must arrive as nil, not zero. The store distinguishes the
// two: nil keeps the column default (the Teltonika CAN-percent behaviour every
// existing device has), while a zero scale is a value the CHECK rejects.
func TestCreateDeviceBodyOmittedFuelFieldsAreNil(t *testing.T) {
	var body createDeviceBody
	if err := json.Unmarshal([]byte(`{"serial":"1"}`), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.FuelIOID != nil || body.FuelScale != nil || body.FuelOffset != nil {
		t.Errorf("omitted fuel fields did not stay nil: io=%v scale=%v offset=%v",
			body.FuelIOID, body.FuelScale, body.FuelOffset)
	}
	if body.Model != "" {
		t.Errorf("model = %q, want empty", body.Model)
	}
}

func TestUpdateDeviceBodyBindsModelAndFuelSensor(t *testing.T) {
	var body updateDeviceBody
	if err := json.Unmarshal([]byte(`{"model":"FMB920","fuelIoId":9,"fuelScale":0.025}`), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Model == nil || *body.Model != "FMB920" {
		t.Errorf("model = %v, want FMB920", body.Model)
	}
	if body.FuelIOID == nil || *body.FuelIOID != 9 {
		t.Errorf("fuelIoId = %v, want 9", body.FuelIOID)
	}
	// Not sent, so it must stay nil — PATCH semantics: leave the column alone.
	if body.FuelOffset != nil {
		t.Errorf("fuelOffset = %v, want nil for an unsent field", body.FuelOffset)
	}
}

// A negative or zero scale inverts or flattens the reading. The fuel-event
// detector works on deltas between consecutive readings, so an inverted sensor
// reports every refuel as a siphoning alert — worth a sentence rather than a
// raw constraint violation from Postgres.
func TestValidateFuelSensorRejectsNonPositiveScale(t *testing.T) {
	for _, bad := range []float64{0, -0.1, -1} {
		if err := validateFuelSensor(&bad); err == nil {
			t.Errorf("scale %v was accepted", bad)
		}
	}
	ok := 0.025
	if err := validateFuelSensor(&ok); err != nil {
		t.Errorf("valid scale rejected: %v", err)
	}
	if err := validateFuelSensor(nil); err != nil {
		t.Errorf("omitted scale rejected: %v", err)
	}
}
