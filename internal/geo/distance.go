package geo

import "math"

const earthRadius = 6371000.0

// DistanceM devuelve la distancia aproximada en metros entre dos puntos
// (equirectangular: el error es despreciable a escala de una ciudad).
func DistanceM(a, b Point) float64 {
	lat := (a[1] + b[1]) / 2 * math.Pi / 180
	dx := (b[0] - a[0]) * math.Pi / 180 * math.Cos(lat)
	dy := (b[1] - a[1]) * math.Pi / 180
	return earthRadius * math.Sqrt(dx*dx+dy*dy)
}

// LineDistanceM devuelve la distancia en metros del punto a la polilínea.
func LineDistanceM(p Point, line []Point) float64 {
	best := math.Inf(1)
	if len(line) == 1 {
		return DistanceM(p, line[0])
	}
	cos := math.Cos(p[1] * math.Pi / 180)
	// Proyección local en metros centrada en p.
	proj := func(q Point) (float64, float64) {
		return (q[0] - p[0]) * math.Pi / 180 * cos * earthRadius, (q[1] - p[1]) * math.Pi / 180 * earthRadius
	}
	for i := 1; i < len(line); i++ {
		ax, ay := proj(line[i-1])
		bx, by := proj(line[i])
		dx, dy := bx-ax, by-ay
		t := 0.0
		if l2 := dx*dx + dy*dy; l2 > 0 {
			t = math.Max(0, math.Min(1, -(ax*dx+ay*dy)/l2))
		}
		cx, cy := ax+t*dx, ay+t*dy
		if d := math.Hypot(cx, cy); d < best {
			best = d
		}
	}
	return best
}
