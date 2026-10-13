package resolver

import (
	"math"
	"sort"

	"addrsvc/internal/geo"
	"addrsvc/internal/snapshot"
)

// axis es el eje principal de una calle: permite pasar de un punto a su progresiva
// (metros a lo largo de la calle) y de vuelta. Es una aproximación recta, suficiente
// para extrapolar unas cuadras; no sirve para calles que dan la vuelta.
type axis struct {
	origin geo.Point
	cos    float64
	ux, uy float64
	lines  [][]geo.Point
	lo, hi float64
}

func (a *axis) xy(p geo.Point) (float64, float64) {
	const r = 6371000.0
	return (p[0] - a.origin[0]) * math.Pi / 180 * a.cos * r, (p[1] - a.origin[1]) * math.Pi / 180 * r
}

// chainage es la progresiva del punto sobre el eje.
func (a *axis) chainage(p geo.Point) float64 {
	x, y := a.xy(p)
	return x*a.ux + y*a.uy
}

func newAxis(s *snapshot.Street) *axis {
	a := &axis{lines: s.Lines, origin: s.Lines[0][0], lo: math.Inf(1), hi: math.Inf(-1)}
	a.cos = math.Cos(a.origin[1] * math.Pi / 180)
	var n, mx, my float64
	for _, l := range s.Lines {
		for _, p := range l {
			x, y := a.xy(p)
			mx, my, n = mx+x, my+y, n+1
		}
	}
	mx, my = mx/n, my/n
	var sxx, sxy, syy float64
	for _, l := range s.Lines {
		for _, p := range l {
			x, y := a.xy(p)
			sxx += (x - mx) * (x - mx)
			sxy += (x - mx) * (y - my)
			syy += (y - my) * (y - my)
		}
	}
	th := 0.5 * math.Atan2(2*sxy, sxx-syy)
	a.ux, a.uy = math.Cos(th), math.Sin(th)
	for _, l := range s.Lines {
		for _, p := range l {
			c := a.chainage(p)
			a.lo, a.hi = min(a.lo, c), max(a.hi, c)
		}
	}
	return a
}

// at devuelve el punto de la calle con esa progresiva, recortada a su extensión.
func (a *axis) at(c float64) geo.Point {
	c = max(a.lo, min(a.hi, c))
	var best geo.Point
	bestD := math.Inf(1)
	for _, l := range a.lines {
		for i := 1; i < len(l); i++ {
			c0, c1 := a.chainage(l[i-1]), a.chainage(l[i])
			t := 0.0
			if c1 != c0 {
				t = (c - c0) / (c1 - c0)
			}
			d := 0.0
			switch {
			case t < 0:
				d, t = math.Abs(c-c0), 0
			case t > 1:
				d, t = math.Abs(c-c1), 1
			}
			if d < bestD {
				bestD = d
				best = geo.Point{l[i-1][0] + t*(l[i][0]-l[i-1][0]), l[i-1][1] + t*(l[i][1]-l[i-1][1])}
			}
		}
	}
	return best
}

// maxSideOffset acota la separación entre el eje de la calle y la puerta.
const maxSideOffset = 40.0

// toSide desplaza un punto del eje hacia el lado de la calle del número: en Lima los
// pares van de un lado y los impares del otro. Usa la separación mediana de las
// anclas de la misma paridad; sin ellas deja el punto en el eje.
func (a *axis) toSide(p geo.Point, anchors []snapshot.Anchor, num int) geo.Point {
	const r = 6371000.0
	var offs []float64
	for _, an := range anchors {
		if an.Number%2 != num%2 {
			continue
		}
		qx, qy := a.xy(a.at(a.chainage(an.Point)))
		x, y := a.xy(an.Point)
		offs = append(offs, -(x-qx)*a.uy+(y-qy)*a.ux)
	}
	if len(offs) == 0 {
		return p
	}
	sort.Float64s(offs)
	off := max(-maxSideOffset, min(maxSideOffset, offs[len(offs)/2]))
	return geo.Point{p[0] + -a.uy*off/(r*a.cos)*180/math.Pi, p[1] + a.ux*off/r*180/math.Pi}
}

// Límites de la extrapolación: las anclas deben cubrir al menos minFitRange números
// para estimar los metros por número, la pendiente se acota a lo que se ve en Lima
// (mediana 1.1 m por número) y no se extrapola más allá de maxExtrapolation números.
const (
	minFitRange      = 20
	minRate, maxRate = 0.3, 3.0
	maxExtrapolation = 600
)

// extrapolate ubica un número que queda fuera del alcance de la interpolación: parte
// del ancla más cercana en número y avanza sobre el eje con los metros por número que
// muestran las anclas de la calle.
func extrapolate(s *snapshot.Street, num int) (geo.Point, bool) {
	an := s.Anchors
	if len(an) < 2 || an[len(an)-1].Number-an[0].Number < minFitRange {
		return geo.Point{}, false
	}
	a := newAxis(s)
	near := &an[0]
	var sn, sc, snn, snc float64
	for i := range an {
		n, c := float64(an[i].Number), a.chainage(an[i].Point)
		sn, sc, snn, snc = sn+n, sc+c, snn+n*n, snc+n*c
		if iabs(an[i].Number-num) < iabs(near.Number-num) {
			near = &an[i]
		}
	}
	if iabs(near.Number-num) > maxExtrapolation {
		return geo.Point{}, false
	}
	k := float64(len(an))
	rate := (k*snc - sn*sc) / (k*snn - sn*sn)
	rate = math.Copysign(max(minRate, min(maxRate, math.Abs(rate))), rate)
	return a.toSide(a.at(a.chainage(near.Point)+rate*float64(num-near.Number)), an, num), true
}

// closestOnStreet devuelve el punto de la calle más cercano a p y su distancia en metros.
func closestOnStreet(s *snapshot.Street, p geo.Point) (geo.Point, float64) {
	const r = 6371000.0
	cos := math.Cos(p[1] * math.Pi / 180)
	proj := func(q geo.Point) (float64, float64) {
		return (q[0] - p[0]) * math.Pi / 180 * cos * r, (q[1] - p[1]) * math.Pi / 180 * r
	}
	var best geo.Point
	bestD := math.Inf(1)
	for _, l := range s.Lines {
		for i := 1; i < len(l); i++ {
			ax, ay := proj(l[i-1])
			bx, by := proj(l[i])
			dx, dy := bx-ax, by-ay
			t := 0.0
			if l2 := dx*dx + dy*dy; l2 > 0 {
				t = max(0, min(1, -(ax*dx+ay*dy)/l2))
			}
			if d := math.Hypot(ax+t*dx, ay+t*dy); d < bestD {
				bestD = d
				best = geo.Point{l[i-1][0] + t*(l[i][0]-l[i-1][0]), l[i-1][1] + t*(l[i][1]-l[i-1][1])}
			}
		}
	}
	return best, bestD
}

func iabs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
