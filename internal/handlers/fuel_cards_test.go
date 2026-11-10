package handlers

import (
	"testing"

	"github.com/iag/fleet-tool/backend/internal/models"
)

func TestLast4FromCardNumber(t *testing.T) {
	if got := last4FromCardNumber("1234-5678-9012-3456"); got != "3456" {
		t.Fatalf("got %q", got)
	}
	if got := last4FromCardNumber("FC-99"); got != "99" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeFuelCard_requiresExactlyOneAssigneeWhenActive(t *testing.T) {
	c := &models.FuelCard{CardNumber: "4111111111111111", Status: "active"}
	if err := normalizeFuelCard(c); err == nil {
		t.Fatal("expected error for missing assignee")
	}
	c.VehicleID = "veh-1"
	c.DriverID = "drv-1"
	if err := normalizeFuelCard(c); err == nil {
		t.Fatal("expected error for both assignees")
	}
	c.DriverID = ""
	if err := normalizeFuelCard(c); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if c.Last4 != "1111" {
		t.Fatalf("last4=%q", c.Last4)
	}
}

func TestLitresCloseFuel(t *testing.T) {
	if !litresCloseFuel(100, 104) {
		t.Fatal("expected close")
	}
	if litresCloseFuel(100, 120) {
		t.Fatal("expected not close")
	}
}

func TestDatesWithinOneDay(t *testing.T) {
	if !datesWithinOneDay("2026-11-10", "2026-11-10") {
		t.Fatal("same day")
	}
	if !datesWithinOneDay("2026-11-10", "2026-11-11") {
		t.Fatal("adjacent day")
	}
	if datesWithinOneDay("2026-11-10", "2026-11-13") {
		t.Fatal("too far")
	}
}
