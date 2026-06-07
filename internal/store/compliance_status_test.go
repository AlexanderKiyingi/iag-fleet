package store

import (
	"testing"
	"time"
)

func TestComplianceStatusFromExpiry(t *testing.T) {
	today := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		expiry string
		want   string
	}{
		{"", "missing"},
		{"bad-date", "missing"},
		{"2026-06-01", "expired"},
		{"2026-06-10", "expiring"},
		{"2026-07-01", "valid"},
	}
	for _, tc := range cases {
		got := ComplianceStatusFromExpiry(tc.expiry, today, ComplianceExpiringWithinDays)
		if got != tc.want {
			t.Fatalf("expiry %q: got %q want %q", tc.expiry, got, tc.want)
		}
	}
}
