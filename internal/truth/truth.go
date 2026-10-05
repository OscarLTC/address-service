// Package truth convierte observaciones GPS de entregas en coordenadas de verdad para
// una dirección, con su calidad (ADR 0005).
//
//   - Una entrega es válida si su punto está a maxArrivalGap o menos del punto de
//     "Llegada a Punto" del mismo pedido y cae dentro del distrito del pedido.
//   - Una dirección es "oro" si tiene al menos dos entregas válidas en días distintos
//     que coinciden (a maxClusterSpread o menos de su punto central).
//   - Con una sola entrega válida es "plata".
//   - Si sus entregas válidas se contradicen, la dirección se descarta: no se sabe
//     cuál es la buena.
package truth

import (
	"sort"

	"addrsvc/internal/geo"
)

const (
	maxArrivalGap    = 50.0
	maxClusterSpread = 30.0
)

// Calidades de una coordenada de verdad.
const (
	Gold   = "oro"
	Silver = "plata"
)

// Delivery es una entrega con GPS.
type Delivery struct {
	Day     string // fecha (YYYY-MM-DD)
	Point   geo.Point
	Arrival *geo.Point // punto de "Llegada a Punto", si existe
}

// Address agrupa las entregas a una misma dirección normalizada.
type Address struct {
	Key        string // match_key + ubigeo
	Ubigeo     string
	Deliveries []Delivery
}

// Result es la coordenada de verdad de una dirección, o el motivo de descarte.
type Result struct {
	Point   geo.Point
	Quality string // oro | plata | "" si se descarta
	Reason  string // motivo de descarte o de la calidad
	Valid   int    // entregas válidas usadas
}

// Locator ubica el distrito de un punto.
type Locator interface {
	Locate(p geo.Point) []string
}

// Resolve aplica las reglas del ADR 0005 a una dirección.
func Resolve(a Address, districts Locator) Result {
	var valid []Delivery
	reasons := map[string]int{}
	for _, d := range a.Deliveries {
		switch {
		case d.Arrival == nil:
			reasons["sin_llegada"]++
		case geo.DistanceM(d.Point, *d.Arrival) > maxArrivalGap:
			reasons["lejos_de_la_llegada"]++
		case !inDistrict(districts, d.Point, a.Ubigeo):
			reasons["fuera_del_distrito"]++
		default:
			valid = append(valid, d)
		}
	}
	if len(valid) == 0 {
		return Result{Reason: "sin_entregas_validas:" + top(reasons)}
	}
	center := medoid(valid)
	days := map[string]bool{}
	for _, d := range valid {
		if geo.DistanceM(d.Point, center) > maxClusterSpread {
			return Result{Reason: "entregas_contradictorias", Valid: len(valid)}
		}
		days[d.Day] = true
	}
	if len(days) >= 2 {
		return Result{Point: center, Quality: Gold, Reason: "entregas_coinciden_en_dias_distintos", Valid: len(valid)}
	}
	return Result{Point: center, Quality: Silver, Reason: "una_sola_fecha", Valid: len(valid)}
}

func inDistrict(l Locator, p geo.Point, ubigeo string) bool {
	for _, c := range l.Locate(p) {
		if c == ubigeo {
			return true
		}
	}
	return false
}

// medoid es la entrega con menor suma de distancias a las demás: un centro robusto
// que siempre es un punto observado.
func medoid(ds []Delivery) geo.Point {
	best, bestSum := ds[0].Point, -1.0
	for _, a := range ds {
		sum := 0.0
		for _, b := range ds {
			sum += geo.DistanceM(a.Point, b.Point)
		}
		if bestSum < 0 || sum < bestSum {
			best, bestSum = a.Point, sum
		}
	}
	return best
}

func top(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m[keys[i]] > m[keys[j]] || (m[keys[i]] == m[keys[j]] && keys[i] < keys[j]) })
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}
