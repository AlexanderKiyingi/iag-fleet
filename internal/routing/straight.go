package routing

// Points are (lat, lng). Returns GeoJSON-style [lng,lat] polyline and path length in metres.
func StraightLinePath(points [][2]float64) ([][2]float64, float64) {
	if len(points) < 2 {
		return nil, 0
	}
	var all [][2]float64
	for i := 0; i < len(points)-1; i++ {
		a := points[i]
		b := points[i+1]
		seg := GreatCirclePolyline(a[0], a[1], b[0], b[1], 32)
		if i == 0 {
			all = append(all, seg...)
		} else if len(seg) > 0 {
			all = append(all, seg[1:]...)
		}
	}
	return all, PolylineDistanceM(all)
}
