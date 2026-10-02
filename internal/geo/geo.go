// Package geo agrupa la geometría mínima que necesita el servicio: anillos,
// polígonos con huecos y la prueba de punto en polígono. Las coordenadas van en
// grados (WGS84, SRID 4326) con el orden [lng, lat] de GeoJSON.
package geo

import (
	"errors"
	"math"
)

// Point es una coordenada [lng, lat].
type Point [2]float64

// Lng devuelve la longitud.
func (p Point) Lng() float64 { return p[0] }

// Lat devuelve la latitud.
func (p Point) Lat() float64 { return p[1] }

// Ring es un anillo cerrado: el primer punto se repite al final.
type Ring []Point

// BBox es un rectángulo envolvente.
type BBox struct {
	MinLng, MinLat, MaxLng, MaxLat float64
}

// Contains indica si el punto cae dentro del rectángulo (bordes incluidos).
func (b BBox) Contains(p Point) bool {
	return p[0] >= b.MinLng && p[0] <= b.MaxLng && p[1] >= b.MinLat && p[1] <= b.MaxLat
}

// Area es un conjunto de anillos que se evalúa con la regla par-impar: un punto
// está dentro si cruza un número impar de bordes. Así los huecos (anillos
// interiores) quedan fuera sin distinguir el rol de cada anillo, siempre que los
// anillos no se crucen entre sí, como ocurre en un multipolígono válido.
type Area struct {
	Rings []Ring
	bbox  BBox
}

// NewArea crea un área y calcula su rectángulo envolvente.
func NewArea(rings []Ring) *Area {
	a := &Area{Rings: rings}
	a.bbox = BBox{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, r := range rings {
		for _, p := range r {
			a.bbox.MinLng = math.Min(a.bbox.MinLng, p[0])
			a.bbox.MinLat = math.Min(a.bbox.MinLat, p[1])
			a.bbox.MaxLng = math.Max(a.bbox.MaxLng, p[0])
			a.bbox.MaxLat = math.Max(a.bbox.MaxLat, p[1])
		}
	}
	return a
}

// BBox devuelve el rectángulo envolvente.
func (a *Area) BBox() BBox { return a.bbox }

// Contains indica si el punto está dentro del área.
func (a *Area) Contains(p Point) bool {
	if !a.bbox.Contains(p) {
		return false
	}
	inside := false
	for _, r := range a.Rings {
		for i, j := 0, len(r)-1; i < len(r); j, i = i, i+1 {
			pi, pj := r[i], r[j]
			if (pi[1] > p[1]) != (pj[1] > p[1]) &&
				p[0] < (pj[0]-pi[0])*(p[1]-pi[1])/(pj[1]-pi[1])+pi[0] {
				inside = !inside
			}
		}
	}
	return inside
}

// ErrOpenRing indica que los tramos no forman anillos cerrados.
var ErrOpenRing = errors.New("los tramos no cierran un anillo")

// AssembleRings une tramos de borde (los ways de una relación de OSM) en anillos
// cerrados. Los tramos pueden venir en cualquier orden y sentido.
func AssembleRings(lines [][]Point) ([]Ring, error) {
	used := make([]bool, len(lines))
	var rings []Ring
	for start := range lines {
		if used[start] || len(lines[start]) < 2 {
			continue
		}
		used[start] = true
		ring := append(Ring{}, lines[start]...)
		for ring[0] != ring[len(ring)-1] {
			end := ring[len(ring)-1]
			next := -1
			reverse := false
			for k := range lines {
				if used[k] || len(lines[k]) < 2 {
					continue
				}
				if lines[k][0] == end {
					next = k
					break
				}
				if lines[k][len(lines[k])-1] == end {
					next, reverse = k, true
					break
				}
			}
			if next < 0 {
				return nil, ErrOpenRing
			}
			used[next] = true
			seg := lines[next]
			if reverse {
				for i := len(seg) - 2; i >= 0; i-- {
					ring = append(ring, seg[i])
				}
			} else {
				ring = append(ring, seg[1:]...)
			}
		}
		if len(ring) >= 4 {
			rings = append(rings, ring)
		}
	}
	return rings, nil
}

// Simplify reduce los puntos de un anillo con Douglas-Peucker. La tolerancia va
// en grados (0.00001 ≈ 1.1 m en Lima). Conserva el cierre del anillo.
func Simplify(r Ring, tol float64) Ring {
	if len(r) <= 4 || tol <= 0 {
		return r
	}
	keep := make([]bool, len(r))
	keep[0], keep[len(r)-1] = true, true
	// Un anillo cerrado tiene inicio y fin iguales: se parte en dos mitades por el
	// punto más lejano al inicio para que la recta de referencia no sea degenerada.
	far, best := 0, -1.0
	for i := range r {
		if d := dist2(r[0], r[i]); d > best {
			far, best = i, d
		}
	}
	keep[far] = true
	dp(r, 0, far, tol*tol, keep)
	dp(r, far, len(r)-1, tol*tol, keep)
	out := make(Ring, 0, len(r))
	for i, k := range keep {
		if k {
			out = append(out, r[i])
		}
	}
	if len(out) < 4 {
		return r
	}
	return out
}

func dp(r Ring, a, b int, tol2 float64, keep []bool) {
	if b <= a+1 {
		return
	}
	idx, best := -1, tol2
	for i := a + 1; i < b; i++ {
		if d := segDist2(r[i], r[a], r[b]); d > best {
			idx, best = i, d
		}
	}
	if idx < 0 {
		return
	}
	keep[idx] = true
	dp(r, a, idx, tol2, keep)
	dp(r, idx, b, tol2, keep)
}

func dist2(a, b Point) float64 {
	dx, dy := a[0]-b[0], a[1]-b[1]
	return dx*dx + dy*dy
}

func segDist2(p, a, b Point) float64 {
	l2 := dist2(a, b)
	if l2 == 0 {
		return dist2(p, a)
	}
	t := ((p[0]-a[0])*(b[0]-a[0]) + (p[1]-a[1])*(b[1]-a[1])) / l2
	t = math.Max(0, math.Min(1, t))
	return dist2(p, Point{a[0] + t*(b[0]-a[0]), a[1] + t*(b[1]-a[1])})
}
