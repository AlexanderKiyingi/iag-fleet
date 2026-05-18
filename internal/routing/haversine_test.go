package routing

import "testing"

func TestHaversineHalfEarth(t *testing.T) {
	// ~Half the Earth along the equator
	d := HaversineM(0, 0, 0, 180)
	if d < 19_000_000 || d > 21_000_000 {
		t.Fatalf("unexpected great-circle km: %.0f m", d)
	}
}

func TestStraightLinePath(t *testing.T) {
	p := [][2]float64{{-0.88, 30.265}, {0.327, 32.591}}
	coords, m := StraightLinePath(p)
	if len(coords) < 2 {
		t.Fatal("expected polyline")
	}
	if m < 100_000 || m > 1_000_000 {
		t.Fatalf("distance plausible UG corridor meters: %.0f", m)
	}
}
