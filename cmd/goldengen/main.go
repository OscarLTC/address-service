// Comando goldengen: a partir de los datos crudos de OpenStreetMap (cmd/osmfetch)
// genera dos cosas:
//
//  1. data/geo/districts.json: los límites distritales de Lima y Callao, validados
//     contra el catálogo de ubigeos.
//  2. El dataset de oro con dos pistas: osm_addr (puntos addr:* de OSM escritos
//     tal como vienen) y synthetic (las mismas direcciones con el ruido que
//     escriben las personas: abreviaturas, mayúsculas, tildes, "#", "N°", alias de
//     distrito, ubicación concatenada, interior).
//
// El parseo esperado sale de las etiquetas de OSM y el ubigeo esperado del
// polígono que contiene el punto, nunca del normalizador: así la evaluación no es
// circular. La partición es por calle y la de test queda congelada: el comando no
// sobrescribe un dataset existente sin -force.
//
//	go run ./cmd/goldengen -out goldenset/golden_v1.csv
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"log"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"addrsvc/internal/catalog"
	"addrsvc/internal/geo"
	"addrsvc/internal/golden"
	"addrsvc/internal/txt"
)

const attribution = "© OpenStreetMap contributors, ODbL 1.0 (https://www.openstreetmap.org/copyright)"

func main() {
	osmDir := flag.String("osm", "data/osm", "datos crudos de cmd/osmfetch")
	catPath := flag.String("catalog", "data/catalog/ubigeos.json", "catálogo de ubigeos")
	distOut := flag.String("districts-out", "data/geo/districts.json", "límites distritales de salida")
	out := flag.String("out", "goldenset/golden_v1.csv", "dataset de oro de salida")
	perDistrict := flag.Int("per-district", 20, "direcciones base por distrito")
	perStreet := flag.Int("per-street", 2, "máximo de direcciones base por calle y distrito")
	variants := flag.Int("variants", 2, "variantes con ruido por dirección base")
	testPct := flag.Uint64("test-pct", 30, "porcentaje de calles que van a la partición test")
	seed := flag.Uint64("seed", 1, "semilla del muestreo y del ruido")
	tol := flag.Float64("simplify", 0.00001, "tolerancia de simplificación de límites, en grados (0 = sin simplificar)")
	force := flag.Bool("force", false, "sobrescribir un dataset existente (rompe el test congelado)")
	flag.Parse()

	cat, err := catalog.Load(*catPath)
	if err != nil {
		log.Fatalf("catálogo: %v", err)
	}

	districts, stamp, err := buildDistricts(filepath.Join(*osmDir, "districts.json"), cat, *tol)
	if err != nil {
		log.Fatalf("límites: %v", err)
	}
	if err := writeDistricts(*distOut, districts, stamp, *tol); err != nil {
		log.Fatalf("límites: %v", err)
	}
	index, err := geo.NewDistricts(districts)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("%s: %d distritos", *distOut, index.Len())

	if _, err := os.Stat(*out); err == nil && !*force {
		log.Printf("%s ya existe: su partición test está congelada. Usa otro -out (nueva versión) o -force.", *out)
		return
	}

	addrs, stats, err := loadAddresses(filepath.Join(*osmDir, "addresses.json"), index)
	if err != nil {
		log.Fatalf("direcciones: %v", err)
	}
	log.Printf("puntos addr:* leídos %d; descartados: %v; utilizables %d", stats.read, stats.dropped, len(addrs))

	bases := sample(addrs, *perDistrict, *perStreet, *seed)
	g := &generator{cat: cat, districts: index, seed: *seed, testPct: *testPct}
	var rows []golden.Row
	for _, b := range bases {
		rows = append(rows, g.rows(b, *variants)...)
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	if err := golden.Write(f, rows); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
	log.Printf("%s: %d direcciones base, %d filas (datos de OSM del %s)", *out, len(bases), len(rows), stamp)
}

// --- límites distritales ---

type osmLatLon struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type osmElement struct {
	Type    string            `json:"type"`
	ID      int64             `json:"id"`
	Lat     float64           `json:"lat"`
	Lon     float64           `json:"lon"`
	Center  *osmLatLon        `json:"center"`
	Tags    map[string]string `json:"tags"`
	Members []struct {
		Type     string      `json:"type"`
		Role     string      `json:"role"`
		Geometry []osmLatLon `json:"geometry"`
	} `json:"members"`
}

type osmFile struct {
	OSM3S struct {
		Timestamp string `json:"timestamp_osm_base"`
	} `json:"osm3s"`
	Elements []osmElement `json:"elements"`
}

func readOSM(path string) (*osmFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f osmFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &f, nil
}

func buildDistricts(path string, cat *catalog.Catalog, tol float64) ([]*geo.District, string, error) {
	f, err := readOSM(path)
	if err != nil {
		return nil, "", err
	}
	var out []*geo.District
	for _, e := range f.Elements {
		code := e.Tags["pe:ubigeo"]
		entry := cat.ByCode(code)
		if entry == nil {
			return nil, "", fmt.Errorf("relación %d (%s): ubigeo %q no está en el catálogo", e.ID, e.Tags["name"], code)
		}
		if txt.Key(e.Tags["name"]) != entry.DistrictKey {
			log.Printf("aviso: %s se llama %q en OSM y %q en el catálogo", code, e.Tags["name"], entry.District)
		}
		var lines [][]geo.Point
		for _, m := range e.Members {
			if m.Type != "way" || (m.Role != "outer" && m.Role != "inner" && m.Role != "") {
				continue
			}
			line := make([]geo.Point, len(m.Geometry))
			for i, g := range m.Geometry {
				line[i] = geo.Point{g.Lon, g.Lat}
			}
			lines = append(lines, line)
		}
		rings, err := geo.AssembleRings(lines)
		if err != nil {
			return nil, "", fmt.Errorf("%s %s: %w", code, e.Tags["name"], err)
		}
		d := &geo.District{Ubigeo: code, Name: e.Tags["name"], OSMID: e.ID}
		for _, r := range rings {
			r = geo.Simplify(r, tol)
			pts := make([]geo.Point, len(r))
			for i, p := range r {
				pts[i] = geo.Point{round6(p[0]), round6(p[1])}
			}
			d.Rings = append(d.Rings, pts)
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ubigeo < out[j].Ubigeo })
	return out, f.OSM3S.Timestamp, nil
}

func writeDistricts(path string, ds []*geo.District, stamp string, tol float64) error {
	file := geo.DistrictsFile{
		Meta: map[string]any{
			"source":         "OpenStreetMap, relaciones boundary=administrative admin_level=8 con pe:ubigeo",
			"license":        attribution,
			"osm_timestamp":  stamp,
			"generated_by":   "go run ./cmd/goldengen",
			"simplify_deg":   tol,
			"note":           "No editar a mano. Coordenadas [lng, lat] en WGS84. Los límites de OSM no son oficiales: sirven para validar y desempatar, no para resolver disputas de límites.",
			"district_count": len(ds),
		},
		Districts: ds,
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(file)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// --- puntos de dirección ---

// address es un punto addr:* de OSM ya validado.
type address struct {
	osmID      string // n123, w456, r789
	street     string // addr:street tal como viene
	streetType string // tipo de vía canónico, "" si el nombre no lo trae
	nameWords  []string
	streetName string // nombre esperado (mayúsculas, sin tildes, conserva la Ñ)
	number     string
	ubigeo     string
	lat, lng   float64
}

type loadStats struct {
	read    int
	dropped map[string]int
}

// streetTypes son los tipos de vía que se aceptan escritos completos en OSM.
var streetTypes = map[string]bool{
	"AVENIDA": true, "JIRON": true, "CALLE": true, "PASAJE": true, "ALAMEDA": true,
	"MALECON": true, "PROLONGACION": true, "CARRETERA": true, "AUTOPISTA": true,
	"OVALO": true, "PLAZA": true, "PARQUE": true, "PASEO": true, "CAMINO": true,
}

// abbreviated son primeras palabras que indican un tipo de vía abreviado en OSM.
// Esos puntos se descartan: la verdad esperada debe salir de un nombre completo.
var abbreviated = map[string]bool{
	"AV": true, "AVDA": true, "JR": true, "CA": true, "CL": true, "CLL": true, "CALL": true,
	"PSJE": true, "PJE": true, "PSJ": true, "PROL": true, "CARR": true, "MZ": true, "LT": true,
}

var (
	reHouseNumber = regexp.MustCompile(`^(\d{1,5})\s*-?\s*([A-Za-z])?$`)
	reNotDisplay  = regexp.MustCompile(`[^A-Z0-9Ñ ]`)
)

// display aplica la forma esperada del normalizador para comparar nombres:
// mayúsculas, sin tildes, conserva la Ñ, sin puntuación.
func display(s string) string {
	return txt.Collapse(reNotDisplay.ReplaceAllString(txt.Fold(s, true), " "))
}

func parseStreet(s string) (streetType string, words []string, ok bool) {
	words = strings.Fields(s)
	if len(words) == 0 {
		return "", nil, false
	}
	first := display(words[0])
	if strings.Contains(words[0], ".") || abbreviated[first] {
		return "", nil, false
	}
	if streetTypes[txt.NoEnye(first)] {
		if len(words) == 1 {
			return "", nil, false
		}
		return txt.NoEnye(first), words[1:], true
	}
	return "", words, true
}

func parseNumber(s string) (string, bool) {
	m := reHouseNumber.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil || strings.TrimLeft(m[1], "0") == "" {
		return "", false
	}
	return m[1] + strings.ToUpper(m[2]), true
}

func loadAddresses(path string, index *geo.Districts) ([]address, loadStats, error) {
	st := loadStats{dropped: map[string]int{}}
	f, err := readOSM(path)
	if err != nil {
		return nil, st, err
	}
	seen := map[string]bool{}
	var out []address
	for _, e := range f.Elements {
		st.read++
		lat, lng := e.Lat, e.Lon
		if e.Center != nil {
			lat, lng = e.Center.Lat, e.Center.Lon
		}
		typ, words, ok := parseStreet(e.Tags["addr:street"])
		if !ok {
			st.dropped["calle_abreviada_o_vacia"]++
			continue
		}
		num, ok := parseNumber(e.Tags["addr:housenumber"])
		if !ok {
			st.dropped["numero_no_simple"]++
			continue
		}
		codes := index.Locate(geo.Point{lng, lat})
		if len(codes) != 1 {
			st.dropped["fuera_o_en_borde"]++
			continue
		}
		a := address{
			osmID: e.Type[:1] + strconv.FormatInt(e.ID, 10), street: e.Tags["addr:street"],
			streetType: typ, nameWords: words, streetName: display(strings.Join(words, " ")),
			number: num, ubigeo: codes[0], lat: lat, lng: lng,
		}
		key := a.ubigeo + "|" + a.streetType + "|" + txt.Key(a.streetName) + "|" + a.number
		if seen[key] {
			st.dropped["duplicado"]++
			continue
		}
		seen[key] = true
		out = append(out, a)
	}
	return out, st, nil
}

func hash64(parts ...string) uint64 {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return h.Sum64()
}

// sample elige hasta perDistrict direcciones por distrito, con tope por calle para
// no sobrerrepresentar las avenidas con muchos puntos. El orden es determinístico.
func sample(addrs []address, perDistrict, perStreet int, seed uint64) []address {
	s := strconv.FormatUint(seed, 10)
	sort.Slice(addrs, func(i, j int) bool {
		hi, hj := hash64(s, addrs[i].osmID), hash64(s, addrs[j].osmID)
		if hi != hj {
			return hi < hj
		}
		return addrs[i].osmID < addrs[j].osmID
	})
	byDistrict := map[string]int{}
	byStreet := map[string]int{}
	var out []address
	for _, a := range addrs {
		sk := a.ubigeo + "|" + txt.Key(a.streetName)
		if byDistrict[a.ubigeo] >= perDistrict || byStreet[sk] >= perStreet {
			continue
		}
		byDistrict[a.ubigeo]++
		byStreet[sk]++
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ubigeo != out[j].ubigeo {
			return out[i].ubigeo < out[j].ubigeo
		}
		return out[i].osmID < out[j].osmID
	})
	return out
}

// --- filas del dataset ---

type generator struct {
	cat       *catalog.Catalog
	districts *geo.Districts
	seed      uint64
	testPct   uint64
}

// split asigna la partición por calle: todas las direcciones de una calle caen en
// la misma, para que el test no premie memorizar calles vistas en desarrollo.
func (g *generator) split(a address) string {
	if hash64("split", a.ubigeo, txt.Key(a.streetName))%100 < g.testPct {
		return "test"
	}
	return "dev"
}

func (g *generator) rows(a address, variants int) []golden.Row {
	entry := g.cat.ByCode(a.ubigeo)
	prov, dept := titleCase(entry.Province), titleCase(entry.Department)
	district := g.districts.ByCode(a.ubigeo).Name
	base := golden.Row{
		ExpectedStreetType: a.streetType,
		ExpectedStreetName: a.streetName,
		ExpectedNumber:     a.number,
		ExpectedUbigeo:     a.ubigeo,
		Lat:                strconv.FormatFloat(a.lat, 'f', 6, 64),
		Lng:                strconv.FormatFloat(a.lng, 'f', 6, 64),
		VerificationMethod: "osm_unverified",
		Split:              g.split(a),
	}

	clean := base
	clean.ID = "OSM-" + a.osmID
	clean.Source = "osm_addr"
	clean.RawAddress = a.street + " " + a.number
	clean.DistrictField, clean.ProvinceField, clean.DepartmentField = district, prov, dept
	clean.Notes = "addr:street y addr:housenumber de OSM"
	rows := []golden.Row{clean}

	rng := rand.New(rand.NewPCG(g.seed, hash64(a.osmID)))
	for k := 1; k <= variants; k++ {
		r := base
		r.ID = fmt.Sprintf("SYN-%s-%d", a.osmID, k)
		r.Source = "synthetic"
		g.noisy(&r, a, entry, district, prov, dept, rng)
		rows = append(rows, r)
	}
	return rows
}

// typeForms son maneras reales de escribir cada tipo de vía. Incluyen formas que
// el léxico podría no reconocer: la evaluación debe mostrarlas, no esconderlas.
var typeForms = map[string][]string{
	"AVENIDA":      {"Av.", "Av", "AV.", "Avenida", "Avda.", "av."},
	"JIRON":        {"Jr.", "Jr", "JR.", "Jirón", "Jiron", "jr."},
	"CALLE":        {"Calle", "Clle.", "Cl.", "Ca.", "Cll", "calle"},
	"PASAJE":       {"Psje.", "Pje.", "Pasaje", "Psj."},
	"ALAMEDA":      {"Alameda", "Alm."},
	"MALECON":      {"Malecón", "Malecon", "Mlcn."},
	"PROLONGACION": {"Prolongación", "Prol.", "Prolong."},
	"CARRETERA":    {"Carretera", "Carr."},
	"AUTOPISTA":    {"Autopista", "Autop."},
	"OVALO":        {"Óvalo", "Ovalo", "Ov."},
	"PLAZA":        {"Plaza", "Pza."},
	"PARQUE":       {"Parque", "Pque."},
	"PASEO":        {"Paseo"},
	"CAMINO":       {"Camino", "Cam."},
}

// titleForms abrevian títulos dentro del nombre (nunca en la última palabra).
var titleForms = map[string]string{
	"GENERAL": "Gral.", "SANTA": "Sta.", "SANTO": "Sto.", "SAN": "Sn", "MARISCAL": "Mcal.",
	"ALMIRANTE": "Almte.", "COMANDANTE": "Cdte.", "CAPITAN": "Cap.", "CORONEL": "Crl.",
	"TENIENTE": "Tte.", "DOCTOR": "Dr.", "INGENIERO": "Ing.", "PROFESOR": "Prof.",
}

var numberForms = []string{"%s", "%s", "#%s", "# %s", "N° %s", "Nº %s", "Nro. %s", "Nro %s", "nro. %s", "N°%s"}

var interiorForms = []string{"Int. 3", "Dpto. 201", "Of. 502", "Piso 2", "Int 4B", "Dpto 1203"}

func pick[T any](rng *rand.Rand, xs []T) T { return xs[rng.IntN(len(xs))] }

func (g *generator) noisy(r *golden.Row, a address, entry *catalog.Entry, district, prov, dept string, rng *rand.Rand) {
	var notes []string

	// Nombre de la vía: títulos abreviados, tildes y mayúsculas.
	words := append([]string{}, a.nameWords...)
	abbr := false
	for i := 0; i < len(words)-1; i++ {
		if f, ok := titleForms[display(words[i])]; ok && rng.IntN(2) == 0 {
			words[i] = f
			abbr = true
		}
	}
	if abbr {
		notes = append(notes, "titulo_abreviado")
	}
	name := strings.Join(words, " ")

	// Tipo de vía.
	typ := ""
	if a.streetType != "" {
		// Si el nombre empieza con otro tipo de vía ("Avenida Paseo de la República"),
		// omitir el tipo vuelve el texto ambiguo de verdad: no sirve como verdad.
		if rng.IntN(100) < 8 && !streetTypes[txt.NoEnye(display(a.nameWords[0]))] {
			r.ExpectedStreetType = ""
			notes = append(notes, "tipo_omitido")
		} else {
			typ = pick(rng, typeForms[a.streetType])
			notes = append(notes, "tipo="+typ)
		}
	}

	// Número de puerta, con la letra pegada, con guion o separada.
	num := a.number
	if last := num[len(num)-1]; last >= 'A' && last <= 'Z' {
		num = pick(rng, []string{num, num[:len(num)-1] + "-" + string(last), num[:len(num)-1] + " " + string(last)})
	}
	nf := pick(rng, numberForms)
	numText := fmt.Sprintf(nf, num)
	if nf != "%s" {
		notes = append(notes, "num="+strings.TrimSpace(strings.TrimSuffix(nf, "%s")))
	}

	sep := " "
	if typ != "" && strings.HasSuffix(typ, ".") && rng.IntN(5) == 0 {
		sep = ""
		notes = append(notes, "tipo_pegado")
	}
	addr := strings.TrimSpace(typ + sep + name + " " + numText)
	if typ == "" {
		addr = name + " " + numText
	}
	if rng.IntN(100) < 15 {
		addr += " " + pick(rng, interiorForms)
		notes = append(notes, "interior")
	}

	// Ubicación: en campos, concatenada o mixta; con el nombre del distrito escrito
	// de varias maneras, incluidos sus alias. Los alias débiles ("Magdalena") no se
	// usan: son ambiguos por definición y abstenerse ante ellos es lo correcto.
	dname := district
	if aliases := entry.Aliases; len(aliases) > 0 && rng.IntN(10) < 3 {
		dname = titleCase(pick(rng, aliases))
		notes = append(notes, "alias_distrito")
	}
	mode := rng.IntN(7)
	// Si el distrito se llama igual que su provincia (Lima, Callao), un sufijo de
	// una o dos partes es ambiguo de verdad: se usa el campo o el sufijo completo.
	if txt.Key(dname) == entry.ProvinceKey && mode != 0 && mode != 4 {
		mode = 4
	}
	switch mode {
	case 0:
		r.DistrictField = dname
		notes = append(notes, "loc=campo")
	case 1:
		addr += ", " + dname
		notes = append(notes, "loc=coma")
	case 2:
		addr += " - " + dname
		notes = append(notes, "loc=guion")
	case 3:
		addr += ", " + dname + ", " + prov
		notes = append(notes, "loc=dist_prov")
	case 4:
		addr += ", " + dname + ", " + prov + ", " + dept
		notes = append(notes, "loc=dist_prov_dep")
	case 5:
		addr += " " + dname
		notes = append(notes, "loc=espacio")
	case 6:
		addr += " - " + dname
		r.ProvinceField = prov
		notes = append(notes, "loc=mixta")
	}

	switch rng.IntN(10) {
	case 0, 1:
		addr = strings.ToUpper(addr)
		notes = append(notes, "mayusculas")
	case 2:
		addr = strings.ToLower(addr)
		notes = append(notes, "minusculas")
	}
	if rng.IntN(2) == 0 {
		addr = stripAccents(addr)
		r.DistrictField = stripAccents(r.DistrictField)
		notes = append(notes, "sin_tildes")
	}
	r.RawAddress = addr
	r.Notes = strings.Join(notes, ";")
}

var accents = strings.NewReplacer(
	"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u",
	"Á", "A", "É", "E", "Í", "I", "Ó", "O", "Ú", "U", "Ü", "U",
)

// stripAccents quita tildes como lo hace quien escribe sin ellas: conserva la ñ.
func stripAccents(s string) string { return accents.Replace(s) }

func titleCase(s string) string {
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		if i > 0 && (w == "de" || w == "del" || w == "la" || w == "las" || w == "los" || w == "el") {
			continue
		}
		r := []rune(w)
		r[0] = []rune(strings.ToUpper(string(r[0])))[0]
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}
