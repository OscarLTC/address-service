// Comando catalogbuild: genera data/catalog/ubigeos.json a partir del Excel de
// ubigeos del INEI. Conserva los alias, los nombres con tildes y las zonas de la
// semilla; el resto de distritos recibe una zona por departamento.
//
//	go run ./cmd/catalogbuild -xlsx "docs/ref/UBIGEO 2022_1891 distritos.xlsx"
package main

import (
	"archive/zip"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"addrsvc/internal/catalog"
	"addrsvc/internal/txt"
)

func main() {
	xlsxPath := flag.String("xlsx", "docs/ref/UBIGEO 2022_1891 distritos.xlsx", "Excel de ubigeos del INEI")
	seedPath := flag.String("seed", "data/catalog/ubigeos_seed.json", "semilla con alias, tildes y zonas a conservar")
	outPath := flag.String("out", "data/catalog/ubigeos.json", "catálogo de salida")
	flag.Parse()

	rows, err := readFirstSheet(*xlsxPath)
	if err != nil {
		log.Fatalf("excel: %v", err)
	}
	seed, err := loadSeed(*seedPath)
	if err != nil {
		log.Fatalf("semilla: %v", err)
	}
	entries, err := buildEntries(rows, seed)
	if err != nil {
		log.Fatal(err)
	}
	for code := range seed {
		if !hasCode(entries, code) {
			log.Fatalf("el ubigeo %s de la semilla no está en el Excel", code)
		}
	}

	out := map[string]any{
		"_meta": map[string]any{
			"source":       "INEI, " + baseName(*xlsxPath),
			"generated_by": "go run ./cmd/catalogbuild",
			"note":         "No editar a mano: los alias y las zonas de Lima y Callao salen de ubigeos_seed.json. Los distritos creados después del Excel se agregan en la semilla.",
			"count":        len(entries),
		},
		"entries": entries,
	}
	f, err := os.Create(*outPath)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(out); err != nil {
		log.Fatal(err)
	}
	// Valida que el resultado cargue igual que en el servidor.
	if _, err := catalog.Load(*outPath); err != nil {
		log.Fatalf("el catálogo generado no carga: %v", err)
	}
	fmt.Printf("%s: %d distritos\n", *outPath, len(entries))
}

var (
	reCode   = regexp.MustCompile(`^\d{6}$`)
	reParens = regexp.MustCompile(`^(.*?)\s*\(([^)]*)\)\s*$`)
)

// buildEntries convierte las filas del Excel (IDDIST, NOMBDEP, NOMBPROV, NOMBDIST)
// en entradas del catálogo. Descarta el encabezado y las notas al pie.
func buildEntries(rows [][]string, seed map[string]*catalog.Entry) ([]*catalog.Entry, error) {
	var entries []*catalog.Entry
	seen := map[string]bool{}
	for _, r := range rows {
		if len(r) < 4 || !reCode.MatchString(strings.TrimSpace(r[0])) {
			continue
		}
		code := strings.TrimSpace(r[0])
		if seen[code] {
			return nil, fmt.Errorf("ubigeo duplicado en el Excel: %s", code)
		}
		seen[code] = true

		dist, alias := cleanName(r[3])
		e := &catalog.Entry{
			Code:        code,
			Department:  clean(r[1]),
			Province:    clean(r[2]),
			District:    dist,
			Aliases:     []string{},
			WeakAliases: []string{},
		}
		if alias != "" {
			e.Aliases = append(e.Aliases, alias)
		}
		e.Zone = zoneFor(e)
		if s := seed[code]; s != nil {
			mergeSeed(e, s)
		}
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("el Excel no tiene filas con ubigeo de 6 dígitos")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Code < entries[j].Code })
	return entries, nil
}

// mergeSeed conserva de la semilla los nombres con tildes (si son el mismo nombre),
// los alias y la zona.
func mergeSeed(e, s *catalog.Entry) {
	if txt.Key(s.District) == txt.Key(e.District) {
		e.District = s.District
	}
	if txt.Key(s.Province) == txt.Key(e.Province) {
		e.Province = s.Province
	}
	if txt.Key(s.Department) == txt.Key(e.Department) {
		e.Department = s.Department
	}
	e.Aliases = union(e.Aliases, s.Aliases)
	e.WeakAliases = union(e.WeakAliases, s.WeakAliases)
	if s.Zone != "" {
		e.Zone = s.Zone
	}
}

// zoneFor asigna la zona por defecto: Lima Metropolitana, Callao, el resto del
// departamento de Lima y un departamento por zona para el resto del país.
func zoneFor(e *catalog.Entry) string {
	switch prov := e.Code[:4]; {
	case prov == "1501":
		return "LIMA_METRO"
	case prov == "0701":
		return "CALLAO"
	case e.Code[:2] == "15":
		return "LIMA_PROVINCIAS"
	}
	return strings.ReplaceAll(txt.Key(e.Department), " ", "_")
}

// cleanName corrige el nombre de distrito: "ANCO_HUALLO" pasa a "ANCO HUALLO" y
// "QUISQUI (KICHKI)" se separa en nombre y alias.
func cleanName(s string) (name, alias string) {
	s = clean(s)
	if m := reParens.FindStringSubmatch(s); m != nil {
		return m[1], m[2]
	}
	return s, ""
}

func clean(s string) string {
	return txt.Collapse(strings.ToUpper(strings.ReplaceAll(s, "_", " ")))
}

func union(a, b []string) []string {
	out := append([]string{}, a...)
	for _, x := range b {
		dup := false
		for _, y := range out {
			if txt.Key(x) == txt.Key(y) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, x)
		}
	}
	return out
}

func hasCode(entries []*catalog.Entry, code string) bool {
	i := sort.Search(len(entries), func(i int) bool { return entries[i].Code >= code })
	return i < len(entries) && entries[i].Code == code
}

func baseName(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	return p[strings.LastIndex(p, "/")+1:]
}

func loadSeed(path string) (map[string]*catalog.Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f struct {
		Entries []*catalog.Entry `json:"entries"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	out := map[string]*catalog.Entry{}
	for _, e := range f.Entries {
		out[e.Code] = e
	}
	return out, nil
}

// readFirstSheet lee la primera hoja de un .xlsx como filas de texto. Solo
// soporta lo que usa el Excel del INEI: textos compartidos, textos en línea y números.
func readFirstSheet(path string) ([][]string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	var shared []string
	if f := findFile(zr, "xl/sharedStrings.xml"); f != nil {
		var sst struct {
			Items []struct {
				T    string `xml:"t"`
				Runs []struct {
					T string `xml:"t"`
				} `xml:"r"`
			} `xml:"si"`
		}
		if err := decodeXML(f, &sst); err != nil {
			return nil, fmt.Errorf("sharedStrings: %w", err)
		}
		for _, it := range sst.Items {
			s := it.T
			for _, r := range it.Runs {
				s += r.T
			}
			shared = append(shared, s)
		}
	}

	sheet := findFile(zr, "xl/worksheets/sheet1.xml")
	if sheet == nil {
		return nil, fmt.Errorf("no se encontró xl/worksheets/sheet1.xml")
	}
	var ws struct {
		Rows []struct {
			Cells []struct {
				Ref    string `xml:"r,attr"`
				Type   string `xml:"t,attr"`
				Value  string `xml:"v"`
				Inline string `xml:"is>t"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := decodeXML(sheet, &ws); err != nil {
		return nil, fmt.Errorf("hoja: %w", err)
	}

	var rows [][]string
	for _, r := range ws.Rows {
		var row []string
		for _, c := range r.Cells {
			col := colIndex(c.Ref)
			for len(row) <= col {
				row = append(row, "")
			}
			switch c.Type {
			case "s":
				i, err := strconv.Atoi(c.Value)
				if err != nil || i < 0 || i >= len(shared) {
					return nil, fmt.Errorf("celda %s: índice de texto inválido %q", c.Ref, c.Value)
				}
				row[col] = shared[i]
			case "inlineStr":
				row[col] = c.Inline
			default:
				row[col] = c.Value
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func findFile(zr *zip.ReadCloser, name string) *zip.File {
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, name) {
			return f
		}
	}
	return nil
}

func decodeXML(f *zip.File, v any) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	return xml.Unmarshal(data, v)
}

// colIndex convierte la referencia de celda ("C12") en índice de columna (2).
func colIndex(ref string) int {
	n := 0
	for _, r := range ref {
		if r < 'A' || r > 'Z' {
			break
		}
		n = n*26 + int(r-'A'+1)
	}
	return n - 1
}
