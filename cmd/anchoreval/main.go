// Comando anchoreval: mide el error de ubicación del número sobre la calle reservando
// anclas. Para cada cuadra con anclas (misma calle y misma centena) las quita todas,
// ubica cada número con lo que queda y mide la distancia a su ancla. Es el caso de
// una dirección en una cuadra de la que no se sabe nada. Con -hold anchor reserva
// cada ancla sola: una dirección en una cuadra con otros números conocidos.
//
// Las anclas de OSM suelen estar en el centro del edificio, a 10-20 m del eje de la
// calle: ese es el piso del error. No tienen verificación independiente (ADR 0005).
//
//	go run ./cmd/anchoreval -snapshot data/snapshot/lima.snap -ubigeo 150122
package main

import (
	"flag"
	"fmt"
	"log"
	"sort"
	"strconv"

	"addrsvc/internal/geo"
	"addrsvc/internal/resolver"
	"addrsvc/internal/snapshot"
)

type bucket []float64

func (b bucket) pct(p float64) float64 {
	if len(b) == 0 {
		return 0
	}
	s := append([]float64{}, b...)
	sort.Float64s(s)
	return s[int(p*float64(len(s)-1))]
}

func (b bucket) within(m float64) float64 {
	n := 0
	for _, e := range b {
		if e <= m {
			n++
		}
	}
	return 100 * float64(n) / float64(max(1, len(b)))
}

func main() {
	snapPath := flag.String("snapshot", "data/snapshot/lima.snap", "snapshot del resolver")
	ubigeo := flag.String("ubigeo", "", "distrito a medir (vacío: todos)")
	noBlocks := flag.Bool("no-blocks", false, "ignora los cruces: mide sin el modelo de cuadras")
	noHints := flag.Bool("no-parallel", false, "mide sin la numeración de las calles paralelas")
	hold := flag.String("hold", "block", "qué se reserva: block (la cuadra), street (todas las anclas de la calle) o anchor (cada ancla sola)")
	flag.Parse()

	snap, err := snapshot.Load(*snapPath)
	if err != nil {
		log.Fatal(err)
	}
	hints := map[*snapshot.Street][]snapshot.Anchor{}
	if !*noHints {
		hints = resolver.ParallelHints(snap.Streets)
	}
	by := map[string]bucket{}
	var all bucket
	streets, alone := 0, 0
	for i := range snap.Streets {
		s := snap.Streets[i]
		if (*ubigeo != "" && s.Ubigeo != *ubigeo) || len(s.Anchors) == 0 {
			continue
		}
		if *noBlocks {
			s.Crossings = nil
		}
		streets++
		hundreds := map[int]bool{}
		for _, a := range s.Anchors {
			hundreds[a.Number/100] = true
		}
		if len(hundreds) == 1 {
			alone += len(s.Anchors) // sin la cuadra no queda ninguna ancla en la calle
		}
		if *hold == "street" {
			hundreds = map[int]bool{-1: true}
		}
		if *hold == "anchor" {
			for k, a := range s.Anchors {
				rest := s
				rest.Anchors = append(append([]snapshot.Anchor{}, s.Anchors[:k]...), s.Anchors[k+1:]...)
				p, prec, note := resolver.Locate(&rest, strconv.Itoa(a.Number), hints[&snap.Streets[i]])
				e := geo.DistanceM(p, a.Point)
				key := prec
				if note != "" {
					key += " (" + note + ")"
				}
				by[key] = append(by[key], e)
				all = append(all, e)
			}
			continue
		}
		for h := range hundreds {
			rest := s
			rest.Anchors = nil
			var held []snapshot.Anchor
			for _, a := range s.Anchors {
				if h == -1 || a.Number/100 == h {
					held = append(held, a)
				} else {
					rest.Anchors = append(rest.Anchors, a)
				}
			}
			for _, a := range held {
				p, prec, note := resolver.Locate(&rest, strconv.Itoa(a.Number), hints[&snap.Streets[i]])
				e := geo.DistanceM(p, a.Point)
				k := prec
				if note != "" {
					k += " (" + note + ")"
				}
				by[k] = append(by[k], e)
				all = append(all, e)
			}
		}
	}

	fmt.Printf("# Error de ubicación reservando: %s\n\n- Snapshot: `%s` (versión %s)\n- Distrito: %s\n- Calles con anclas: %d; anclas medidas: %d (%d en calles con una sola cuadra con anclas)\n\n",
		*hold, *snapPath, snap.Version, orAll(*ubigeo), streets, len(all), alone)
	fmt.Printf("| Método | Anclas | Mediana | p75 | p90 | ≤ 50 m | ≤ 100 m | ≤ 150 m |\n|---|---:|---:|---:|---:|---:|---:|---:|\n")
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(by[keys[i]]) > len(by[keys[j]]) })
	row := func(k string, b bucket) {
		fmt.Printf("| %s | %d | %.0f m | %.0f m | %.0f m | %.1f %% | %.1f %% | %.1f %% |\n", k, len(b), b.pct(0.5), b.pct(0.75), b.pct(0.9), b.within(50), b.within(100), b.within(150))
	}
	for _, k := range keys {
		row(k, by[k])
	}
	row("**Total**", all)
}

func orAll(u string) string {
	if u == "" {
		return "todos"
	}
	return u
}
