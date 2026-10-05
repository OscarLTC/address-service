// Comando sampleprofile: pasa una muestra de direcciones reales (exportada con
// scripts/emsd/muestra_direcciones.sql) por el normalizador y resume cómo llegan:
// qué se parsea, qué flags aparecen y si la ubicación detectada coincide con el
// ubigeo que ya tiene el sistema de origen.
//
// La muestra contiene direcciones de clientes: vive en data/private/ y la salida de
// este comando no debe versionarse.
//
//	go run ./cmd/sampleprofile -in data/private/emsd_muestra.csv
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"addrsvc/internal/catalog"
	"addrsvc/internal/normalizer"
)

type sample struct {
	id, cuenta, direccion, numero, referencia string
	distrito, provincia, departamento, ubigeo string
}

func main() {
	in := flag.String("in", "data/private/emsd_muestra.csv", "muestra exportada de EMS-D")
	dataDir := flag.String("data", "data", "directorio de datos (catálogo, reglas, zonas)")
	examples := flag.Int("examples", 30, "ejemplos a mostrar por grupo")
	outCSV := flag.String("out-csv", "", "si se indica, escribe la propuesta del normalizador por fila (solo dentro de data/private/)")
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

	rows, err := readSamples(*in)
	if err != nil {
		log.Fatal(err)
	}

	type outcome struct {
		s        sample
		withF    normalizer.Result // con los campos de ubicación, como llamaría EMS-D
		textOnly normalizer.Result // solo el texto: mide la detección desde el texto
	}
	var all []outcome
	flagsF := map[string]int{}
	var locF, locT = map[string]int{}, map[string]int{}
	shape := map[string]int{}
	for _, s := range rows {
		o := outcome{s: s}
		o.withF = norm.Normalize(normalizer.Request{Address: s.direccion, Number: s.numero, Reference: s.referencia,
			District: s.distrito, Province: s.provincia, Department: s.departamento})
		o.textOnly = norm.Normalize(normalizer.Request{Address: s.direccion, Number: s.numero})
		all = append(all, o)
		for _, f := range o.withF.Flags {
			flagsF[f]++
		}
		locF[compare(o.withF.Location.Ubigeo, s.ubigeo)]++
		locT[compare(o.textOnly.Location.Ubigeo, s.ubigeo)]++
		shape[shapeOf(o.withF.Components)]++
	}

	if *outCSV != "" {
		if !strings.HasPrefix(filepath.ToSlash(filepath.Clean(*outCSV)), "data/private/") {
			log.Fatalf("-out-csv debe quedar dentro de data/private/: la salida contiene direcciones de clientes")
		}
		results := make([]normalizer.Result, len(all))
		for i, o := range all {
			results[i] = o.withF
		}
		if err := writeProposals(*outCSV, rows, results); err != nil {
			log.Fatal(err)
		}
	}

	n := len(all)
	fmt.Printf("# Perfil de la muestra (%d direcciones)\n\n", n)
	fmt.Printf("Normalizador: %s\n\n", norm.VersionString())
	fmt.Printf("## Ubicación frente al ubigeo del sistema de origen\n\n")
	fmt.Printf("| Resultado | Con campos | Solo texto |\n|---|---:|---:|\n")
	for _, k := range []string{"igual", "abstencion", "distinto"} {
		fmt.Printf("| %s | %d (%.1f %%) | %d (%.1f %%) |\n", k, locF[k], pct(locF[k], n), locT[k], pct(locT[k], n))
	}
	fmt.Printf("\n## Forma de la dirección (con campos)\n\n| Forma | Filas |\n|---|---:|\n")
	for _, k := range sortedByCount(shape) {
		fmt.Printf("| %s | %d (%.1f %%) |\n", k, shape[k], pct(shape[k], n))
	}
	fmt.Printf("\n## Flags (con campos)\n\n| Flag | Filas |\n|---|---:|\n")
	for _, k := range sortedByCount(flagsF) {
		fmt.Printf("| `%s` | %d (%.1f %%) |\n", k, flagsF[k], pct(flagsF[k], n))
	}

	show := func(title string, keep func(outcome) bool) {
		fmt.Printf("\n## %s\n\n", title)
		c := 0
		for _, o := range all {
			if !keep(o) {
				continue
			}
			if c < *examples {
				r := o.withF
				fmt.Printf("- `%s` %q [%s / %s / %s ubigeo=%s]\n  - %q | tipo=%q nombre=%q num=%q mz=%q lt=%q urb=%q ref=%q int=%q\n  - ubigeo=%q flags=%s\n",
					o.s.id, o.s.direccion+suffix(o.s.numero), o.s.distrito, o.s.provincia, o.s.departamento, o.s.ubigeo,
					r.Normalized, r.Components.StreetType, r.Components.StreetName, r.Components.Number, r.Components.Block,
					r.Components.Lot, r.Components.Urbanization, r.Components.Reference, r.Components.Interior,
					r.Location.Ubigeo, strings.Join(r.Flags, ","))
			}
			c++
		}
		fmt.Printf("\n(%d filas en total)\n", c)
	}
	show("Ubigeo distinto al del origen (con campos)", func(o outcome) bool {
		return compare(o.withF.Location.Ubigeo, o.s.ubigeo) == "distinto"
	})
	show("Ubigeo distinto al del origen (solo texto)", func(o outcome) bool {
		return compare(o.textOnly.Location.Ubigeo, o.s.ubigeo) == "distinto"
	})
	show("Abstención de ubicación (con campos)", func(o outcome) bool {
		return compare(o.withF.Location.Ubigeo, o.s.ubigeo) == "abstencion"
	})
	show("Sin tipo de vía ni Mz/Lt", func(o outcome) bool {
		c := o.withF.Components
		return c.StreetType == "" && c.Block == "" && c.Lot == ""
	})
	show("Con texto movido a referencia", func(o outcome) bool {
		return has(o.withF.Flags, "TRAILING_TEXT_MOVED_TO_REFERENCE")
	})
	show("Muestra general", func(o outcome) bool { return true })
}

// writeProposals escribe, por fila de la muestra, lo que propone el normalizador
// (llamado con los campos de ubicación). Es la base de la planilla de etiquetado.
func writeProposals(path string, rows []sample, results []normalizer.Result) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	head := []string{"id_muestra", "cuenta", "direccion", "numero", "referencia", "distrito", "provincia",
		"departamento", "ubigeo", "p_tipo_via", "p_nombre_via", "p_numero", "p_interior", "p_mz", "p_lt",
		"p_urbanizacion", "p_referencia", "p_ubigeo", "p_normalizada", "p_flags"}
	if err := w.Write(head); err != nil {
		return err
	}
	for i, s := range rows {
		r := results[i]
		c := r.Components
		if err := w.Write([]string{s.id, s.cuenta, s.direccion, s.numero, s.referencia, s.distrito, s.provincia,
			s.departamento, s.ubigeo, c.StreetType, c.StreetName, c.Number, c.Interior, c.Block, c.Lot,
			c.Urbanization, c.Reference, r.Location.Ubigeo, r.Normalized, strings.Join(r.Flags, " ")}); err != nil {
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	return f.Close()
}

func suffix(num string) string {
	if num == "" {
		return ""
	}
	return " [num=" + num + "]"
}

func compare(got, want string) string {
	switch {
	case got == want:
		return "igual"
	case got == "":
		return "abstencion"
	default:
		return "distinto"
	}
}

// shapeOf clasifica la dirección por los componentes que se extrajeron.
func shapeOf(c normalizer.Components) string {
	var parts []string
	if c.StreetType != "" || c.StreetName != "" {
		parts = append(parts, "vía")
	}
	if c.Number != "" {
		parts = append(parts, "número")
	}
	if c.Block != "" || c.Lot != "" {
		parts = append(parts, "Mz/Lt")
	}
	if c.Urbanization != "" {
		parts = append(parts, "urbanización")
	}
	if c.Interior != "" {
		parts = append(parts, "interior")
	}
	if c.Reference != "" {
		parts = append(parts, "referencia")
	}
	if len(parts) == 0 {
		return "(vacía)"
	}
	return strings.Join(parts, " + ")
}

func has(flags []string, f string) bool {
	for _, x := range flags {
		if x == f {
			return true
		}
	}
	return false
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
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

func readSamples(path string) ([]sample, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	head, err := r.Read()
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i, h := range head {
		idx[strings.TrimSpace(h)] = i
	}
	for _, c := range []string{"id_muestra", "cuenta", "direccion", "numero", "referencia", "distrito", "provincia", "departamento", "ubigeo"} {
		if _, ok := idx[c]; !ok {
			return nil, fmt.Errorf("falta la columna %q", c)
		}
	}
	var out []sample
	for {
		rec, err := r.Read()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		get := func(c string) string { return strings.TrimSpace(rec[idx[c]]) }
		out = append(out, sample{
			id: get("id_muestra"), cuenta: get("cuenta"), direccion: get("direccion"), numero: get("numero"),
			referencia: get("referencia"), distrito: get("distrito"), provincia: get("provincia"),
			departamento: get("departamento"), ubigeo: get("ubigeo"),
		})
	}
}
