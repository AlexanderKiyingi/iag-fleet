package models

// CargoStages is the ordered cargo lifecycle. Index position drives `advance`
// (moving to the next stage). Mirrors lib/data/cargo.ts STAGES.
var CargoStages = []struct {
	K     string `json:"k"`
	Label string `json:"label"`
}{
	{"at-mombasa", "Mombasa Port"},
	{"in-transit-malaba", "In transit"},
	{"at-malaba", "URA Malaba"},
	{"in-transit-kampala", "In transit"},
	{"at-kampala", "Kampala"},
	{"in-transit-acp", "In transit"},
	{"at-acp", "ACP — pending offload"},
	{"offloaded", "Offloaded"},
	{"demobilised", "Demobilised"},
	{"completed", "Completed"},
}

// Departments mirrors lib/types.ts DEPARTMENTS.
var Departments = []string{
	"Agronomy", "Procurement", "Quality Assurance", "Construction",
	"Human Resources", "Finance", "Operations", "IT", "Security", "Other",
}

var SafetyTypes = []string{
	"Near-miss", "Accident", "Mechanical failure", "Speeding",
	"Driver behaviour", "Harsh braking", "Cargo damage",
}

var ComplianceDocTypes = []string{
	"Driving Permit", "First Aid", "Defensive Driving", "Medical",
	"PSV Insurance", "Annual Inspection", "COMESA Yellow Card",
}

var PreferredVehicleTypes = []string{
	"Any", "Pickup", "SUV", "Light Truck", "Heavy Truck", "Mini-van", "Tipper",
}

var FuelStations = []string{
	"TUSU Filling Station", "SHELL", "SHELL · Eldoret", "TOTAL Energies", "Stabex", "Hass",
}

// VehicleStatuses, JmpStatuses etc are exposed for clients that want a
// canonical list (dropdowns, validation).
var VehicleStatuses = []string{"moving", "idle", "maintenance", "offline"}

var InspectionKinds = []string{"pre-trip", "post-trip", "periodic"}
var InspectionStatuses = []string{"draft", "submitted", "passed", "failed"}
var InspectionItemStatuses = []string{"pass", "fail", "na"}
var PMServiceTypes = []string{"Service", "Inspection", "Tyres", "Brakes", "Engine", "Repair"}
var JmpStatuses = []string{"draft", "pending-toolbox", "active", "completed", "cancelled"}
var MileageStatuses = []string{"Pending", "Submitted", "Approved", "Rejected", "Disbursed"}
var RequestStatuses = []string{"submitted", "reviewed", "approved", "assigned", "rejected", "completed"}
var TaskStates = []string{"open", "in-review", "in-progress", "done"}
var DeploymentMechStatuses = []string{"operational", "minor-issue", "in-service", "out-of-service", "grounded"}
var DeploymentStatuses = []string{"deployed", "idle", "under-repair", "demobilised"}

type POI struct {
	Name        string  `json:"name"`
	Lat         float64 `json:"lat"`
	Lng         float64 `json:"lng"`
	Type        string  `json:"type"` // iag | port | hub | border
	GeofenceKm  float64 `json:"geofenceKm,omitempty"`
}

type Corridor struct {
	Name  string       `json:"name"`
	Color string       `json:"color"`
	Path  [][2]float64 `json:"path"`
}

type Basemap struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	URL  string `json:"url"`
	Sub  string `json:"sub,omitempty"`
	Attr string `json:"attr"`
}

// POIs mirrors lib/data/geo.ts POIS.
var POIs = []POI{
	{"Africa Coffee Park (ACP)", -0.880, 30.265, "iag", 0.6},
	{"Rwashamaire Estate", -0.814, 30.067, "iag", 0.4},
	{"IAG Kampala HQ", 0.327, 32.591, "iag", 0.3},
	{"Mombasa Port", -4.050, 39.667, "port", 1.5},
	{"Dar es Salaam Port", -6.792, 39.208, "port", 1.5},
	{"Nairobi", -1.286, 36.817, "hub", 0},
	{"Mbarara", -0.607, 30.658, "hub", 0},
	{"Juba", 4.853, 31.583, "hub", 0},
	{"Malaba Border (URA)", 0.637, 34.265, "border", 0.5},
	{"Busia Border", 0.456, 34.107, "border", 0},
	{"Mutukula", -1.015, 31.398, "border", 0},
}

var Corridors = []Corridor{
	{"ACP → Mombasa", "#d4791f", [][2]float64{
		{-0.880, 30.265}, {-0.607, 30.658}, {0.347, 32.582}, {0.440, 33.204},
		{0.637, 34.265}, {0.520, 35.270}, {-0.717, 36.431}, {-1.286, 36.817},
		{-2.983, 38.467}, {-4.050, 39.667},
	}},
	{"ACP → Dar", "#2a6e6a", [][2]float64{
		{-0.880, 30.265}, {-0.607, 30.658}, {-1.015, 31.398}, {-3.367, 36.683}, {-6.792, 39.208},
	}},
	{"ACP → Juba", "#5a3a7a", [][2]float64{
		{-0.880, 30.265}, {0.347, 32.582}, {3.603, 32.069}, {4.853, 31.583},
	}},
	{"ACP → Nairobi", "#8b4513", [][2]float64{
		{-0.880, 30.265}, {0.347, 32.582}, {0.456, 34.107}, {-1.286, 36.817},
	}},
}

var Basemaps = []Basemap{
	{"dark", "Dark", "https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png", "abcd", "&copy; CARTO &copy; OSM"},
	{"voyager", "Voyager", "https://{s}.basemaps.cartocdn.com/rastertiles/voyager/{z}/{x}/{y}{r}.png", "abcd", "&copy; CARTO &copy; OSM"},
	{"satellite", "Satellite", "https://server.arcgisonline.com/ArcGIS/rest/services/World_Imagery/MapServer/tile/{z}/{y}/{x}", "", "&copy; Esri"},
	{"topo", "Topo", "https://{s}.tile.opentopomap.org/{z}/{x}/{y}.png", "abc", "&copy; OpenTopoMap"},
}
