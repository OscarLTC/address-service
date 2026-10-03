// Comando snapshotbuild: construye el snapshot del resolver a partir de los datos
// de OpenStreetMap (cmd/osmfetch): calles por distrito con su geometría, puntos
// ancla con número de puerta y centros de urbanizaciones.
//
// Los nombres se normalizan con el mismo normalizador que usa el resolver, así las
// claves de búsqueda coinciden. Los puntos addr:* que están en la partición test
// del dataset de oro no se usan como anclas: la evaluación no debe ver su respuesta.
//
//	go run ./cmd/snapshotbuild -out data/snapshot/lima.snap
//
// v0: lee OSM directamente. Cuando el plano de control tenga datos propios
// (aliases, anclas verificadas), el builder leerá de PostgreSQL.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"addrsvc/internal/catalog"
	"addrsvc/internal/geo"
	"addrsvc/internal/golden"
	"addrsvc/internal/normalizer"
	"addrsvc/internal/snapshot"
	"addrsvc/internal/txt"
)

// skipHighway son vías que no reciben direcciones.
var skipHighway = map[string]bool{
	"footway": true, "cycleway": true, "corridor": true, "steps": true, "path": true,
	"bridleway": true, "construction": true, "proposed": true, "platform": true,
	"elevator": true, "busway": true, "raceway": true,
}

// areaMarkers son prefijos que se quitan del nombre de un área para indexarla.
var areaMarkers = []string{
	"ASENTAMIENTO HUMANO", "PUEBLO JOVEN", "ASOCIACION PRO VIVIENDA", "URBANIZACION", "URB", "AAHH", "AH",
	"ASOCIACION", "ASOC", "COOPERATIVA", "COOP", "RESIDENCIAL", "CONDOMINIO", "SECTOR", "ZONA", "GRUPO", "APV",
}

var reNum = regexp.MustCompile(`^(\d{1,5})\s*[A-Za-z]?$`)

type osmElement struct {
	Type   string  `json:"type"`
	ID     int64   `json:"id"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
	Center *struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	} `json:"center"`
	Tags     map[string]string `json:"tags"`
	Geometry []struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	} `json:"geometry"`
}

func readOSM(path string) ([]osmElement, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	var f struct {
		OSM3S struct {
			Timestamp string `json:"timestamp_osm_base"`
		} `json:"osm3s"`
		Elements []osmElement `json:"elements"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}
	return f.Elements, f.OSM3S.Timestamp, nil
}

func main() {
	osmDir := flag.String("osm", "data/osm", "datos crudos de cmd/osmfetch")
	dataDir := flag.String("data", "data", "directorio de datos (catálogo, reglas, zonas, geo)")
	goldenPath := flag.String("golden", "goldenset/golden_v1.csv", "dataset de oro: sus puntos test no se usan como anclas")
	out := flag.String("out", "data/snapshot/lima.snap", "snapshot de salida")
	excludeSplit := flag.String("exclude-split", "test", "partición del dataset de oro que no se usa como ancla (test o all; all sirve para diagnosticar en dev)")
	flag.Parse()

	cat, err := catalog.Load(filepath.Join(*dataDir, "catalog", "ubigeos.json"))
	if err != nil {
		log.Fatal(err)
	}
	lex, err := normalizer.LoadLexicon(filepath.Join(*dataDir, "rules", "lexicon.json"))
	if err != nil {
		log.Fatal(err)
	}
	norm := normalizer.New(cat, lex, normalizer.Options{})
	districts, err := geo.LoadDistricts(filepath.Join(*dataDir, "geo", "districts.json"))
	if err != nil {
		log.Fatal(err)
	}
	excluded, err := goldenAnchors(*goldenPath, *excludeSplit)
	if err != nil {
		log.Fatal(err)
	}

	b := &builder{norm: norm, districts: districts, groups: map[string]*group{}, byKey: map[string][]*snapshot.Street{}}
	files, _ := filepath.Glob(filepath.Join(*osmDir, "streets", "*.json"))
	stamp := ""
	for _, f := range files {
		els, ts, err := readOSM(f)
		if err != nil {
			log.Fatal(err)
		}
		stamp = ts
		for _, e := range els {
			b.addWay(e)
		}
	}
	split := b.finalize()
	log.Printf("calles: %d en %d nombres (%d nombres con homónimas separadas; ways leídos %d, sin distrito %d, omitidos %d)",
		len(b.streets), len(b.groups), split, b.ways, b.noDistrict, b.skipped)

	addrs, _, err := readOSM(filepath.Join(*osmDir, "addresses.json"))
	if err != nil {
		log.Fatal(err)
	}
	st := b.addAnchors(addrs, excluded)
	st["descartada_inconsistente"] = b.dropInconsistent()
	log.Printf("anclas: %v", st)

	areas, _, err := readOSM(filepath.Join(*osmDir, "areas.json"))
	if err != nil {
		log.Fatal(err)
	}
	snapAreas := b.areas(areas)
	log.Printf("áreas: %d", len(snapAreas))

	s := &snapshot.Snapshot{
		Format:  snapshot.FormatVersion,
		Version: time.Now().UTC().Format("2006-01-02T150405Z"),
		Meta: map[string]string{
			"source":             "OpenStreetMap, © OpenStreetMap contributors, ODbL 1.0",
			"osm_timestamp":      stamp,
			"normalizer_version": norm.VersionString(),
			"anchors_excluded":   strconv.Itoa(len(excluded)) + " puntos (" + *excludeSplit + ") de " + *goldenPath,
		},
		Areas: snapAreas,
	}
	for _, st := range b.streets {
		s.Streets = append(s.Streets, *st)
	}
	s.SortAnchors()
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := snapshot.Save(*out, s); err != nil {
		log.Fatal(err)
	}
	fi, _ := os.Stat(*out)
	log.Printf("%s: versión %s, %d calles, %d áreas, %d KB", *out, s.Version, len(s.Streets), len(s.Areas), fi.Size()/1024)
}

// goldenAnchors devuelve los ids de OSM ("n123") de las filas del dataset de oro de
// la partición indicada (test, o all para todas).
func goldenAnchors(path, split string) (map[string]bool, error) {
	rows, err := golden.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, r := range rows {
		if split != "all" && r.Split != split {
			continue
		}
		id := strings.TrimPrefix(strings.TrimPrefix(r.ID, "OSM-"), "SYN-")
		if i := strings.LastIndex(id, "-"); i > 0 && strings.HasPrefix(r.ID, "SYN-") {
			id = id[:i]
		}
		out[id] = true
	}
	return out, nil
}

type builder struct {
	norm      *normalizer.Normalizer
	districts *geo.Districts
	groups    map[string]*group // tramos por distrito, tipo y nombre
	order     []string
	byKey     map[string][]*snapshot.Street // componentes por distrito, tipo y nombre
	streets   []*snapshot.Street

	ways, noDistrict, skipped int
}

// group reúne los tramos de OSM con el mismo nombre en un distrito.
type group struct {
	ubigeo, typ, display string
	lines                [][]geo.Point
	refs                 []string
}

// joinDistance es la distancia máxima entre tramos para considerarlos la misma calle.
const joinDistance = 30.0

// anchorDistance es la distancia máxima de un punto addr:* a su calle.
const anchorDistance = 120.0

// streetName normaliza un nombre de calle de OSM con el mismo normalizador del resolver.
func (b *builder) streetName(name string) (typ, display string) {
	r := b.norm.Normalize(normalizer.Request{Address: name})
	c := r.Components
	if c.StreetName == "" || c.Number != "" || c.Block != "" {
		return "", ""
	}
	return c.StreetType, c.StreetName
}

func streetKey(ubigeo, typ, key string) string { return ubigeo + "|" + typ + "|" + key }

func (b *builder) addWay(e osmElement) {
	b.ways++
	name := e.Tags["name"]
	if e.Type != "way" || name == "" || skipHighway[e.Tags["highway"]] || len(e.Geometry) < 2 {
		b.skipped++
		return
	}
	typ, display := b.streetName(name)
	if display == "" {
		b.skipped++
		return
	}
	line := make([]geo.Point, len(e.Geometry))
	for i, g := range e.Geometry {
		line[i] = geo.Point{g.Lon, g.Lat}
	}
	codes := b.districts.Locate(line[len(line)/2])
	if len(codes) != 1 {
		b.noDistrict++
		return
	}
	k := streetKey(codes[0], typ, txt.Key(display))
	g := b.groups[k]
	if g == nil {
		g = &group{ubigeo: codes[0], typ: typ, display: display}
		b.groups[k] = g
		b.order = append(b.order, k)
	}
	g.lines = append(g.lines, line)
	g.refs = append(g.refs, "w"+strconv.FormatInt(e.ID, 10))
}

// finalize separa cada nombre en componentes conectados: en Lima un mismo nombre se
// repite en urbanizaciones distintas del mismo distrito ("Calle Los Geranios"), y
// esas son calles distintas. Devuelve cuántos nombres tenían más de una componente.
func (b *builder) finalize() int {
	split := 0
	for _, k := range b.order {
		g := b.groups[k]
		comps := components(g.lines)
		if len(comps) > 1 {
			split++
		}
		for _, comp := range comps {
			st := &snapshot.Street{
				ID: len(b.streets) + 1, Ubigeo: g.ubigeo, Type: g.typ, Name: g.display,
				Key: txt.Key(g.display), Phonetic: txt.Phonetic(g.display),
			}
			for _, i := range comp {
				st.Lines = append(st.Lines, g.lines[i])
				st.Refs = append(st.Refs, g.refs[i])
			}
			b.streets = append(b.streets, st)
			b.byKey[k] = append(b.byKey[k], st)
		}
	}
	return split
}

// components agrupa los tramos que se tocan (a joinDistance o menos).
func components(lines [][]geo.Point) [][]int {
	parent := make([]int, len(lines))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	touch := func(a, b []geo.Point) bool {
		for _, p := range []geo.Point{a[0], a[len(a)-1]} {
			if geo.LineDistanceM(p, b) <= joinDistance {
				return true
			}
		}
		return false
	}
	for i := range lines {
		for j := i + 1; j < len(lines); j++ {
			if find(i) != find(j) && (touch(lines[i], lines[j]) || touch(lines[j], lines[i])) {
				parent[find(i)] = find(j)
			}
		}
	}
	byRoot := map[int][]int{}
	var roots []int
	for i := range lines {
		r := find(i)
		if byRoot[r] == nil {
			roots = append(roots, r)
		}
		byRoot[r] = append(byRoot[r], i)
	}
	out := make([][]int, 0, len(roots))
	for _, r := range roots {
		out = append(out, byRoot[r])
	}
	return out
}

// dropInconsistent descarta anclas que contradicen a sus vecinas: dos números del
// mismo lado separados por 20 o menos no pueden estar a más de 150 m. Si ocurre, no
// se sabe cuál está mal y se descartan ambas. Devuelve cuántas se descartaron.
func (b *builder) dropInconsistent() int {
	dropped := 0
	for _, s := range b.streets {
		bad := map[int]bool{}
		for i, a := range s.Anchors {
			for j, c := range s.Anchors {
				if j <= i || a.Number%2 != c.Number%2 {
					continue
				}
				if diff := a.Number - c.Number; diff >= -20 && diff <= 20 && geo.DistanceM(a.Point, c.Point) > 150 {
					bad[i], bad[j] = true, true
				}
			}
		}
		if len(bad) == 0 {
			continue
		}
		kept := s.Anchors[:0]
		for i, a := range s.Anchors {
			if !bad[i] {
				kept = append(kept, a)
			}
		}
		dropped += len(s.Anchors) - len(kept)
		s.Anchors = kept
	}
	return dropped
}

// nearest elige la componente más cercana al punto y su distancia.
func nearest(p geo.Point, cands []*snapshot.Street) (*snapshot.Street, float64) {
	var best *snapshot.Street
	bestD := 1e18
	for _, s := range cands {
		for _, l := range s.Lines {
			if d := geo.LineDistanceM(p, l); d < bestD {
				best, bestD = s, d
			}
		}
	}
	return best, bestD
}

// addAnchors asocia los puntos addr:* a su calle (mismo distrito y nombre).
func (b *builder) addAnchors(els []osmElement, excluded map[string]bool) map[string]int {
	st := map[string]int{}
	byName := map[string][]*snapshot.Street{} // ubigeo|key, cualquier tipo y componente
	for _, s := range b.streets {
		byName[s.Ubigeo+"|"+s.Key] = append(byName[s.Ubigeo+"|"+s.Key], s)
	}
	for _, e := range els {
		id := e.Type[:1] + strconv.FormatInt(e.ID, 10)
		if excluded[id] {
			st["excluido_test"]++
			continue
		}
		m := reNum.FindStringSubmatch(strings.TrimSpace(e.Tags["addr:housenumber"]))
		if m == nil {
			st["numero_no_simple"]++
			continue
		}
		num, _ := strconv.Atoi(m[1])
		lat, lng := e.Lat, e.Lon
		if e.Center != nil {
			lat, lng = e.Center.Lat, e.Center.Lon
		}
		p := geo.Point{lng, lat}
		codes := b.districts.Locate(p)
		if len(codes) != 1 {
			st["sin_distrito"]++
			continue
		}
		typ, display := b.streetName(e.Tags["addr:street"])
		if display == "" {
			st["calle_no_parseable"]++
			continue
		}
		cands := b.byKey[streetKey(codes[0], typ, txt.Key(display))]
		if len(cands) == 0 {
			cands = byName[codes[0]+"|"+txt.Key(display)]
		}
		s, d := nearest(p, cands)
		if s == nil {
			st["sin_calle"]++
			continue
		}
		if d > anchorDistance {
			st["lejos_de_su_calle"]++
			continue
		}
		s.Anchors = append(s.Anchors, snapshot.Anchor{Number: num, Point: p, Ref: "osm:" + id})
		st["ok"]++
	}
	return st
}

func (b *builder) areas(els []osmElement) []snapshot.Area {
	seen := map[string]bool{}
	var out []snapshot.Area
	for _, e := range els {
		lat, lng := e.Lat, e.Lon
		if e.Center != nil {
			lat, lng = e.Center.Lat, e.Center.Lon
		}
		p := geo.Point{lng, lat}
		codes := b.districts.Locate(p)
		if len(codes) != 1 {
			continue
		}
		key := AreaKey(e.Tags["name"])
		if key == "" || seen[codes[0]+"|"+key] {
			continue
		}
		seen[codes[0]+"|"+key] = true
		out = append(out, snapshot.Area{Ubigeo: codes[0], Name: e.Tags["name"], Key: key, Point: p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ubigeo+out[i].Key < out[j].Ubigeo+out[j].Key })
	return out
}

// AreaKey quita el marcador (Urb., AA.HH., Asoc.…) y devuelve la clave del nombre.
func AreaKey(name string) string {
	k := txt.Key(name)
	for _, m := range areaMarkers {
		if strings.HasPrefix(k, m+" ") {
			return strings.TrimPrefix(k, m+" ")
		}
	}
	return k
}
