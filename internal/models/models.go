// Package models contains domain types mirroring the frontend's lib/types.ts.
//
// Field tags carry both `json:"..."` (for the wire format the Next.js client
// consumes) and `db:"..."` (for the Postgres column the row maps to). The
// store layer reads `db` tags reflectively to derive SELECT/INSERT/UPDATE
// statements. The order of fields in each struct matches the order of
// columns in db/schema.sql so SELECTs return rows that scan cleanly.
//
// Date and timestamp fields are kept as Go strings; the store casts the
// underlying DATE / TIMESTAMPTZ columns to text on SELECT (preserving
// "YYYY-MM-DD" / RFC3339 formatting) and lets Postgres parse the strings
// back on INSERT. This avoids a sweeping time.Time refactor and keeps the
// JSON the frontend already consumes byte-identical.
package models

type Driver struct {
	ID                string  `json:"id"                          db:"id"`
	Name              string  `json:"name"                        db:"name"`
	Initials          string  `json:"initials"                    db:"initials"`
	External          bool    `json:"external"                    db:"external"`
	Transporter       string  `json:"transporter,omitempty"       db:"transporter"`
	Role              string  `json:"role"                        db:"role"`
	Phone             string  `json:"phone"                       db:"phone"`
	Email             string  `json:"email,omitempty"             db:"email"`
	PermitNo          string  `json:"permitNo"                    db:"permit_no"`
	PermitClass       string  `json:"permitClass"                 db:"permit_class"`
	PermitExpiry      string  `json:"permitExpiry"                db:"permit_expiry"     dbcast:"date"`
	FirstAid          bool    `json:"firstAid"                    db:"first_aid"`
	FirstAidExpiry    string  `json:"firstAidExpiry,omitempty"    db:"first_aid_expiry"  dbcast:"date"`
	Defensive         bool    `json:"defensive"                   db:"defensive"`
	DefensiveExpiry   string  `json:"defensiveExpiry,omitempty"   db:"defensive_expiry"  dbcast:"date"`
	MedicalExpiry     string  `json:"medicalExpiry,omitempty"     db:"medical_expiry"    dbcast:"date"`
	YearsExp          int     `json:"yearsExp"                    db:"years_exp"`
	VehicleID         string  `json:"vehicleId,omitempty"         db:"vehicle_id"`
	CurrentAssignment string  `json:"currentAssignment,omitempty" db:"current_assignment"`
	HomeRegion        string  `json:"homeRegion"                  db:"home_region"`
	Rating            float64 `json:"rating"                      db:"rating"`
	SafetyScore       float64 `json:"safetyScore"                 db:"safety_score"`
	TripCount         *int    `json:"tripCount,omitempty"         db:"trip_count"`
	ViolationCount    *int    `json:"violationCount,omitempty"    db:"violation_count"`
	Status            string  `json:"status"                      db:"status"`
	Notes             string  `json:"notes,omitempty"             db:"notes"`
}

func (d Driver) GetID() string    { return d.ID }
func (d *Driver) SetID(id string) { d.ID = id }

type Vehicle struct {
	ID                 string   `json:"id"                       db:"id"`
	Plate              string   `json:"plate"                    db:"plate"`
	Type               string   `json:"type"                     db:"type"`
	Make               string   `json:"make"                     db:"make"`
	Model              string   `json:"model"                    db:"model"`
	Year               int      `json:"year"                     db:"year"`
	VehicleClass       string   `json:"vehicleClass"             db:"vehicle_class"`
	Ownership          string   `json:"ownership"                db:"ownership"`
	Vin                string   `json:"vin,omitempty"            db:"vin"`
	Color              string   `json:"color,omitempty"          db:"color"`
	SeatCapacity       *int     `json:"seatCapacity,omitempty"   db:"seat_capacity"`
	Transmission       string   `json:"transmission,omitempty"   db:"transmission"`
	EngineCapacity     string   `json:"engineCapacity,omitempty" db:"engine_capacity"`
	DriveHand          string   `json:"driveHand,omitempty"      db:"drive_hand"`
	PurchaseDate       string   `json:"purchaseDate,omitempty"   db:"purchase_date" dbcast:"date"`
	Mileage            *float64 `json:"mileage,omitempty"        db:"mileage"`
	DriverID           string   `json:"driverId,omitempty"       db:"driver_id"`
	Status             string   `json:"status"                   db:"status"`
	Location           string   `json:"location"                 db:"location"`
	Lat                float64  `json:"lat"                      db:"lat"`
	Lng                float64  `json:"lng"                      db:"lng"`
	Heading            float64  `json:"heading"                  db:"heading"`
	Fuel               float64  `json:"fuel"                     db:"fuel"`
	Odo                float64  `json:"odo"                      db:"odo"`
	Capacity           string   `json:"capacity"                 db:"capacity"`
	Cargo              string   `json:"cargo,omitempty"          db:"cargo"`
	LastSeen           string   `json:"lastSeen"                 db:"last_seen"        dbcast:"timestamptz"`
	Telematics         string   `json:"telematics,omitempty"     db:"telematics"`
	FuelTracker        bool     `json:"fuelTracker"              db:"fuel_tracker"`
	Dashcam            *bool    `json:"dashcam,omitempty"        db:"dashcam"`
	NextServiceKm      float64  `json:"nextServiceKm"            db:"next_service_km"`
	Speed              float64  `json:"speed"                    db:"speed"`
	EngineHours        *float64 `json:"engineHours,omitempty"    db:"engine_hours"`
	Purpose            string   `json:"purpose,omitempty"        db:"purpose"`
	MechStatus         string   `json:"mechStatus"               db:"mech_status"`
	Alert              string   `json:"alert,omitempty"          db:"alert"`
	TankCapacityLitres *int     `json:"tankCapacityLitres,omitempty" db:"tank_capacity_litres"`
	// CostCenter is the finance bucket warehouse stock issues raised on this
	// vehicle's maintenance WOs are costed to. Optional.
	CostCenter string `json:"costCenter,omitempty" db:"cost_center"`
}

func (v Vehicle) GetID() string    { return v.ID }
func (v *Vehicle) SetID(id string) { v.ID = id }

type ToolboxItems struct {
	NoDrunkDriving    *bool `json:"noDrunkDriving,omitempty"`
	SpeedLimits       *bool `json:"speedLimits,omitempty"`
	CargoInspection   *bool `json:"cargoInspection,omitempty"`
	Communication     *bool `json:"communication,omitempty"`
	FatigueManagement *bool `json:"fatigueManagement,omitempty"`
	IncidentContacts  *bool `json:"incidentContacts,omitempty"`
	RouteReviewed     *bool `json:"routeReviewed,omitempty"`
	ParkingConfirmed  *bool `json:"parkingConfirmed,omitempty"`
}

// Toolbox is a single JSONB column on jmps. Scanner/Valuer in jsonb.go.
type Toolbox struct {
	Completed   bool         `json:"completed"`
	CompletedAt string       `json:"completedAt,omitempty"`
	Facilitator string       `json:"facilitator,omitempty"`
	Items       ToolboxItems `json:"items"`
}

type JMP struct {
	ID                string   `json:"id"                       db:"id"`
	VehicleID         string   `json:"vehicleId"                db:"vehicle_id"`
	DriverID          string   `json:"driverId"                 db:"driver_id"`
	Purpose           string   `json:"purpose"                  db:"purpose"`
	CargoDescription  string   `json:"cargoDescription"         db:"cargo_description"`
	StartDate         string   `json:"startDate"                db:"start_date"        dbcast:"date"`
	ExpectedArrival   string   `json:"expectedArrival"          db:"expected_arrival"  dbcast:"date"`
	DesignatedParking string   `json:"designatedParking"        db:"designated_parking"`
	RouteSummary      string   `json:"routeSummary"             db:"route_summary"`
	RouteDetail       string   `json:"routeDetail"              db:"route_detail"`
	ExpectedDays      int      `json:"expectedDays"             db:"expected_days"`
	ExpectedReturn    string   `json:"expectedReturn"           db:"expected_return"   dbcast:"date"`
	DistanceKm        float64  `json:"distanceKm"               db:"distance_km"`
	FuelEstimateL     float64  `json:"fuelEstimateL"            db:"fuel_estimate_l"`
	MileageRequest    float64  `json:"mileageRequest"           db:"mileage_request"`
	MileageStatus     string   `json:"mileageStatus"            db:"mileage_status"`
	TotalBudgetUgx    float64  `json:"totalBudgetUgx"           db:"total_budget_ugx"`
	Toolbox           Toolbox  `json:"toolbox"                  db:"toolbox"`
	FatiguePlan       string   `json:"fatiguePlan"              db:"fatigue_plan"`
	IncidentContacts  string   `json:"incidentContacts"         db:"incident_contacts"`
	ConvoyPartner     string   `json:"convoyPartner"            db:"convoy_partner"`
	Status            string   `json:"status"                   db:"status"`
	CreatedAt         string   `json:"createdAt"                db:"created_at"        dbcast:"timestamptz"`
	CreatedBy         string   `json:"createdBy"                db:"created_by"`
	ApprovedBy        string   `json:"approvedBy,omitempty"     db:"approved_by"`
	ApprovedAt        string   `json:"approvedAt,omitempty"     db:"approved_at"       dbcast:"timestamptz"`
	CompletedAt       string   `json:"completedAt,omitempty"    db:"completed_at"      dbcast:"timestamptz"`
	ParkingPhotos     []string `json:"parkingPhotos"            db:"parking_photos"`
	SourceRequestID   string   `json:"sourceRequestId,omitempty" db:"source_request_id"`
	// DispatchStatus is the pre-trip dispatch approval gate (Pending/Approved/
	// Rejected), distinct from MileageStatus (the post-trip mileage approval).
	// Stamped "Pending" at creation; cleared (empty) on historical rows.
	DispatchStatus     string `json:"dispatchStatus,omitempty"     db:"dispatch_status"`
	DispatchApprovedBy string `json:"dispatchApprovedBy,omitempty" db:"dispatch_approved_by"`
	DispatchApprovedAt string `json:"dispatchApprovedAt,omitempty" db:"dispatch_approved_at" dbcast:"timestamptz"`
}

func (j JMP) GetID() string    { return j.ID }
func (j *JMP) SetID(id string) { j.ID = id }

type CargoStageEvent struct {
	Stage string `json:"stage"`
	At    string `json:"at"`
	By    string `json:"by,omitempty"`
	Note  string `json:"note,omitempty"`
}

// CargoStageHistory is a slice with Scanner/Valuer to round-trip through
// the cargo.stage_history JSONB column.
type CargoStageHistory []CargoStageEvent

type Cargo struct {
	ID               string            `json:"id"                          db:"id"`
	Convoy           string            `json:"convoy"                      db:"convoy"`
	TruckPlate       string            `json:"truckPlate"                  db:"truck_plate"`
	DriverName       string            `json:"driverName"                  db:"driver_name"`
	DriverPhone      string            `json:"driverPhone"                 db:"driver_phone"`
	Transporter      string            `json:"transporter"                 db:"transporter"`
	CargoNature      string            `json:"cargoNature"                 db:"cargo_nature"`
	Container        string            `json:"container"                   db:"container"`
	DepartureMombasa string            `json:"departureMombasa,omitempty"  db:"departure_mombasa" dbcast:"date"`
	DepartureMalaba  string            `json:"departureMalaba,omitempty"   db:"departure_malaba"  dbcast:"date"`
	DepartureKampala string            `json:"departureKampala,omitempty"  db:"departure_kampala" dbcast:"date"`
	ArrivalAcp       string            `json:"arrivalAcp,omitempty"        db:"arrival_acp"       dbcast:"date"`
	OffloadingDate   string            `json:"offloadingDate,omitempty"    db:"offloading_date"   dbcast:"date"`
	Stage            string            `json:"stage"                       db:"stage"`
	Urgency          string            `json:"urgency"                     db:"urgency"`
	Demobilised      *bool             `json:"demobilised,omitempty"       db:"demobilised"`
	DemobilisedAt    string            `json:"demobilisedAt,omitempty"     db:"demobilised_at"    dbcast:"timestamptz"`
	Remarks          string            `json:"remarks,omitempty"           db:"remarks"`
	CreatedAt        string            `json:"createdAt"                   db:"created_at"        dbcast:"timestamptz"`
	StageHistory     CargoStageHistory `json:"stageHistory"                db:"stage_history"`
}

func (c Cargo) GetID() string    { return c.ID }
func (c *Cargo) SetID(id string) { c.ID = id }

type CargoDoc struct {
	ID      string `json:"id"                db:"id"`
	CargoID string `json:"cargoId"           db:"cargo_id"`
	DocType string `json:"docType"           db:"doc_type"`
	DocNo   string `json:"docNo,omitempty"   db:"doc_no"`
	Issued  string `json:"issued,omitempty"  db:"issued"  dbcast:"date"`
	Expiry  string `json:"expiry,omitempty"  db:"expiry"  dbcast:"date"`
	Issuer  string `json:"issuer,omitempty"  db:"issuer"`
	Notes   string `json:"notes,omitempty"   db:"notes"`
}

func (c CargoDoc) GetID() string    { return c.ID }
func (c *CargoDoc) SetID(id string) { c.ID = id }

type AnomalyHistoryEvent struct {
	At    string `json:"at"`
	Event string `json:"event"` // flagged | investigated | resolved | dismissed
	By    string `json:"by,omitempty"`
	Note  string `json:"note,omitempty"`
}

type AnomalyHistory []AnomalyHistoryEvent

type FuelRecord struct {
	ID             string         `json:"id"                       db:"id"`
	VehicleID      string         `json:"vehicleId"                db:"vehicle_id"`
	DriverID       string         `json:"driverId,omitempty"       db:"driver_id"`
	Date           string         `json:"date"                     db:"date"   dbcast:"date"`
	Litres         float64        `json:"litres"                   db:"litres"`
	UnitPrice      float64        `json:"unitPrice"                db:"unit_price"`
	Total          float64        `json:"total"                    db:"total"`
	Station        string         `json:"station"                  db:"station"`
	Invoice        string         `json:"invoice,omitempty"        db:"invoice"`
	Odo            float64        `json:"odo"                      db:"odo"`
	Notes          string         `json:"notes,omitempty"          db:"notes"`
	PaymentMethod  string         `json:"paymentMethod,omitempty"  db:"payment_method"`
	Attendant      string         `json:"attendant,omitempty"      db:"attendant"`
	CardLast4      string         `json:"cardLast4,omitempty"      db:"card_last4"`
	Anomaly        *bool          `json:"anomaly,omitempty"        db:"anomaly"`
	AnomalyReason  string         `json:"anomalyReason,omitempty"  db:"anomaly_reason"`
	AnomalyType    string         `json:"anomalyType,omitempty"    db:"anomaly_type"`
	AnomalyTypes   AnomalyTypes   `json:"anomalyTypes"               db:"anomaly_types"`
	AnomalyStatus  string         `json:"anomalyStatus,omitempty"  db:"anomaly_status"`
	AnomalyHistory AnomalyHistory `json:"anomalyHistory"           db:"anomaly_history"`
	FuelEventID    *int64         `json:"fuelEventId,omitempty"    db:"fuel_event_id"`
}

func (f FuelRecord) GetID() string    { return f.ID }
func (f *FuelRecord) SetID(id string) { f.ID = id }

type MaintenancePartLine struct {
	PartID   string  `json:"partId"`
	Qty      float64 `json:"qty"`
	UnitCost float64 `json:"unitCost"`
	Note     string  `json:"note,omitempty"`
}

// MaintenancePartLines is the JSONB-backed line-item breakdown on
// maintenance_items.parts_breakdown. On WO transition to "completed",
// each line decrements parts.stock and writes an "out" movement
// referencing this WO.
type MaintenancePartLines []MaintenancePartLine

type StatusHistoryEvent struct {
	At     string `json:"at"`
	Status string `json:"status,omitempty"`
	By     string `json:"by,omitempty"`
	Note   string `json:"note,omitempty"`
}

type StatusHistory []StatusHistoryEvent

type MaintenanceItem struct {
	ID             string               `json:"id"                  db:"id"`
	VehicleID      string               `json:"vehicleId"           db:"vehicle_id"`
	Date           string               `json:"date"                db:"date"     dbcast:"date"`
	Type           string               `json:"type"                db:"type"`
	Service        string               `json:"service"             db:"service"`
	Status         string               `json:"status"              db:"status"`
	Priority       string               `json:"priority"            db:"priority"`
	Workshop       string               `json:"workshop"            db:"workshop"`
	Mechanic       string               `json:"mechanic,omitempty"  db:"mechanic"`
	Odo            float64              `json:"odo"                 db:"odo"`
	NextDueKm      *float64             `json:"nextDueKm,omitempty" db:"next_due_km"`
	Cost           float64              `json:"cost"                db:"cost"`
	Parts          string               `json:"parts,omitempty"     db:"parts"`
	Notes          string               `json:"notes,omitempty"     db:"notes"`
	PartsBreakdown MaintenancePartLines `json:"partsBreakdown"      db:"parts_breakdown"`
	StatusHistory  StatusHistory        `json:"statusHistory"       db:"status_history"`
	PmScheduleID   string               `json:"pmScheduleId,omitempty" db:"pm_schedule_id"`
	LinkedSafetyID string               `json:"linkedSafetyId,omitempty" db:"linked_safety_id"`
}

func (m MaintenanceItem) GetID() string    { return m.ID }
func (m *MaintenanceItem) SetID(id string) { m.ID = id }

type PartMovement struct {
	ID       string  `json:"id"`
	Date     string  `json:"date"`
	Type     string  `json:"type"` // "in" | "out" | "adjust"
	Qty      float64 `json:"qty"`
	UnitCost float64 `json:"unitCost,omitempty"`
	Ref      string  `json:"ref,omitempty"`
	By       string  `json:"by,omitempty"`
	Note     string  `json:"note,omitempty"`
}

// PartMovements is the JSONB-backed stock ledger on parts.movements.
type PartMovements []PartMovement

type Part struct {
	ID           string        `json:"id"               db:"id"`
	Name         string        `json:"name"             db:"name"`
	Category     string        `json:"category"         db:"category"`
	SKU          string        `json:"sku"              db:"sku"`
	Stock        int           `json:"stock"            db:"stock"`
	ReorderPoint int           `json:"reorderPoint"     db:"reorder_point"`
	ReorderQty   int           `json:"reorderQty"       db:"reorder_qty"`
	UnitCost     float64       `json:"unitCost"         db:"unit_cost"`
	Location     string        `json:"location,omitempty" db:"location"`
	Vendor       string        `json:"vendor,omitempty"   db:"vendor"`
	LeadTimeDays int           `json:"leadTimeDays"     db:"lead_time_days"`
	LastReceived string        `json:"lastReceived,omitempty" db:"last_received" dbcast:"date"`
	LastConsumed string        `json:"lastConsumed,omitempty" db:"last_consumed" dbcast:"date"`
	Notes        string        `json:"notes,omitempty"    db:"notes"`
	Movements    PartMovements `json:"movements"          db:"movements"`
	// WarehouseItemID maps this part to its iag-warehouse wh_items UUID under
	// the stock-delegation model (empty until reconciled). WarehouseSyncedAt is
	// the last time Stock/Movements were refreshed from a warehouse event;
	// empty means this part is still showing legacy local stock.
	WarehouseItemID   string `json:"warehouseItemId,omitempty"   db:"warehouse_item_id"`
	WarehouseSyncedAt string `json:"warehouseSyncedAt,omitempty" db:"warehouse_synced_at" dbcast:"timestamptz"`
}

func (p Part) GetID() string    { return p.ID }
func (p *Part) SetID(id string) { p.ID = id }

type Tyre struct {
	ID             string  `json:"id"               db:"id"`
	VehicleID      string  `json:"vehicleId"        db:"vehicle_id"`
	Position       string  `json:"position"         db:"position"`
	Brand          string  `json:"brand"            db:"brand"`
	Model          string  `json:"model"            db:"model"`
	Serial         string  `json:"serial"           db:"serial"`
	MountedDate    string  `json:"mountedDate"      db:"mounted_date"  dbcast:"date"`
	MountedKm      float64 `json:"mountedKm"        db:"mounted_km"`
	TreadDepthMm   float64 `json:"treadDepthMm"     db:"tread_depth_mm"`
	TreadInitialMm float64 `json:"treadInitialMm"   db:"tread_initial_mm"`
	Status         string  `json:"status"           db:"status"`
}

func (t Tyre) GetID() string    { return t.ID }
func (t *Tyre) SetID(id string) { t.ID = id }

type Trip struct {
	ID            string   `json:"id"                  db:"id"`
	DriverID      string   `json:"driverId"            db:"driver_id"`
	VehicleID     string   `json:"vehicleId"           db:"vehicle_id"`
	Date          string   `json:"date"                db:"date"   dbcast:"date"`
	StartLocation string   `json:"startLocation"       db:"start_location"`
	EndLocation   string   `json:"endLocation"         db:"end_location"`
	DistanceKm    float64  `json:"distanceKm"          db:"distance_km"`
	DurationMin   float64  `json:"durationMin"         db:"duration_min"`
	FuelL         float64  `json:"fuelL"               db:"fuel_l"`
	Status        string   `json:"status"              db:"status"`
	Rating        *float64 `json:"rating,omitempty"    db:"rating"`
	Notes         string   `json:"notes,omitempty"     db:"notes"`
	StartedAt     string   `json:"startedAt,omitempty" db:"started_at" dbcast:"timestamptz"`
	EndedAt       string   `json:"endedAt,omitempty"   db:"ended_at"   dbcast:"timestamptz"`
	AutoGenerated bool     `json:"autoGenerated"       db:"auto_generated"`
}

func (t Trip) GetID() string    { return t.ID }
func (t *Trip) SetID(id string) { t.ID = id }

type SafetyEvent struct {
	ID            string        `json:"id"                  db:"id"`
	VehicleID     string        `json:"vehicleId"           db:"vehicle_id"`
	DriverID      string        `json:"driverId,omitempty"  db:"driver_id"`
	Date          string        `json:"date"                db:"date"  dbcast:"timestamptz"`
	Type          string        `json:"type"                db:"type"`
	Severity      string        `json:"severity"            db:"severity"`
	Status        string        `json:"status"              db:"status"`
	Location      string        `json:"location,omitempty"  db:"location"`
	Description   string        `json:"description"         db:"description"`
	Action        string        `json:"action,omitempty"    db:"action"`
	ReportedBy    string        `json:"reportedBy,omitempty" db:"reported_by"`
	GpsLat        *float64      `json:"gpsLat,omitempty"    db:"gps_lat"`
	GpsLng        *float64      `json:"gpsLng,omitempty"    db:"gps_lng"`
	Injuries      *int          `json:"injuries,omitempty"  db:"injuries"`
	Cost          *float64      `json:"cost,omitempty"      db:"cost"`
	LinkedWoID    string        `json:"linkedWoId,omitempty" db:"linked_wo_id"`
	Authorities   string        `json:"authorities,omitempty" db:"authorities"`
	StatusHistory StatusHistory `json:"statusHistory"       db:"status_history"`
}

func (s SafetyEvent) GetID() string    { return s.ID }
func (s *SafetyEvent) SetID(id string) { s.ID = id }

type ComplianceRenewal struct {
	RenewedAt string  `json:"renewedAt"`
	DocNumber string  `json:"docNumber,omitempty"`
	Issuer    string  `json:"issuer,omitempty"`
	Issued    string  `json:"issued,omitempty"`
	Expiry    string  `json:"expiry,omitempty"`
	Cost      float64 `json:"cost,omitempty"`
	Note      string  `json:"note,omitempty"`
}

// ComplianceRenewals is the JSONB-backed renewal log on
// compliance_items.renewal_history. The row's top-level fields carry
// the *current* period; every renewal pushes the prior period (with
// a renewedAt stamp) onto this array before overwriting them.
type ComplianceRenewals []ComplianceRenewal

type ComplianceItem struct {
	ID             string             `json:"id"                   db:"id"`
	VehicleID      string             `json:"vehicleId,omitempty"  db:"vehicle_id"`
	DriverID       string             `json:"driverId,omitempty"   db:"driver_id"`
	DocType        string             `json:"docType"              db:"doc_type"`
	DocNumber      string             `json:"docNumber,omitempty"  db:"doc_number"`
	Issuer         string             `json:"issuer,omitempty"     db:"issuer"`
	Issued         string             `json:"issued,omitempty"     db:"issued"  dbcast:"date"`
	Expiry         string             `json:"expiry"               db:"expiry"  dbcast:"date"`
	Status         string             `json:"status"               db:"status"`
	Notes          string             `json:"notes,omitempty"      db:"notes"`
	RenewalCostUgx float64            `json:"renewalCostUgx,omitempty" db:"renewal_cost_ugx"`
	RenewalHistory ComplianceRenewals `json:"renewalHistory"       db:"renewal_history"`
}

func (c ComplianceItem) GetID() string    { return c.ID }
func (c *ComplianceItem) SetID(id string) { c.ID = id }

type ServiceRequest struct {
	ID                   string `json:"id"                          db:"id"`
	RequesterName        string `json:"requesterName"               db:"requester_name"`
	RequesterDept        string `json:"requesterDept"               db:"requester_dept"`
	RequesterPhone       string `json:"requesterPhone,omitempty"    db:"requester_phone"`
	Purpose              string `json:"purpose"                     db:"purpose"`
	Destination          string `json:"destination"                 db:"destination"`
	StartDate            string `json:"startDate"                   db:"start_date"  dbcast:"date"`
	EndDate              string `json:"endDate,omitempty"           db:"end_date"    dbcast:"date"`
	Pax                  *int   `json:"pax,omitempty"               db:"pax"`
	CargoType            string `json:"cargoType,omitempty"         db:"cargo_type"`
	Urgency              string `json:"urgency"                     db:"urgency"`
	PreferredVehicleType string `json:"preferredVehicleType,omitempty" db:"preferred_vehicle_type"`
	ReviewerNotes        string `json:"reviewerNotes,omitempty"     db:"reviewer_notes"`
	Status               string `json:"status"                      db:"status"`
	SubmittedAt          string `json:"submittedAt"                 db:"submitted_at" dbcast:"timestamptz"`
	CreatedBy            string `json:"createdBy,omitempty"         db:"created_by"`
	AssignedVehicleID    string `json:"assignedVehicleId,omitempty" db:"assigned_vehicle_id"`
	AssignedDriverID     string `json:"assignedDriverId,omitempty"  db:"assigned_driver_id"`
	JmpID                string `json:"jmpId,omitempty"             db:"jmp_id"`
	TaskID               string `json:"taskId,omitempty"            db:"task_id"`
	// ApprovedBy / ApprovedAt record who moved the request into "approved"
	// and when — the approval audit trail. Stamped the first time the status
	// reaches "approved", via either the /advance workflow endpoint or the
	// generic PATCH path (mirrors the JMP mileage-approval fields).
	ApprovedBy string `json:"approvedBy,omitempty" db:"approved_by"`
	ApprovedAt string `json:"approvedAt,omitempty" db:"approved_at" dbcast:"timestamptz"`
	// Dispatch-chain gates (independent, each its own role). AssignmentApproved*
	// is the sign-off on the chosen vehicle+driver; Deployed* is the release of
	// that vehicle for the task/journey (DeploymentEntryID links the row added
	// to the daily deployment sheet).
	AssignmentApprovedBy string `json:"assignmentApprovedBy,omitempty" db:"assignment_approved_by"`
	AssignmentApprovedAt string `json:"assignmentApprovedAt,omitempty" db:"assignment_approved_at" dbcast:"timestamptz"`
	DeployedBy           string `json:"deployedBy,omitempty"           db:"deployed_by"`
	DeployedAt           string `json:"deployedAt,omitempty"           db:"deployed_at" dbcast:"timestamptz"`
	DeploymentEntryID    string `json:"deploymentEntryId,omitempty"    db:"deployment_entry_id"`
}

func (s ServiceRequest) GetID() string    { return s.ID }
func (s *ServiceRequest) SetID(id string) { s.ID = id }

// FuelRequest is a pre-authorisation for a fuel purchase. Unlike FuelRecord —
// which logs fuel that was already dispensed — a FuelRequest moves through an
// approval lifecycle (submitted → approved/rejected → fulfilled/cancelled).
// On fulfilment the request spawns a FuelRecord (FuelRecordID links the two)
// and the existing fleet.fuel.recorded finance event fires from that record.
type FuelRequest struct {
	ID              string  `json:"id"                       db:"id"`
	VehicleID       string  `json:"vehicleId"                db:"vehicle_id"`
	DriverID        string  `json:"driverId,omitempty"       db:"driver_id"`
	RequesterName   string  `json:"requesterName"            db:"requester_name"`
	RequesterDept   string  `json:"requesterDept,omitempty"  db:"requester_dept"`
	RequestedLitres float64 `json:"requestedLitres"          db:"requested_litres"`
	EstUnitPrice    float64 `json:"estUnitPrice,omitempty"   db:"est_unit_price"`
	EstTotal        float64 `json:"estTotal,omitempty"       db:"est_total"`
	Station         string  `json:"station,omitempty"        db:"station"`
	Purpose         string  `json:"purpose,omitempty"        db:"purpose"`
	Urgency         string  `json:"urgency,omitempty"        db:"urgency"`
	Status          string  `json:"status"                   db:"status"`
	Notes           string  `json:"notes,omitempty"          db:"notes"`
	ReviewerNotes   string  `json:"reviewerNotes,omitempty"  db:"reviewer_notes"`
	SubmittedAt     string  `json:"submittedAt,omitempty"    db:"submitted_at" dbcast:"timestamptz"`
	ApprovedBy      string  `json:"approvedBy,omitempty"     db:"approved_by"`
	ApprovedAt      string  `json:"approvedAt,omitempty"     db:"approved_at"  dbcast:"timestamptz"`
	FuelRecordID    string  `json:"fuelRecordId,omitempty"   db:"fuel_record_id"`
	CreatedBy       string  `json:"createdBy,omitempty"      db:"created_by"`
	// Chain linkage — a fuel request raised for a specific service request
	// and/or journey plan (empty when raised standalone against a vehicle).
	RequestID string `json:"requestId,omitempty" db:"request_id"`
	JmpID     string `json:"jmpId,omitempty"     db:"jmp_id"`

	// Procurement reconciliation (transient, db:"-"): when the procurement
	// integration is enabled, GET /fuel-requests/:id enriches these from the
	// sourcing requisition procurement imported for this request (origin_ref =
	// this id). Not persisted — procurement remains the system of record.
	ProcurementRequisitionID string `json:"procurementRequisitionId,omitempty" db:"-"`
	ProcurementStatus        string `json:"procurementStatus,omitempty"        db:"-"`
}

func (f FuelRequest) GetID() string    { return f.ID }
func (f *FuelRequest) SetID(id string) { f.ID = id }

type TaskLink struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// TaskLinks is the JSONB-backed slice of links on a task.
type TaskLinks []TaskLink

type TaskItem struct {
	ID           string    `json:"id"                  db:"id"`
	Title        string    `json:"title"               db:"title"`
	State        string    `json:"state"               db:"state"`
	Priority     string    `json:"priority"            db:"priority"`
	AssigneeName string    `json:"assigneeName"        db:"assignee_name"`
	DueDate      string    `json:"dueDate,omitempty"   db:"due_date"     dbcast:"date"`
	CreatedAt    string    `json:"createdAt"           db:"created_at"   dbcast:"timestamptz"`
	CompletedAt  string    `json:"completedAt,omitempty" db:"completed_at" dbcast:"timestamptz"`
	Source       string    `json:"source"              db:"source"`
	SourceID     string    `json:"sourceId,omitempty"  db:"source_id"`
	Links        TaskLinks `json:"links,omitempty"     db:"links"`
}

func (t TaskItem) GetID() string    { return t.ID }
func (t *TaskItem) SetID(id string) { t.ID = id }

type DeploymentEntry struct {
	ID               string  `json:"id"`
	VehicleID        string  `json:"vehicleId"`
	DriverID         string  `json:"driverId,omitempty"`
	Deployment       string  `json:"deployment,omitempty"`
	Location         string  `json:"location,omitempty"`
	MechStatus       string  `json:"mechStatus"`
	DeploymentStatus string  `json:"deploymentStatus"`
	OdoStart         float64 `json:"odoStart"`
	OdoEnd           float64 `json:"odoEnd"`
	FuelTracker      *bool   `json:"fuelTracker,omitempty"`
	Notes            string  `json:"notes,omitempty"`
}

// DeploymentEntries is the JSONB-backed slice of vehicles in one day's roll-up.
type DeploymentEntries []DeploymentEntry

type DeploymentDay struct {
	ID         string            `json:"id"          db:"id"`
	Date       string            `json:"date"        db:"date"        dbcast:"date"`
	CompiledBy string            `json:"compiledBy"  db:"compiled_by"`
	Notes      string            `json:"notes"       db:"notes"`
	Entries    DeploymentEntries `json:"entries"     db:"entries"`
}

func (d DeploymentDay) GetID() string    { return d.ID }
func (d *DeploymentDay) SetID(id string) { d.ID = id }

type AuditEntry struct {
	TS      string `json:"ts"`
	Action  string `json:"action"`
	Entity  string `json:"entity"`
	ID      string `json:"id"`
	Details string `json:"details,omitempty"`
	User    string `json:"user"`
}

type OperatorTicker struct {
	Diesel   float64 `json:"diesel"`
	Ugx      float64 `json:"ugx"`
	Operator string  `json:"operator"`
	Role     string  `json:"role"`
}

// ─── Inspections (DVIR) ────────────────────────────────────────────────────

type InspectionChecklistItem struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Category string `json:"category,omitempty"`
	Required bool   `json:"required"`
}

type InspectionChecklist []InspectionChecklistItem

type InspectionTemplate struct {
	ID        string              `json:"id"              db:"id"`
	Name      string              `json:"name"            db:"name"`
	Kind      string              `json:"kind"            db:"kind"`
	Checklist InspectionChecklist `json:"checklist"       db:"checklist"`
	Active    bool                `json:"active"          db:"active"`
	Notes     string              `json:"notes,omitempty" db:"notes"`
	CreatedAt string              `json:"createdAt"       db:"created_at" dbcast:"timestamptz"`
}

func (t InspectionTemplate) GetID() string    { return t.ID }
func (t *InspectionTemplate) SetID(id string) { t.ID = id }

type InspectionResultItem struct {
	ItemID string `json:"itemId"`
	Status string `json:"status"` // pass | fail | na
	Note   string `json:"note,omitempty"`
}

type InspectionResults []InspectionResultItem

type InspectionDefect struct {
	ItemID      string `json:"itemId"`
	Description string `json:"description"`
	Severity    string `json:"severity,omitempty"` // minor | major | critical
}

type InspectionDefects []InspectionDefect

type VehicleInspection struct {
	ID            string            `json:"id"                      db:"id"`
	TemplateID    string            `json:"templateId"              db:"template_id"`
	VehicleID     string            `json:"vehicleId"               db:"vehicle_id"`
	DriverID      string            `json:"driverId,omitempty"      db:"driver_id"`
	Kind          string            `json:"kind"                    db:"kind"`
	Status        string            `json:"status"                  db:"status"`
	Odo           float64           `json:"odo"                     db:"odo"`
	Location      string            `json:"location,omitempty"      db:"location"`
	Results       InspectionResults `json:"results"                 db:"results"`
	Defects       InspectionDefects `json:"defects"                 db:"defects"`
	Signature     string            `json:"signature,omitempty"     db:"signature"`
	SubmittedAt   string            `json:"submittedAt,omitempty"   db:"submitted_at" dbcast:"timestamptz"`
	SubmittedBy   string            `json:"submittedBy,omitempty"   db:"submitted_by"`
	MaintenanceID string            `json:"maintenanceId,omitempty" db:"maintenance_id"`
	Notes         string            `json:"notes,omitempty"         db:"notes"`
}

func (v VehicleInspection) GetID() string    { return v.ID }
func (v *VehicleInspection) SetID(id string) { v.ID = id }

// ─── Preventive maintenance schedules ─────────────────────────────────────

type PMSchedule struct {
	ID                 string   `json:"id"                           db:"id"`
	VehicleID          string   `json:"vehicleId,omitempty"          db:"vehicle_id"`
	Name               string   `json:"name"                         db:"name"`
	ServiceType        string   `json:"serviceType"                  db:"service_type"`
	ServiceDescription string   `json:"serviceDescription"           db:"service_description"`
	IntervalKm         *float64 `json:"intervalKm,omitempty"       db:"interval_km"`
	IntervalDays       *int     `json:"intervalDays,omitempty"     db:"interval_days"`
	LastServiceOdo     *float64 `json:"lastServiceOdo,omitempty"   db:"last_service_odo"`
	LastServiceDate    string   `json:"lastServiceDate,omitempty"  db:"last_service_date" dbcast:"date"`
	NextDueKm          *float64 `json:"nextDueKm,omitempty"        db:"next_due_km"`
	NextDueDate        string   `json:"nextDueDate,omitempty"      db:"next_due_date" dbcast:"date"`
	Vendor             string   `json:"vendor,omitempty"           db:"vendor"`
	AutoCreateWO       bool     `json:"autoCreateWo"               db:"auto_create_wo"`
	Active             bool     `json:"active"                     db:"active"`
	Notes              string   `json:"notes,omitempty"            db:"notes"`
}

func (p PMSchedule) GetID() string    { return p.ID }
func (p *PMSchedule) SetID(id string) { p.ID = id }
