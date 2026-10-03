// Comando geoeval: mide en metros el error del resolver contra las filas del dataset
// de oro que tienen coordenada, por nivel de precisión y por decisión.
//
// El error se juzga con los umbrales del plan: más de 50 m es error en ADDRESS_POINT
// y SEGMENT_INTERPOLATED, más de 150 m en STREET y más de 300 m en ZONE.
//
// Por defecto mide la partición test, cuyos puntos se excluyen de las anclas al
// construir el snapshot. Las coordenadas de las pistas de OSM no tienen verificación
// independiente: este número es una referencia, no la exactitud real.
//
//	go run ./cmd/geoeval -snapshot data/snapshot/lima.snap
package main

import (
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"addrsvc/internal/catalog"
	"addrsvc/internal/geo"
	"addrsvc/internal/golden"
	"addrsvc/internal/normalizer"
	"addrsvc/internal/resolver"
	"addrsvc/internal/snapshot"
)

var threshold = map[string]float64{
	resolver.AddressPoint: 50, resolver.SegmentInterpolated: 50, resolver.Street: 150, resolver.Zone: 300,
}

type bucket struct {
	errs   []float64
	failed int // fuera del umbral de su precisión
	judged int
}

func (b *bucket) add(e float64, prec string) {
	b.errs = append(b.errs, e)
	if t, ok := threshold[prec]; ok {
		b.judged++
		if e > t {
			b.failed++
		}
	}
}

func (b *bucket) pct(p float64) float64 {
	if len(b.errs) == 0 {
		return 0
	}
	s := append([]float64{}, b.errs...)
	sort.Float64s(s)
	return s[int(p*float64(len(s)-1))]
}

func (b *bucket) within(m float64) float64 {
	n := 0
	for _, e := range b.errs {
		if e <= m {
			n++
		}
	}
	return 100 * float64(n) / float64(max(1, len(b.errs)))
}

func main() {
	goldenPaths := flag.String("golden", "goldenset/golden_v1.csv", "datasets de oro con coordenada independiente del snapshot, separados por coma (la pista Mz/Lt usa como verdad el mismo centro de área del snapshot: es circular)")
	snapPath := flag.String("snapshot", "data/snapshot/lima.snap", "snapshot del resolver")
	dataDir := flag.String("data", "data", "directorio de datos")
	split := flag.String("split", "test", "partición a medir (test, dev o all)")
	examples := flag.Int("examples", 15, "peores casos a mostrar (solo con -split dev)")
	flag.Parse()

	cat, err := catalog.Load(filepath.Join(*dataDir, "catalog", "ubigeos.json"))
	if err != nil {
		log.Fatal(err)
	}
	lex, err := normalizer.LoadLexicon(filepath.Join(*dataDir, "rules", "lexicon.json"))
	if err != nil {
		log.Fatal(err)
	}
	zones, err := normalizer.LoadZones(filepath.Join(*dataDir, "config", "zones.json"))
	if err != nil {
		log.Fatal(err)
	}
	districts, err := geo.LoadDistricts(filepath.Join(*dataDir, "geo", "districts.json"))
	if err != nil {
		log.Fatal(err)
	}
	snap, err := snapshot.Load(*snapPath)
	if err != nil {
		log.Fatal(err)
	}
	res := resolver.New(normalizer.New(cat, lex, normalizer.Options{ActiveZones: zones}), snap, districts.Centroids())

	var rows []golden.Row
	for _, p := range strings.Split(*goldenPaths, ",") {
		rs, err := golden.ReadFile(strings.TrimSpace(p))
		if err != nil {
			log.Fatal(err)
		}
		rows = append(rows, rs...)
	}

	byPrec := map[string]*bucket{}
	byDecision := map[string]*bucket{}
	bySource := map[string]*bucket{}
	decisions := map[string]map[string]int{} // fuente -> decisión -> filas
	type worst struct {
		row golden.Row
		r   resolver.Result
		e   float64
	}
	var worsts []worst
	total := 0
	get := func(m map[string]*bucket, k string) *bucket {
		if m[k] == nil {
			m[k] = &bucket{}
		}
		return m[k]
	}
	for _, row := range rows {
		if (*split != "all" && row.Split != *split) || row.Lat == "" {
			continue
		}
		lat, err1 := strconv.ParseFloat(row.Lat, 64)
		lng, err2 := strconv.ParseFloat(row.Lng, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		total++
		r := res.Geocode(normalizer.Request{Address: row.RawAddress, District: row.DistrictField,
			Province: row.ProvinceField, Department: row.DepartmentField})
		if decisions[row.Source] == nil {
			decisions[row.Source] = map[string]int{}
		}
		decisions[row.Source][r.Decision]++
		if r.Location == nil {
			continue
		}
		e := resolver.Distance(geo.Point{lng, lat}, geo.Point{r.Location.Lng, r.Location.Lat})
		get(byPrec, r.PrecisionLevel).add(e, r.PrecisionLevel)
		get(byDecision, r.Decision).add(e, r.PrecisionLevel)
		get(bySource, row.Source).add(e, r.PrecisionLevel)
		if t, ok := threshold[r.PrecisionLevel]; *split == "dev" && r.Decision == resolver.AutoAccept && ok && e > t {
			worsts = append(worsts, worst{row, r, e})
		}
	}

	fmt.Printf("# Error del resolver en metros\n\n- Snapshot: `%s` (versión %s)\n- Partición: %s (%d filas con coordenada)\n- Umbrales de error: 50 m (punto o interpolado), 150 m (calle), 300 m (zona)\n\n", *snapPath, snap.Version, *split, total)
	table := func(title string, m map[string]*bucket, order []string) {
		fmt.Printf("| %s | Filas | Mediana | p90 | ≤ 50 m | ≤ 150 m | Error según umbral |\n|---|---:|---:|---:|---:|---:|---:|\n", title)
		for _, k := range order {
			b := m[k]
			if b == nil {
				continue
			}
			errTxt := "—"
			if b.judged > 0 {
				errTxt = fmt.Sprintf("%.1f %%", 100*float64(b.failed)/float64(b.judged))
			}
			fmt.Printf("| %s | %d | %.0f m | %.0f m | %.1f %% | %.1f %% | %s |\n", k, len(b.errs), b.pct(0.5), b.pct(0.9), b.within(50), b.within(150), errTxt)
		}
		fmt.Println()
	}
	table("Decisión", byDecision, []string{resolver.AutoAccept, resolver.AcceptFlagged, resolver.Review})
	table("Precisión", byPrec, []string{resolver.AddressPoint, resolver.SegmentInterpolated, resolver.Street, resolver.Zone, resolver.District})
	srcs := make([]string, 0, len(bySource))
	for k := range bySource {
		srcs = append(srcs, k)
	}
	sort.Strings(srcs)
	table("Fuente", bySource, srcs)

	fmt.Printf("## Decisiones por fuente\n\n| Fuente | AUTO_ACCEPT | ACCEPT_FLAGGED | REVIEW | REJECT |\n|---|---:|---:|---:|---:|\n")
	for _, s := range srcs {
		d := decisions[s]
		n := 0
		for _, v := range d {
			n += v
		}
		f := func(k string) string { return fmt.Sprintf("%d (%.0f %%)", d[k], 100*float64(d[k])/float64(max(1, n))) }
		fmt.Printf("| %s | %s | %s | %s | %s |\n", s, f(resolver.AutoAccept), f(resolver.AcceptFlagged), f(resolver.Review), f(resolver.Reject))
	}
	fmt.Println()

	if *split == "dev" && *examples > 0 {
		sort.Slice(worsts, func(i, j int) bool { return worsts[i].e > worsts[j].e })
		fmt.Printf("## Errores en AUTO_ACCEPT en dev (%d filas)\n\n", len(worsts))
		for i, w := range worsts {
			if i == *examples {
				break
			}
			fmt.Printf("- `%s` %q → %s %s (%s, %s, score %.2f) a %.0f m\n", w.row.ID, w.row.RawAddress, w.r.Canonical, w.r.PrecisionLevel, w.r.ResolutionType, w.r.Decision, w.r.Score, w.e)
		}
	}
}
