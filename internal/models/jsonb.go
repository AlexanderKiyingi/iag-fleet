package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
)

// Scanner/Valuer for the JSONB-backed model fields:
//   - jmps.toolbox                       → Toolbox
//   - cargo.stage_history                → CargoStageHistory ([]CargoStageEvent)
//   - task_items.links                   → TaskLinks ([]TaskLink)
//   - deployment_days.entries            → DeploymentEntries ([]DeploymentEntry)
//   - parts.movements                    → PartMovements ([]PartMovement)
//   - compliance_items.renewal_history   → ComplianceRenewals ([]ComplianceRenewal)
//   - maintenance_items.parts_breakdown  → MaintenancePartLines ([]MaintenancePartLine)
//   - maintenance_items.status_history     → StatusHistory ([]StatusHistoryEvent)
//   - safety_events.status_history         → StatusHistory
//   - fuel_records.anomaly_history         → AnomalyHistory ([]AnomalyHistoryEvent)
//   - fuel_records.anomaly_types           → AnomalyTypes ([]string)
//
// pgx delivers JSONB as []byte (or string) on Scan; we json.Unmarshal.
// On Value (INSERT/UPDATE bind), we json.Marshal — pgx converts that
// to a JSONB parameter.
//
// Nil-safety: reading SQL NULL into one of these types gives the zero
// value. Writing the zero value (empty struct / nil slice) yields a
// JSON null on Value, which Postgres accepts for nullable columns and
// treats as `'{}' / '[]'` for typed JSONB DEFAULTs (since DEFAULTs
// only fire on missing INSERT columns, never on explicit NULL — the
// store layer compensates by emitting `'{}'` / `'[]'` for empty values).

func (t *Toolbox) Scan(src any) error {
	b, err := jsonbBytes(src)
	if err != nil || b == nil {
		*t = Toolbox{}
		return err
	}
	return json.Unmarshal(b, t)
}

func (t Toolbox) Value() (driver.Value, error) {
	return json.Marshal(t)
}

func (h *CargoStageHistory) Scan(src any) error {
	b, err := jsonbBytes(src)
	if err != nil || b == nil {
		*h = nil
		return err
	}
	return json.Unmarshal(b, h)
}

func (h CargoStageHistory) Value() (driver.Value, error) {
	if h == nil {
		return []byte(`[]`), nil
	}
	return json.Marshal(h)
}

func (l *TaskLinks) Scan(src any) error {
	b, err := jsonbBytes(src)
	if err != nil || b == nil {
		*l = nil
		return err
	}
	return json.Unmarshal(b, l)
}

func (l TaskLinks) Value() (driver.Value, error) {
	if l == nil {
		return []byte(`[]`), nil
	}
	return json.Marshal(l)
}

func (e *DeploymentEntries) Scan(src any) error {
	b, err := jsonbBytes(src)
	if err != nil || b == nil {
		*e = nil
		return err
	}
	return json.Unmarshal(b, e)
}

func (e DeploymentEntries) Value() (driver.Value, error) {
	if e == nil {
		return []byte(`[]`), nil
	}
	return json.Marshal(e)
}

func (m *PartMovements) Scan(src any) error {
	b, err := jsonbBytes(src)
	if err != nil || b == nil {
		*m = nil
		return err
	}
	return json.Unmarshal(b, m)
}

func (m PartMovements) Value() (driver.Value, error) {
	if m == nil {
		return []byte(`[]`), nil
	}
	return json.Marshal(m)
}

func (r *ComplianceRenewals) Scan(src any) error {
	b, err := jsonbBytes(src)
	if err != nil || b == nil {
		*r = nil
		return err
	}
	return json.Unmarshal(b, r)
}

func (r ComplianceRenewals) Value() (driver.Value, error) {
	if r == nil {
		return []byte(`[]`), nil
	}
	return json.Marshal(r)
}

func (l *MaintenancePartLines) Scan(src any) error {
	b, err := jsonbBytes(src)
	if err != nil || b == nil {
		*l = nil
		return err
	}
	return json.Unmarshal(b, l)
}

func (l MaintenancePartLines) Value() (driver.Value, error) {
	if l == nil {
		return []byte(`[]`), nil
	}
	return json.Marshal(l)
}

func (h *StatusHistory) Scan(src any) error {
	b, err := jsonbBytes(src)
	if err != nil || b == nil {
		*h = nil
		return err
	}
	return json.Unmarshal(b, h)
}

func (h StatusHistory) Value() (driver.Value, error) {
	if h == nil {
		return []byte(`[]`), nil
	}
	return json.Marshal(h)
}

func (h *AnomalyHistory) Scan(src any) error {
	b, err := jsonbBytes(src)
	if err != nil || b == nil {
		*h = nil
		return err
	}
	return json.Unmarshal(b, h)
}

func (h AnomalyHistory) Value() (driver.Value, error) {
	if h == nil {
		return []byte(`[]`), nil
	}
	return json.Marshal(h)
}

// AnomalyTypes is every rule that fired on a fuel record (primary in anomaly_type).
type AnomalyTypes []string

func (t *AnomalyTypes) Scan(src any) error {
	b, err := jsonbBytes(src)
	if err != nil || b == nil {
		*t = nil
		return err
	}
	return json.Unmarshal(b, t)
}

func (t AnomalyTypes) Value() (driver.Value, error) {
	if t == nil {
		return []byte(`[]`), nil
	}
	return json.Marshal(t)
}

func (c *InspectionChecklist) Scan(src any) error {
	b, err := jsonbBytes(src)
	if err != nil || b == nil {
		*c = nil
		return err
	}
	return json.Unmarshal(b, c)
}

func (c InspectionChecklist) Value() (driver.Value, error) {
	if c == nil {
		return []byte(`[]`), nil
	}
	return json.Marshal(c)
}

func (r *InspectionResults) Scan(src any) error {
	b, err := jsonbBytes(src)
	if err != nil || b == nil {
		*r = nil
		return err
	}
	return json.Unmarshal(b, r)
}

func (r InspectionResults) Value() (driver.Value, error) {
	if r == nil {
		return []byte(`[]`), nil
	}
	return json.Marshal(r)
}

func (d *InspectionDefects) Scan(src any) error {
	b, err := jsonbBytes(src)
	if err != nil || b == nil {
		*d = nil
		return err
	}
	return json.Unmarshal(b, d)
}

func (d InspectionDefects) Value() (driver.Value, error) {
	if d == nil {
		return []byte(`[]`), nil
	}
	return json.Marshal(d)
}

// jsonbBytes normalizes the various concrete types pgx can hand to Scan
// for a JSONB column ([]byte, string, or nil).
func jsonbBytes(src any) ([]byte, error) {
	switch v := src.(type) {
	case nil:
		return nil, nil
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	default:
		return nil, errors.New("models: cannot scan JSONB from unsupported source type")
	}
}
