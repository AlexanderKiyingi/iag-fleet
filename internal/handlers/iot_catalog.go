package handlers

// What hardware this platform knows how to talk to.
//
// Devices were provisioned with a serial, a vehicle and a free-text `model`.
// Nothing recorded whether a unit was a GPS tracker or a fuel probe, and
// nothing recorded its brand at all — so "ST-901", "st901", "Sinotrack ST 901"
// and "" were four ways of describing the same hardware, and the only one the
// status-word decoder could key on was an exact match it rarely got.
//
// The fix is not a `brand` text box next to the `model` text box. It is a
// catalogue: the set of hardware the software actually has a decoder for,
// declared in code, with the protocol and listener each entry implies. That
// makes the operator's choice a selection rather than a spelling, lets the
// server reject hardware it cannot decode at registration instead of at 3am,
// and lets the onboarding screen show the right endpoint and the right SMS for
// whatever was chosen.
//
// It is code and not a table on purpose: this is a property of what the
// software can decode, not operator data. A row in a table can claim support
// that no decoder backs. Compare iot.SupportedCommandModels(), which reports
// the immobilise encoders that are actually registered rather than the models
// somebody hoped were.

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

// Device types. A tracker dials in and owns a serial; a fuel probe does not —
// it is wired into a tracker's IO and reaches us through that tracker's frames.
// Conflating them is what made `fuelIoId` look like a property every device had.
const (
	DeviceTypeTracker    = "gps_tracker"
	DeviceTypeFuelSensor = "fuel_sensor"
)

// BrandOther is the escape hatch. Hardware nobody has written a decoder for can
// still be recorded — it just cannot claim a protocol, and the operator is told
// what that costs rather than discovering it from an empty map.
const BrandOther = "Other"

// CatalogEntry is one supported piece of hardware.
type CatalogEntry struct {
	Type  string `json:"type"`
	Brand string `json:"brand"`
	Model string `json:"model"`
	// Protocol is what this hardware speaks on the wire, NOT what was last
	// observed from a particular unit (that is iot_devices.protocol, set on
	// connect — the two disagreeing is precisely how you spot a unit dialling
	// the wrong gateway).
	Protocol string `json:"protocol,omitempty"`
	// Listener names which endpoint the onboarding screen should show:
	// "sinotrack", "teltonika" (raw TCP) or "http" (the ingest relay).
	Listener string `json:"listener,omitempty"`
	// AttachesToTracker marks hardware with no network identity of its own.
	// A fuel probe is configured through its host tracker's IO mapping.
	AttachesToTracker bool     `json:"attachesToTracker,omitempty"`
	Capabilities      []string `json:"capabilities,omitempty"`
	Note              string   `json:"note,omitempty"`
}

// deviceCatalog is the whole supported set.
//
// Adding a row here is a claim that the ingest path decodes this hardware. If
// it does not, do not add it — record the unit as brand "Other" instead.
var deviceCatalog = []CatalogEntry{
	// ── SinoTrack / HQ protocol ──────────────────────────────────────────
	// One decoder covers the family: iot/hq.go keys on the *HQ,…# frame shape,
	// not on a model list, so every HQ-speaking unit works without code.
	{Type: DeviceTypeTracker, Brand: "SinoTrack", Model: "ST-901", Protocol: "hq", Listener: "sinotrack",
		Capabilities: []string{"position", "speed", "heading", "trips", "geofence"},
		Note:         "2G GSM/GPRS. No fuel sensor and no odometer in the protocol; distance is derived from GPS."},
	{Type: DeviceTypeTracker, Brand: "SinoTrack", Model: "ST-901L", Protocol: "hq", Listener: "sinotrack",
		Capabilities: []string{"position", "speed", "heading", "trips", "geofence"},
		Note:         "4G variant of the ST-901 — use this where 2G has been retired. Decoded identically."},
	{Type: DeviceTypeTracker, Brand: "SinoTrack", Model: "ST-901M", Protocol: "hq", Listener: "sinotrack",
		Capabilities: []string{"position", "speed", "heading", "trips", "geofence"},
		Note:         "4G variant of the ST-901. Decoded identically."},
	{Type: DeviceTypeTracker, Brand: "SinoTrack", Model: "ST-903", Protocol: "hq", Listener: "sinotrack",
		Capabilities: []string{"position", "speed", "heading", "trips", "geofence"}},
	{Type: DeviceTypeTracker, Brand: "SinoTrack", Model: "ST-906", Protocol: "hq", Listener: "sinotrack",
		Capabilities: []string{"position", "speed", "heading", "trips", "geofence"}},
	{Type: DeviceTypeTracker, Brand: "SinoTrack", Model: "ST-915", Protocol: "hq", Listener: "sinotrack",
		Capabilities: []string{"position", "speed", "heading", "trips", "geofence"}},

	// ── Teltonika / Codec 8 ──────────────────────────────────────────────
	{Type: DeviceTypeTracker, Brand: "Teltonika", Model: "FMB920", Protocol: "teltonika", Listener: "teltonika",
		Capabilities: []string{"position", "speed", "heading", "trips", "geofence", "ignition", "io"}},
	{Type: DeviceTypeTracker, Brand: "Teltonika", Model: "FMB930", Protocol: "teltonika", Listener: "teltonika",
		Capabilities: []string{"position", "speed", "heading", "trips", "geofence", "ignition", "io"}},
	{Type: DeviceTypeTracker, Brand: "Teltonika", Model: "FMC130", Protocol: "teltonika", Listener: "teltonika",
		Capabilities: []string{"position", "speed", "heading", "trips", "geofence", "ignition", "io", "fuel"},
		Note:         "CAN-capable; can carry a fuel reading when an IO element is mapped on the device."},
	{Type: DeviceTypeTracker, Brand: "Teltonika", Model: "FMB640", Protocol: "teltonika", Listener: "teltonika",
		Capabilities: []string{"position", "speed", "heading", "trips", "geofence", "ignition", "io", "fuel"}},

	// ── HTTP relay ───────────────────────────────────────────────────────
	// Anything that can POST JSON: a phone, a third-party platform's webhook,
	// a script. Authenticated by the device's own API key.
	{Type: DeviceTypeTracker, Brand: BrandOther, Model: "HTTP relay", Protocol: "http", Listener: "http",
		Capabilities: []string{"position", "speed", "heading", "trips", "geofence", "fuel", "ignition"},
		Note:         "Any client that can POST the ingest JSON. Issue an API key for it."},

	// ── Fuel probes ──────────────────────────────────────────────────────
	// These have no network identity: they are wired into a tracker's IO and
	// arrive inside that tracker's frames. Registering one records what is
	// fitted and how to decode it; the reading still comes through the host.
	{Type: DeviceTypeFuelSensor, Brand: "Technoton", Model: "DUT-E", AttachesToTracker: true,
		Capabilities: []string{"fuel"},
		Note:         "Capacitive probe. Map it on the host tracker with the fuel IO element, scale and offset."},
	{Type: DeviceTypeFuelSensor, Brand: "Omnicomm", Model: "LLS 20160", AttachesToTracker: true,
		Capabilities: []string{"fuel"},
		Note:         "Digital LLS probe reporting a raw count — set the scale against a known tank level."},
	{Type: DeviceTypeFuelSensor, Brand: "Escort", Model: "TD-BLE", AttachesToTracker: true,
		Capabilities: []string{"fuel"}},
	{Type: DeviceTypeFuelSensor, Brand: BrandOther, Model: "Analog sender", AttachesToTracker: true,
		Capabilities: []string{"fuel"},
		Note:         "Resistive/voltage sender. Reports millivolts; calibrate scale and offset in the vehicle."},
}

// catalogEntry finds hardware by brand and model, case-insensitively so an
// operator's capitalisation is not a validation failure.
func catalogEntry(brand, model string) (CatalogEntry, bool) {
	b, m := strings.TrimSpace(brand), strings.TrimSpace(model)
	for _, e := range deviceCatalog {
		if strings.EqualFold(e.Brand, b) && strings.EqualFold(e.Model, m) {
			return e, true
		}
	}
	return CatalogEntry{}, false
}

// validDeviceType reports whether a type string is one this platform models.
func validDeviceType(t string) bool {
	switch strings.TrimSpace(t) {
	case "", DeviceTypeTracker, DeviceTypeFuelSensor:
		return true
	}
	return false
}

// validateDeviceHardware checks a chosen type/brand/model against the catalogue.
//
// Deliberately permissive in one direction: brand "Other" accepts any model,
// because hardware the platform cannot decode still has to be recordable — an
// operator with an unsupported unit needs an inventory row, not a dead end.
// What it will not accept is a *named* brand with a model that brand does not
// have, which is the typo case, nor a type/brand pairing that contradicts the
// catalogue — a fuel probe recorded as a GPS tracker would be provisioned with
// a serial it can never dial in with.
func validateDeviceHardware(deviceType, brand, model string) error {
	if !validDeviceType(deviceType) {
		return fmt.Errorf("unknown device type %q — expected %s or %s",
			deviceType, DeviceTypeTracker, DeviceTypeFuelSensor)
	}
	brand, model = strings.TrimSpace(brand), strings.TrimSpace(model)
	if brand == "" || strings.EqualFold(brand, BrandOther) {
		return nil
	}
	entry, ok := catalogEntry(brand, model)
	if !ok {
		if !brandKnown(brand) {
			return fmt.Errorf("unknown brand %q — choose one of %s, or %q to record "+
				"hardware this platform has no decoder for",
				brand, strings.Join(knownBrands(), ", "), BrandOther)
		}
		return fmt.Errorf("%s has no model %q in the catalogue — known models are %s",
			brand, model, strings.Join(modelsForBrand(brand), ", "))
	}
	if deviceType != "" && entry.Type != deviceType {
		return fmt.Errorf("%s %s is a %s, not a %s", brand, model, entry.Type, deviceType)
	}
	return nil
}

func brandKnown(brand string) bool {
	for _, e := range deviceCatalog {
		if strings.EqualFold(e.Brand, brand) {
			return true
		}
	}
	return false
}

func knownBrands() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range deviceCatalog {
		if e.Brand == BrandOther || seen[e.Brand] {
			continue
		}
		seen[e.Brand] = true
		out = append(out, e.Brand)
	}
	sort.Strings(out)
	return append(out, BrandOther)
}

func modelsForBrand(brand string) []string {
	var out []string
	for _, e := range deviceCatalog {
		if strings.EqualFold(e.Brand, brand) {
			out = append(out, e.Model)
		}
	}
	sort.Strings(out)
	return out
}

// catalogProtocol returns the wire protocol a chosen brand/model implies, so a
// device row records it at registration instead of staying empty until the unit
// first connects.
func catalogProtocol(brand, model string) string {
	if e, ok := catalogEntry(brand, model); ok {
		return e.Protocol
	}
	return ""
}

// deviceCatalogHandler serves the taxonomy the operator UI builds its
// type → brand → model selects from.
//
// Served rather than duplicated in the client because the catalogue is a claim
// about what the ingest path decodes; a copy in the frontend would drift, and
// the direction it drifts is a UI offering hardware the server rejects.
func (h *IoT) deviceCatalog(c *gin.Context) {
	types := []gin.H{
		{
			"value": DeviceTypeTracker,
			"label": "GPS tracker",
			"help":  "Dials in over the network and owns a serial. Register it, then point it at the listener for its protocol.",
		},
		{
			"value": DeviceTypeFuelSensor,
			"label": "Fuel sensor",
			"help":  "Wired into a tracker's IO — it has no network identity of its own. Readings arrive inside the host tracker's frames, so map the fuel IO element, scale and offset on that tracker.",
		},
	}

	byBrand := map[string][]CatalogEntry{}
	for _, e := range deviceCatalog {
		byBrand[e.Brand] = append(byBrand[e.Brand], e)
	}
	brands := make([]gin.H, 0, len(byBrand))
	for _, name := range knownBrands() {
		entries := byBrand[name]
		models := make([]gin.H, 0, len(entries))
		for _, e := range entries {
			models = append(models, gin.H{
				"model":             e.Model,
				"type":              e.Type,
				"protocol":          e.Protocol,
				"listener":          e.Listener,
				"attachesToTracker": e.AttachesToTracker,
				"capabilities":      e.Capabilities,
				"note":              e.Note,
			})
		}
		brands = append(brands, gin.H{"brand": name, "models": models})
	}

	c.JSON(http.StatusOK, gin.H{
		"types":  types,
		"brands": brands,
		"other": gin.H{
			"brand": BrandOther,
			"note": "Record hardware with no decoder here. It will store and report, " +
				"but nothing will parse its frames until a decoder exists.",
		},
		// The listener each model points at, so a screen can resolve
		// model → endpoint → SMS without a second call.
		"listeners": tcpIngestionBlock(),
	})
}

// hardwarePatchIsValid validates a PATCH against the row it will produce.
//
// A PATCH may carry only the brand, only the model, or only the type, so
// checking the body alone would let "change brand to Teltonika" land on a row
// whose model is still ST-901 — a pairing the catalogue would have rejected on
// create. Merging first is the difference between validating an edit and
// validating a form.
//
// Reports through the context and returns false when it has already answered.
func (h *IoT) hardwarePatchIsValid(c *gin.Context, id int64, deviceType, brand, model *string) bool {
	if deviceType == nil && brand == nil && model == nil {
		return true // nothing hardware-related changed
	}
	current, err := h.Store.GetDevice(c.Request.Context(), id)
	if err != nil {
		respondIotError(c, err)
		return false
	}
	merged := func(patch *string, existing string) string {
		if patch != nil {
			return *patch
		}
		return existing
	}
	if err := validateDeviceHardware(
		merged(deviceType, current.DeviceType),
		merged(brand, current.Brand),
		merged(model, current.Model),
	); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return false
	}
	return true
}
