// Comando goldeneval: corre el normalizador sobre el dataset de oro y reporta la
// exactitud del parseo por campo, la métrica de la compuerta G1 (todos los campos
// esperados correctos a la vez: vía, número, Mz/Lt, urbanización y ubigeo), la
// idempotencia y dónde falla.
//
// Los ejemplos de fallos se muestran solo de la partición dev: la de test está
// congelada y solo se reporta en cifras, para no ajustar reglas mirándola.
//
//	go run ./cmd/goldeneval -golden goldenset/golden_v1.csv,goldenset/golden_mzlt_v1.csv
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"addrsvc/internal/catalog"
	"addrsvc/internal/golden"
	"addrsvc/internal/normalizer"
)

// fields son los campos comparados, en el orden del reporte.
var fields = []string{"street_type", "street_name", "number", "block", "lot", "urbanization", "ubigeo"}

func expected(r golden.Row, f string) string {
	switch f {
	case "street_type":
		return r.ExpectedStreetType
	case "street_name":
		return r.ExpectedStreetName
	case "number":
		return r.ExpectedNumber
	case "block":
		return r.ExpectedBlock
	case "lot":
		return r.ExpectedLot
	case "urbanization":
		return r.ExpectedUrbanization
	case "ubigeo":
		return r.ExpectedUbigeo
	}
	panic("campo desconocido: " + f)
}

func got(res normalizer.Result, f string) string {
	c := res.Components
	switch f {
	case "street_type":
		return c.StreetType
	case "street_name":
		return c.StreetName
	case "number":
		return c.Number
	case "block":
		return c.Block
	case "lot":
		return c.Lot
	case "urbanization":
		return c.Urbanization
	case "ubigeo":
		return res.Location.Ubigeo
	}
	panic("campo desconocido: " + f)
}

// tally acumula aciertos por campo y de la métrica G1.
type tally struct {
	rows, core int
	ok         map[string]int
}

func newTally() *tally { return &tally{ok: map[string]int{}} }

func (t *tally) add(okFields map[string]bool) bool {
	t.rows++
	core := true
	for _, f := range fields {
		if okFields[f] {
			t.ok[f]++
		}
		core = core && okFields[f]
	}
	if core {
		t.core++
	}
	return core
}

func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return 100 * float64(n) / float64(d)
}

type failure struct {
	row  golden.Row
	res  normalizer.Result
	diff []string
}

func main() {
	goldenPaths := flag.String("golden", "goldenset/golden_v1.csv,goldenset/golden_mzlt_v1.csv", "datasets de oro, separados por coma")
	dataDir := flag.String("data", "data", "directorio de datos (catálogo, reglas, zonas)")
	examples := flag.Int("examples", 25, "ejemplos de fallos de dev a mostrar")
	minCore := flag.Float64("min-core", 0, "si es > 0, termina con error cuando la métrica G1 de test queda por debajo (en %)")
	flag.Parse()

	cat, err := catalog.Load(filepath.Join(*dataDir, "catalog", "ubigeos.json"))
	if err != nil {
		log.Fatalf("catálogo: %v", err)
	}
	lex, err := normalizer.LoadLexicon(filepath.Join(*dataDir, "rules", "lexicon.json"))
	if err != nil {
		log.Fatalf("léxico: %v", err)
	}
	zones, err := normalizer.LoadZones(filepath.Join(*dataDir, "config", "zones.json"))
	if err != nil {
		log.Fatalf("zonas: %v", err)
	}
	norm := normalizer.New(cat, lex, normalizer.Options{ActiveZones: zones})
	var rows []golden.Row
	for _, p := range strings.Split(*goldenPaths, ",") {
		rs, err := golden.ReadFile(strings.TrimSpace(p))
		if err != nil {
			log.Fatalf("dataset %s: %v", p, err)
		}
		rows = append(rows, rs...)
	}

	bySplit := map[string]*tally{}
	bySource := map[string]*tally{}
	byDistrict := map[string]*tally{}
	byTag := map[string]*tally{}
	// ubiOutcome separa, por partición, el ubigeo equivocado (el error peligroso) de
	// la abstención (sin ubigeo): abstenerse ante una ambigüedad real es correcto.
	ubiOutcome := map[string]map[string]int{}
	flagCount := map[string]int{}
	fieldFail := map[string]int{}
	var fails []failure
	idemFail := 0
	var idemExamples []string

	for _, r := range rows {
		res := norm.Normalize(normalizer.Request{
			Address: r.RawAddress, District: r.DistrictField,
			Province: r.ProvinceField, Department: r.DepartmentField,
		})
		okFields := map[string]bool{}
		var diff []string
		for _, f := range fields {
			e, g := expected(r, f), got(res, f)
			okFields[f] = e == g
			if e != g {
				diff = append(diff, fmt.Sprintf("%s: esperado %q, obtenido %q", f, e, g))
				if r.Split == "dev" {
					fieldFail[f]++
				}
			}
		}
		if ubiOutcome[r.Split] == nil {
			ubiOutcome[r.Split] = map[string]int{}
		}
		switch g := res.Location.Ubigeo; {
		case g == r.ExpectedUbigeo:
			ubiOutcome[r.Split]["correcto"]++
		case g == "":
			ubiOutcome[r.Split]["abstencion"]++
		default:
			ubiOutcome[r.Split]["equivocado"]++
		}
		get(bySplit, r.Split).add(okFields)
		get(bySource, r.Split+" / "+r.Source).add(okFields)
		get(byDistrict, r.ExpectedUbigeo).add(okFields)
		if strings.HasPrefix(r.Source, "synthetic") {
			for _, tag := range strings.Split(r.Notes, ";") {
				get(byTag, tag).add(okFields)
			}
		}
		for _, f := range res.Flags {
			flagCount[f]++
		}
		if len(diff) > 0 && r.Split == "dev" {
			fails = append(fails, failure{row: r, res: res, diff: diff})
		}

		// Idempotencia: normalizar el texto ya normalizado, con la ubicación ya
		// detectada (como en las pruebas del normalizador), no debe cambiarlo.
		again := norm.Normalize(normalizer.Request{Address: res.Normalized, Ubigeo: res.Location.Ubigeo})
		if again.Normalized != res.Normalized {
			idemFail++
			if len(idemExamples) < 5 {
				idemExamples = append(idemExamples, fmt.Sprintf("%q -> %q", res.Normalized, again.Normalized))
			}
		}
	}

	fmt.Printf("# Evaluación del normalizador sobre el dataset de oro\n\n")
	fmt.Printf("- Datasets: `%s` (%d filas)\n- Normalizador: `%s`\n", *goldenPaths, len(rows), norm.VersionString())
	fmt.Printf("- Métrica G1: todos los campos esperados correctos a la vez (meta de partida: 95 %%)\n\n")

	fmt.Printf("## Resumen\n\n")
	printTable("Partición", bySplit, sortedKeys(bySplit))
	printTable("Partición / fuente", bySource, sortedKeys(bySource))
	fmt.Printf("| Partición | Ubigeo correcto | Abstención | Ubigeo equivocado |\n|---|---:|---:|---:|\n")
	for _, k := range sortedKeys(bySplit) {
		o, rows := ubiOutcome[k], bySplit[k].rows
		fmt.Printf("| %s | %d (%.1f %%) | %d (%.1f %%) | **%d (%.1f %%)** |\n", k,
			o["correcto"], pct(o["correcto"], rows), o["abstencion"], pct(o["abstencion"], rows),
			o["equivocado"], pct(o["equivocado"], rows))
	}
	fmt.Println()
	fmt.Printf("Idempotencia: %d de %d (%.1f %%)\n", len(rows)-idemFail, len(rows), pct(len(rows)-idemFail, len(rows)))
	for _, e := range idemExamples {
		fmt.Printf("- %s\n", e)
	}
	fmt.Println()

	fmt.Printf("## Exactitud G1 por transformación (pistas sintéticas, dev y test)\n\n")
	tags := sortedKeys(byTag)
	sort.SliceStable(tags, func(i, j int) bool {
		return pct(byTag[tags[i]].core, byTag[tags[i]].rows) < pct(byTag[tags[j]].core, byTag[tags[j]].rows)
	})
	fmt.Printf("| Transformación | Filas | G1 |\n|---|---:|---:|\n")
	for _, k := range tags {
		t := byTag[k]
		fmt.Printf("| `%s` | %d | %.1f %% |\n", k, t.rows, pct(t.core, t.rows))
	}
	fmt.Println()

	fmt.Printf("## Distritos con menor exactitud G1\n\n")
	ds := sortedKeys(byDistrict)
	sort.SliceStable(ds, func(i, j int) bool {
		return pct(byDistrict[ds[i]].core, byDistrict[ds[i]].rows) < pct(byDistrict[ds[j]].core, byDistrict[ds[j]].rows)
	})
	fmt.Printf("| Ubigeo | Distrito | Filas | G1 |\n|---|---|---:|---:|\n")
	for i, k := range ds {
		if i == 10 {
			break
		}
		name := ""
		if e := cat.ByCode(k); e != nil {
			name = e.District
		}
		t := byDistrict[k]
		fmt.Printf("| %s | %s | %d | %.1f %% |\n", k, name, t.rows, pct(t.core, t.rows))
	}
	fmt.Println()

	fmt.Printf("## Flags más frecuentes\n\n| Flag | Filas |\n|---|---:|\n")
	for i, k := range sortedByCount(flagCount) {
		if i == 15 {
			break
		}
		fmt.Printf("| `%s` | %d |\n", k, flagCount[k])
	}
	fmt.Println()

	fmt.Printf("## Fallos en dev (%d filas)\n\n", len(fails))
	fmt.Printf("Por campo: ")
	var parts []string
	for _, f := range fields {
		if fieldFail[f] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", f, fieldFail[f]))
		}
	}
	fmt.Printf("%s\n\n", strings.Join(parts, ", "))
	for i, f := range fails {
		if i == *examples {
			break
		}
		fmt.Printf("- `%s` %q", f.row.ID, f.row.RawAddress)
		if f.row.DistrictField != "" || f.row.ProvinceField != "" {
			fmt.Printf(" [distrito=%q provincia=%q]", f.row.DistrictField, f.row.ProvinceField)
		}
		fmt.Printf("\n  - %s\n  - flags: %s\n", strings.Join(f.diff, "; "), strings.Join(f.res.Flags, ", "))
	}

	if test := bySplit["test"]; *minCore > 0 && test != nil && pct(test.core, test.rows) < *minCore {
		fmt.Fprintf(os.Stderr, "G1 en test %.1f %% por debajo del mínimo %.1f %%\n", pct(test.core, test.rows), *minCore)
		os.Exit(1)
	}
}

func get(m map[string]*tally, k string) *tally {
	if m[k] == nil {
		m[k] = newTally()
	}
	return m[k]
}

func printTable(title string, m map[string]*tally, keys []string) {
	fmt.Printf("| %s | Filas | G1 |", title)
	for _, f := range fields {
		fmt.Printf(" %s |", f)
	}
	fmt.Printf("\n|---|---:|---:|%s\n", strings.Repeat("---:|", len(fields)))
	for _, k := range keys {
		t := m[k]
		fmt.Printf("| %s | %d | **%.1f %%** |", k, t.rows, pct(t.core, t.rows))
		for _, f := range fields {
			fmt.Printf(" %.1f |", pct(t.ok[f], t.rows))
		}
		fmt.Println()
	}
	fmt.Println()
}

func sortedKeys(m map[string]*tally) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedByCount(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}
