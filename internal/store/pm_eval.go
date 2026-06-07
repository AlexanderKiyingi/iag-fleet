package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/iag/fleet-tool/backend/internal/models"
)

// PMDueRow is one schedule that is due or coming due soon.
type PMDueRow struct {
	Schedule models.PMSchedule `json:"schedule"`
	Vehicle  *models.Vehicle   `json:"vehicle,omitempty"`
	Reason   string            `json:"reason"` // odo | date | both
	DueInKm  *float64          `json:"dueInKm,omitempty"`
	DueInDays *int             `json:"dueInDays,omitempty"`
}

// ListDuePMSchedules returns active schedules due within the given thresholds.
func (r *Repository) ListDuePMSchedules(ctx context.Context, withinDays int, withinKm float64) ([]PMDueRow, error) {
	schedules, err := r.PMSchedules.List(ctx)
	if err != nil {
		return nil, err
	}
	vehicles, err := r.Vehicles.List(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]models.Vehicle, len(vehicles))
	for _, v := range vehicles {
		byID[v.ID] = v
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	var out []PMDueRow
	for _, s := range schedules {
		if !s.Active {
			continue
		}
		var veh *models.Vehicle
		if s.VehicleID != "" {
			if v, ok := byID[s.VehicleID]; ok {
				veh = &v
			}
		}
		reason, dueKm, dueDays := pmDueReason(s, veh, today, withinDays, withinKm)
		if reason == "" {
			continue
		}
		out = append(out, PMDueRow{
			Schedule:  s,
			Vehicle:   veh,
			Reason:    reason,
			DueInKm:   dueKm,
			DueInDays: dueDays,
		})
	}
	return out, nil
}

func pmDueReason(s models.PMSchedule, veh *models.Vehicle, today time.Time, withinDays int, withinKm float64) (reason string, dueInKm *float64, dueInDays *int) {
	odoDue := false
	dateDue := false
	if s.NextDueKm != nil && veh != nil {
		remaining := *s.NextDueKm - veh.Odo
		dueInKm = &remaining
		if remaining <= withinKm {
			odoDue = true
		}
	}
	if s.NextDueDate != "" {
		d, err := time.Parse("2006-01-02", s.NextDueDate)
		if err == nil {
			days := int(d.Sub(today).Hours() / 24)
			dueInDays = &days
			if days <= withinDays {
				dateDue = true
			}
		}
	}
	switch {
	case odoDue && dateDue:
		return "both", dueInKm, dueInDays
	case odoDue:
		return "odo", dueInKm, dueInDays
	case dateDue:
		return "date", dueInKm, dueInDays
	default:
		return "", nil, nil
	}
}

// PMEvaluateResult summarizes a preventive maintenance evaluation run.
type PMEvaluateResult struct {
	Checked   int      `json:"checked"`
	Created   int      `json:"workOrdersCreated"`
	Skipped   int      `json:"skipped"`
	CreatedIDs []string `json:"createdIds,omitempty"`
}

// EvaluatePMSchedules creates scheduled maintenance_items for due PM schedules.
func (r *Repository) EvaluatePMSchedules(ctx context.Context, withinDays int, withinKm float64) (PMEvaluateResult, error) {
	due, err := r.ListDuePMSchedules(ctx, withinDays, withinKm)
	if err != nil {
		return PMEvaluateResult{}, err
	}
	res := PMEvaluateResult{Checked: len(due)}
	today := time.Now().UTC().Format("2006-01-02")

	for _, row := range due {
		s := row.Schedule
		if !s.AutoCreateWO {
			res.Skipped++
			continue
		}
		if s.VehicleID == "" {
			res.Skipped++
			continue
		}
		open, err := r.hasOpenPMWorkOrder(ctx, s.ID, s.VehicleID)
		if err != nil {
			return res, err
		}
		if open {
			res.Skipped++
			continue
		}
		mx := models.MaintenanceItem{
			ID:           "",
			VehicleID:    s.VehicleID,
			Date:         today,
			Type:         s.ServiceType,
			Service:      s.ServiceDescription,
			Status:       "scheduled",
			Priority:     "normal",
			Workshop:     s.Vendor,
			Odo:          0,
			Notes:        fmt.Sprintf("Auto-created from PM schedule %s (%s)", s.ID, s.Name),
			PmScheduleID: s.ID,
		}
		if row.Vehicle != nil {
			mx.Odo = row.Vehicle.Odo
		}
		if mx.Service == "" {
			mx.Service = s.Name
		}
		if mx.Workshop == "" {
			mx.Workshop = "TBD"
		}
		created, err := r.Maintenance.Add(ctx, mx)
		if err != nil {
			return res, err
		}
		res.Created++
		res.CreatedIDs = append(res.CreatedIDs, created.ID)
	}
	return res, nil
}

// RollPMScheduleFromWorkOrder advances last_service on the linked PM schedule and
// syncs vehicles.next_service_km for v7 dashboard parity.
func (r *Repository) RollPMScheduleFromWorkOrder(ctx context.Context, mx models.MaintenanceItem) error {
	if mx.PmScheduleID == "" {
		return nil
	}
	sched, err := r.PMSchedules.Get(ctx, mx.PmScheduleID)
	if err != nil {
		return err
	}
	odo := mx.Odo
	date := mx.Date
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}
	sched.LastServiceOdo = &odo
	sched.LastServiceDate = date
	RecomputePMNextDue(&sched)
	if _, err := r.PMSchedules.Replace(ctx, sched.ID, sched); err != nil {
		return err
	}
	if sched.VehicleID != "" && sched.NextDueKm != nil {
		nextKm := *sched.NextDueKm
		_, _ = r.Vehicles.Update(ctx, sched.VehicleID, func(v *models.Vehicle) {
			v.NextServiceKm = nextKm
		})
	}
	return nil
}

func (r *Repository) hasOpenPMWorkOrder(ctx context.Context, scheduleID, vehicleID string) (bool, error) {
	const q = `
        SELECT 1 FROM maintenance_items
        WHERE pm_schedule_id = $1 AND vehicle_id = $2
          AND status IN ('scheduled', 'in-progress', 'overdue')
        LIMIT 1`
	var one int
	err := r.pool.QueryRow(ctx, q, scheduleID, vehicleID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// RecomputePMNextDue sets next_due_km / next_due_date from intervals and last service.
func RecomputePMNextDue(s *models.PMSchedule) {
	if s.IntervalKm != nil && s.LastServiceOdo != nil {
		next := *s.LastServiceOdo + *s.IntervalKm
		s.NextDueKm = &next
	}
	if s.IntervalDays != nil && s.LastServiceDate != "" {
		d, err := time.Parse("2006-01-02", s.LastServiceDate)
		if err == nil {
			next := d.AddDate(0, 0, *s.IntervalDays).Format("2006-01-02")
			s.NextDueDate = next
		}
	}
}
