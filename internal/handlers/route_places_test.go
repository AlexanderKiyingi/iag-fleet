package handlers

import (
	"testing"
)

func TestHaversineM_samePoint(t *testing.T) {
	if d := haversineM(0.3, 32.5, 0.3, 32.5); d > 0.01 {
		t.Fatalf("same point distance = %v, want ~0", d)
	}
}

func TestHaversineM_nearby(t *testing.T) {
	// ~111 m per 0.001° latitude near the equator.
	d := haversineM(0.0, 32.0, 0.001, 32.0)
	if d < 100 || d > 120 {
		t.Fatalf("0.001° north ≈ 111 m, got %v", d)
	}
}
