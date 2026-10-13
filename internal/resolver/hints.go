package resolver

import (
	"math"

	"addrsvc/internal/geo"
	"addrsvc/internal/snapshot"
)

// Límites para tomar prestada la numeración de una calle paralela.
const (
	hintMaxLateral = 250.0 // metros entre las dos calles
	hintMinCos     = 0.966 // cos 15°: qué tan paralelas deben ser
	hintCell       = 0.003 // grados; celda de la grilla de búsqueda (~330 m)
)

// ParallelHints calcula, para cada calle, las anclas de las calles paralelas
// cercanas proyectadas sobre ella. En una cuadrícula las calles paralelas numeran
// sus cuadras entre los mismos cruces, así que esas anclas dicen qué centena le toca
// a cada cuadra de una calle que no tiene ninguna. También recoge las anclas del
// tramo de la misma calle en el distrito vecino, donde la numeración continúa.
func ParallelHints(streets []snapshot.Street) map[*snapshot.Street][]snapshot.Anchor {
	type source struct {
		anchor *snapshot.Anchor
		owner  *snapshot.Street
		ux, uy float64
	}
	cell := func(p geo.Point) [2]int {
		return [2]int{int(math.Floor(p[0] / hintCell)), int(math.Floor(p[1] / hintCell))}
	}
	grid := map[[2]int][]source{}
	axes := make([]*axis, len(streets))
	for i := range streets {
		s := &streets[i]
		if len(s.Lines) == 0 {
			continue
		}
		axes[i] = newAxis(s)
		for j := range s.Anchors {
			c := cell(s.Anchors[j].Point)
			grid[c] = append(grid[c], source{&s.Anchors[j], s, axes[i].ux, axes[i].uy})
		}
	}
	out := map[*snapshot.Street][]snapshot.Anchor{}
	for i := range streets {
		s, a := &streets[i], axes[i]
		if a == nil {
			continue
		}
		// Celdas que toca la calle, con un margen de una celda.
		cells := map[[2]int]bool{}
		for _, l := range s.Lines {
			for j := 1; j < len(l); j++ {
				steps := int(geo.DistanceM(l[j-1], l[j])/150) + 1
				for k := 0; k <= steps; k++ {
					t := float64(k) / float64(steps)
					c := cell(geo.Point{l[j-1][0] + t*(l[j][0]-l[j-1][0]), l[j-1][1] + t*(l[j][1]-l[j-1][1])})
					for dx := -1; dx <= 1; dx++ {
						for dy := -1; dy <= 1; dy++ {
							cells[[2]int{c[0] + dx, c[1] + dy}] = true
						}
					}
				}
			}
		}
		for c := range cells {
			for _, src := range grid[c] {
				if src.owner == s || math.Abs(a.ux*src.ux+a.uy*src.uy) < hintMinCos {
					continue
				}
				if ch := a.chainage(src.anchor.Point); ch < a.lo || ch > a.hi {
					continue
				}
				p, d := closestOnStreet(s, src.anchor.Point)
				if d > hintMaxLateral {
					continue
				}
				out[s] = append(out[s], snapshot.Anchor{Number: src.anchor.Number, Point: p, Ref: src.anchor.Ref})
			}
		}
	}
	return out
}
