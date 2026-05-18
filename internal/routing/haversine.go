package routing

import "math"

const earthRadiusM = 6371000.0

// HaversineM returns great-circle distance in metres.
func HaversineM(lat1, lon1, lat2, lon2 float64) float64 {
	const rad = math.Pi / 180
	φ1, φ2 := lat1*rad, lat2*rad
	Δφ := (lat2 - lat1) * rad
	Δλ := (lon2 - lon1) * rad
	a := math.Sin(Δφ/2)*math.Sin(Δφ/2) +
		math.Cos(φ1)*math.Cos(φ2)*math.Sin(Δλ/2)*math.Sin(Δλ/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusM * c
}

// GreatCirclePolyline samples a great-circle arc as [lng,lat] pairs for GeoJSON-style coords.
// Segments is capped to a sensible max for tiny payloads.
func GreatCirclePolyline(lat1, lon1, lat2, lon2 float64, segments int) [][2]float64 {
	if segments < 2 {
		segments = 2
	}
	if segments > 96 {
		segments = 96
	}
	out := make([][2]float64, 0, segments+1)
	const rad = math.Pi / 180
	const deg = 180 / math.Pi

	x1 := lat1 * rad
	y1 := lon1 * rad
	x2 := lat2 * rad
	y2 := lon2 * rad

	d := 2 * math.Asin(math.Min(1, math.Sqrt(
		math.Pow(math.Sin((x2-x1)/2), 2)+math.Cos(x1)*math.Cos(x2)*math.Pow(math.Sin((y2-y1)/2), 2))))
	if d < 1e-9 {
		return [][2]float64{{lon1, lat1}, {lon2, lat2}}
	}

	sinD := math.Sin(d)
	for i := 0; i <= segments; i++ {
		t := float64(i) / float64(segments)
		a := math.Sin((1-t)*d) / sinD
		b := math.Sin(t*d) / sinD
		x := a*math.Cos(x1)*math.Cos(y1) + b*math.Cos(x2)*math.Cos(y2)
		y := a*math.Cos(x1)*math.Sin(y1) + b*math.Cos(x2)*math.Sin(y2)
		z := a*math.Sin(x1) + b*math.Sin(x2)
		φ := math.Atan2(z, math.Sqrt(x*x+y*y))
		λ := math.Atan2(y, x)
		out = append(out, [2]float64{λ * deg, φ * deg})
	}
	return out
}

// PolylineDistanceM sums haversine segments for a [lng,lat] path.
func PolylineDistanceM(coords [][2]float64) float64 {
	if len(coords) < 2 {
		return 0
	}
	var sum float64
	for i := 1; i < len(coords); i++ {
		sum += HaversineM(coords[i-1][1], coords[i-1][0], coords[i][1], coords[i][0])
	}
	return sum
}
