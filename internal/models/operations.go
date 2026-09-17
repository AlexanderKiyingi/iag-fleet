package models

// Operations records: the eight fleet screens the frontend ships forms for
// and this service had no table for, plus the carrier master the Logistics
// app needs. Each one was a stored entity on the app side whose rows only
// ever reached the shared Go API — so with the platform adapter on, the form
// still rendered and every save went to a store the platform cannot read.
//
// They are plain CRUD resources (handlers.Resource) with a derived column or
// two computed in a BeforeCreate/BeforeUpdate hook — see
// handlers/operations_resources.go — rather than bespoke handlers, so they
// carry the same list/search/bulk surface as every other entity.
//
// Foreign keys are nullable uuids: a weighbridge ticket usually names a
// vehicle and a cargo, but the form does not require either, and a ticket
// typed against a truck that is not on the register is still a ticket.

// WeighbridgeTicket is one weighing at a weighbridge or corridor checkpoint.
type WeighbridgeTicket struct {
	ID            string   `json:"id"                       db:"id" dbcast:"uuid"`
	Reference     string   `json:"reference,omitempty"      db:"reference"`
	Date          string   `json:"date"                     db:"date" dbcast:"date"`
	CargoID       string   `json:"cargoId,omitempty"        db:"cargo_id" dbcast:"uuid"`
	VehicleID     string   `json:"vehicleId,omitempty"      db:"vehicle_id" dbcast:"uuid"`
	Site          string   `json:"site"                     db:"site"`
	Stage         string   `json:"stage,omitempty"          db:"stage"`
	GrossWeightKg float64  `json:"grossWeightKg"            db:"gross_weight_kg"`
	AxleWeightKg  *float64 `json:"axleWeightKg,omitempty"   db:"axle_weight_kg"`
	GrossLimitKg  *float64 `json:"grossLimitKg,omitempty"   db:"gross_limit_kg"`
	AxleLimitKg   *float64 `json:"axleLimitKg,omitempty"    db:"axle_limit_kg"`
	// Derived: gross or axle over its limit. Written by the resource hook.
	Overweight bool   `json:"overweight"               db:"overweight"`
	Status     string `json:"status"                   db:"status"`
	Notes      string `json:"notes,omitempty"          db:"notes"`
	CreatedAt  string `json:"createdAt" db:"created_at" dbcast:"timestamptz" dbdefault:"true"`
	UpdatedAt  string `json:"updatedAt" db:"updated_at" dbcast:"timestamptz" dbdefault:"true"`
}

func (w WeighbridgeTicket) GetID() string    { return w.ID }
func (w *WeighbridgeTicket) SetID(id string) { w.ID = id }

// VehicleDiagnostic is a fault code raised on a vehicle (DTC or inspection).
type VehicleDiagnostic struct {
	ID              string `json:"id"                        db:"id" dbcast:"uuid"`
	Reference       string `json:"reference,omitempty"       db:"reference"`
	Date            string `json:"date"                      db:"date" dbcast:"date"`
	VehicleID       string `json:"vehicleId,omitempty"       db:"vehicle_id" dbcast:"uuid"`
	Code            string `json:"code"                      db:"code"`
	Description     string `json:"description,omitempty"     db:"description"`
	Severity        string `json:"severity"                  db:"severity"`
	SuggestedAction string `json:"suggestedAction,omitempty" db:"suggested_action"`
	// The maintenance item raised for it, if any. Text rather than a uuid FK:
	// the form lets a supervisor type a work-order reference before one exists.
	WorkOrder string `json:"workOrder,omitempty"        db:"work_order"`
	Status    string `json:"status"                    db:"status"`
	Notes     string `json:"notes,omitempty"           db:"notes"`
	CreatedAt string `json:"createdAt" db:"created_at" dbcast:"timestamptz" dbdefault:"true"`
	UpdatedAt string `json:"updatedAt" db:"updated_at" dbcast:"timestamptz" dbdefault:"true"`
}

func (v VehicleDiagnostic) GetID() string    { return v.ID }
func (v *VehicleDiagnostic) SetID(id string) { v.ID = id }

// DriverHOSLog is one day's hours-of-service record for a driver.
type DriverHOSLog struct {
	ID              string   `json:"id"                        db:"id" dbcast:"uuid"`
	DriverID        string   `json:"driverId,omitempty"        db:"driver_id" dbcast:"uuid"`
	VehicleID       string   `json:"vehicleId,omitempty"       db:"vehicle_id" dbcast:"uuid"`
	JMPID           string   `json:"jmpId,omitempty"           db:"jmp_id" dbcast:"uuid"`
	Date            string   `json:"date"                      db:"date" dbcast:"date"`
	DrivingHours    float64  `json:"drivingHours"              db:"driving_hours"`
	RestHours       *float64 `json:"restHours,omitempty"       db:"rest_hours"`
	ContinuousHours *float64 `json:"continuousHours,omitempty" db:"continuous_hours"`
	Status          string   `json:"status"                    db:"status"`
	Notes           string   `json:"notes,omitempty"           db:"notes"`
	CreatedAt       string   `json:"createdAt" db:"created_at" dbcast:"timestamptz" dbdefault:"true"`
	UpdatedAt       string   `json:"updatedAt" db:"updated_at" dbcast:"timestamptz" dbdefault:"true"`
}

func (d DriverHOSLog) GetID() string    { return d.ID }
func (d *DriverHOSLog) SetID(id string) { d.ID = id }

// DriverSafetyScore is a driver's score for a period, with the coaching that
// followed.
type DriverSafetyScore struct {
	ID              string  `json:"id"                        db:"id" dbcast:"uuid"`
	DriverID        string  `json:"driverId,omitempty"        db:"driver_id" dbcast:"uuid"`
	PeriodStart     string  `json:"periodStart"               db:"period_start" dbcast:"date"`
	PeriodEnd       string  `json:"periodEnd,omitempty"       db:"period_end" dbcast:"date"`
	Score           float64 `json:"score"                     db:"score"`
	HarshCount      *int    `json:"harshCount,omitempty"      db:"harsh_count"`
	Supervisor      string  `json:"supervisor,omitempty"      db:"supervisor"`
	CoachingOutcome string  `json:"coachingOutcome,omitempty" db:"coaching_outcome"`
	Status          string  `json:"status"                    db:"status"`
	Notes           string  `json:"notes,omitempty"           db:"notes"`
	CreatedAt       string  `json:"createdAt" db:"created_at" dbcast:"timestamptz" dbdefault:"true"`
	UpdatedAt       string  `json:"updatedAt" db:"updated_at" dbcast:"timestamptz" dbdefault:"true"`
}

func (d DriverSafetyScore) GetID() string    { return d.ID }
func (d *DriverSafetyScore) SetID(id string) { d.ID = id }

// FuelCardReconciliation compares a card statement with the fuel log.
type FuelCardReconciliation struct {
	ID               string  `json:"id"                     db:"id" dbcast:"uuid"`
	Reference        string  `json:"reference,omitempty"    db:"reference"`
	Date             string  `json:"date"                   db:"date" dbcast:"date"`
	Account          string  `json:"account"                db:"account"`
	StatementBalance float64 `json:"statementBalance"       db:"statement_balance"`
	SystemBalance    float64 `json:"systemBalance"          db:"system_balance"`
	// Derived: statement − system. Written by the resource hook.
	Discrepancy float64 `json:"discrepancy"            db:"discrepancy"`
	Status      string  `json:"status"                 db:"status"`
	Notes       string  `json:"notes,omitempty"        db:"notes"`
	CreatedAt   string  `json:"createdAt" db:"created_at" dbcast:"timestamptz" dbdefault:"true"`
	UpdatedAt   string  `json:"updatedAt" db:"updated_at" dbcast:"timestamptz" dbdefault:"true"`
}

func (f FuelCardReconciliation) GetID() string    { return f.ID }
func (f *FuelCardReconciliation) SetID(id string) { f.ID = id }

// ServiceReminder is a raised reminder — from a DTC, a PM schedule or an
// inspection — with an open/done state. Distinct from pm_schedules, which is
// the interval rule; this is the row a supervisor ticks off.
type ServiceReminder struct {
	ID          string `json:"id"                    db:"id" dbcast:"uuid"`
	VehicleID   string `json:"vehicleId,omitempty"   db:"vehicle_id" dbcast:"uuid"`
	Code        string `json:"code,omitempty"        db:"code"`
	Date        string `json:"date"                  db:"date" dbcast:"date"`
	DueDate     string `json:"dueDate,omitempty"     db:"due_date" dbcast:"date"`
	Source      string `json:"source"                db:"source"`
	Description string `json:"description,omitempty" db:"description"`
	Status      string `json:"status"                db:"status"`
	Notes       string `json:"notes,omitempty"       db:"notes"`
	CreatedAt   string `json:"createdAt" db:"created_at" dbcast:"timestamptz" dbdefault:"true"`
	UpdatedAt   string `json:"updatedAt" db:"updated_at" dbcast:"timestamptz" dbdefault:"true"`
}

func (s ServiceReminder) GetID() string    { return s.ID }
func (s *ServiceReminder) SetID(id string) { s.ID = id }

// EmissionsEntry is fuel burned on a journey, with CO₂e derived from it.
type EmissionsEntry struct {
	ID        string   `json:"id"                  db:"id" dbcast:"uuid"`
	Reference string   `json:"reference,omitempty" db:"reference"`
	Date      string   `json:"date"                db:"date" dbcast:"date"`
	VehicleID string   `json:"vehicleId,omitempty" db:"vehicle_id" dbcast:"uuid"`
	JMPID     string   `json:"jmpId,omitempty"     db:"jmp_id" dbcast:"uuid"`
	Litres    float64  `json:"litres"              db:"litres"`
	Km        *float64 `json:"km,omitempty"        db:"km"`
	Tonnes    *float64 `json:"tonnes,omitempty"    db:"tonnes"`
	// Derived from litres at DieselKgCO2ePerLitre; per tonne-km when both km
	// and tonnes are present. Written by the resource hook.
	CO2eKg         float64  `json:"co2eKg"                    db:"co2e_kg"`
	CO2ePerTonneKm *float64 `json:"co2ePerTonneKm,omitempty" db:"co2e_per_tonne_km"`
	Status         string   `json:"status"                    db:"status"`
	Notes          string   `json:"notes,omitempty"           db:"notes"`
	CreatedAt      string   `json:"createdAt" db:"created_at" dbcast:"timestamptz" dbdefault:"true"`
	UpdatedAt      string   `json:"updatedAt" db:"updated_at" dbcast:"timestamptz" dbdefault:"true"`
}

func (e EmissionsEntry) GetID() string    { return e.ID }
func (e *EmissionsEntry) SetID(id string) { e.ID = id }

// DieselKgCO2ePerLitre is the tank-to-wheel factor for road diesel (DEFRA /
// GHG Protocol, 2.68 kg CO₂e per litre). One constant, stated once, so an
// emissions figure on the platform can be traced to it.
const DieselKgCO2ePerLitre = 2.68

// RouteETA is a live progress row for a journey plan.
type RouteETA struct {
	ID               string   `json:"id"                         db:"id" dbcast:"uuid"`
	Reference        string   `json:"reference,omitempty"        db:"reference"`
	JMPID            string   `json:"jmpId,omitempty"            db:"jmp_id" dbcast:"uuid"`
	VehicleID        string   `json:"vehicleId,omitempty"        db:"vehicle_id" dbcast:"uuid"`
	FromLocation     string   `json:"fromLocation,omitempty"     db:"from_location"`
	ToLocation       string   `json:"toLocation,omitempty"       db:"to_location"`
	RemainingKm      *float64 `json:"remainingKm,omitempty"      db:"remaining_km"`
	SpeedKmh         *float64 `json:"speedKmh,omitempty"         db:"speed_kmh"`
	SuggestedRoute   string   `json:"suggestedRoute,omitempty"   db:"suggested_route"`
	LiveETA          string   `json:"liveEta,omitempty"          db:"live_eta"`
	BorderDelayHours *float64 `json:"borderDelayHours,omitempty" db:"border_delay_hours"`
	Status           string   `json:"status"                     db:"status"`
	Notes            string   `json:"notes,omitempty"            db:"notes"`
	CreatedAt        string   `json:"createdAt" db:"created_at" dbcast:"timestamptz" dbdefault:"true"`
	UpdatedAt        string   `json:"updatedAt" db:"updated_at" dbcast:"timestamptz" dbdefault:"true"`
}

func (r RouteETA) GetID() string    { return r.ID }
func (r *RouteETA) SetID(id string) { r.ID = id }

// Carrier is a third-party transporter — reference data the Logistics app's
// shipment form picks from. Cargo already carries a free-text `transporter`;
// this is the master that text should come from.
type Carrier struct {
	ID            string `json:"id"                      db:"id" dbcast:"uuid"`
	Name          string `json:"name"                    db:"name"`
	Code          string `json:"code,omitempty"          db:"code"`
	Phone         string `json:"phone,omitempty"         db:"phone"`
	Email         string `json:"email,omitempty"         db:"email"`
	ContactPerson string `json:"contactPerson,omitempty" db:"contact_person"`
	Address       string `json:"address,omitempty"       db:"address"`
	Type          string `json:"type,omitempty"          db:"type"`
	Status        string `json:"status"                  db:"status"`
	Notes         string `json:"notes,omitempty"         db:"notes"`
	CreatedAt     string `json:"createdAt" db:"created_at" dbcast:"timestamptz" dbdefault:"true"`
	UpdatedAt     string `json:"updatedAt" db:"updated_at" dbcast:"timestamptz" dbdefault:"true"`
}

func (c Carrier) GetID() string    { return c.ID }
func (c *Carrier) SetID(id string) { c.ID = id }

// PhotoIDs is a JSONB list of DMS attachment ids.
type PhotoIDs []string

// TripPOD is proof of delivery for a trip (migration 0058). Recording one
// completes the trip; see handlers.NewTripPODResource.
type TripPOD struct {
	ID            string   `json:"id"                      db:"id" dbcast:"uuid"`
	TripID        string   `json:"tripId"                  db:"trip_id" dbcast:"uuid"`
	Reference     string   `json:"reference,omitempty"     db:"reference"`
	Date          string   `json:"date"                    db:"date" dbcast:"date"`
	Customer      string   `json:"customer,omitempty"      db:"customer"`
	ReceivedBy    string   `json:"receivedBy"              db:"received_by"`
	ReceiverPhone string   `json:"receiverPhone,omitempty" db:"receiver_phone"`
	Condition     string   `json:"condition"               db:"condition"`
	PhotoIDs      PhotoIDs `json:"photoIds"                db:"photo_ids"`
	Status        string   `json:"status"                  db:"status"`
	Notes         string   `json:"notes,omitempty"         db:"notes"`
	CreatedAt     string   `json:"createdAt" db:"created_at" dbcast:"timestamptz" dbdefault:"true"`
	UpdatedAt     string   `json:"updatedAt" db:"updated_at" dbcast:"timestamptz" dbdefault:"true"`
}

func (p TripPOD) GetID() string    { return p.ID }
func (p *TripPOD) SetID(id string) { p.ID = id }
