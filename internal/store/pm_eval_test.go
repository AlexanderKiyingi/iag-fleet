package store

import (
	"testing"
	"time"

	"github.com/iag/fleet-tool/backend/internal/models"
)

func TestRecomputePMNextDue(t *testing.T) {
	km := 10000.0
	days := 90
	lastOdo := 50000.0
	s := models.PMSchedule{
		IntervalKm:      &km,
		IntervalDays:      &days,
		LastServiceOdo:    &lastOdo,
		LastServiceDate:   "2026-01-01",
	}
	RecomputePMNextDue(&s)
	if s.NextDueKm == nil || *s.NextDueKm != 60000 {
		t.Fatalf("nextDueKm %+v", s.NextDueKm)
	}
	wantDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 90).Format("2006-01-02")
	if s.NextDueDate != wantDate {
		t.Fatalf("nextDueDate %q want %q", s.NextDueDate, wantDate)
	}
}

func TestPmDueReason(t *testing.T) {
	today := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	nextKm := 10500.0
	s := models.PMSchedule{NextDueKm: &nextKm, NextDueDate: "2026-08-01"}
	veh := &models.Vehicle{Odo: 10000}
	reason, dueKm, _ := pmDueReason(s, veh, today, 14, 500)
	if reason != "odo" {
		t.Fatalf("reason %q", reason)
	}
	if dueKm == nil || *dueKm != 500 {
		t.Fatalf("dueKm %+v", dueKm)
	}
}
