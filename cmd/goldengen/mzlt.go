package main

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"addrsvc/internal/catalog"
	"addrsvc/internal/geo"
	"addrsvc/internal/golden"
	"addrsvc/internal/txt"
)

// Pista Mz/Lt: direcciones sin número de puerta, con manzana, lote y el nombre real
// de una urbanización, AA.HH. o asociación tomado de OSM. Es la forma más común en
// buena parte de Lima y la que la pista de calles no cubre.

// area es un área residencial con nombre, ya validada.
type area struct {
	osmID  string
	marker string   // marcador canónico (URBANIZACION, ASENTAMIENTO HUMANO...)
	words  []string // nombre sin el marcador, tal como viene en OSM
	ubigeo string
	lat    float64
	lng    float64
}

// areaMarkers son los marcadores que pueden abrir un nombre en OSM, del más largo
// al más corto.
var areaMarkers = []struct {
	words []string
	canon string
}{
	{[]string{"ASENTAMIENTO", "HUMANO"}, "ASENTAMIENTO HUMANO"},
	{[]string{"PUEBLO", "JOVEN"}, "PUEBLO JOVEN"},
	{[]string{"URBANIZACION"}, "URBANIZACION"},
	{[]string{"URB"}, "URBANIZACION"},
	{[]string{"AH"}, "ASENTAMIENTO HUMANO"},
	{[]string{"AAHH"}, "ASENTAMIENTO HUMANO"},
	{[]string{"COOPERATIVA"}, "COOPERATIVA"},
	{[]string{"ASOCIACION"}, "ASOCIACION"},
	{[]string{"RESIDENCIAL"}, "RESIDENCIAL"},
	{[]string{"CONDOMINIO"}, "CONDOMINIO"},
	{[]string{"SECTOR"}, "SECTOR"},
	{[]string{"ZONA"}, "ZONA"},
	{[]string{"GRUPO"}, "GRUPO"},
}

// forbiddenInName son palabras que, dentro del nombre de un área, cambian el parseo
// de verdad (una unidad, una referencia o una vía): esos nombres no sirven de verdad.
var forbiddenInName = map[string]bool{
	"MZ": true, "MZA": true, "MANZ": true, "MANZANA": true, "MZNA": true, "LT": true, "LTE": true,
	"LOTE": true, "INT": true, "INTERIOR": true, "DPTO": true, "DEPTO": true, "DEP": true,
	"DEPARTAMENTO": true, "OF": true, "OFIC": true, "OFICINA": true, "PISO": true, "TDA": true,
	"TIENDA": true, "STAND": true, "PUESTO": true, "REF": true, "REFERENCIA": true, "FRENTE": true,
	"CERCA": true, "ALTURA": true, "ESQUINA": true, "ESQ": true, "COSTADO": true, "ATRAS": true,
	"DETRAS": true, "LADO": true, "AV": true, "JR": true, "CL": true, "CLL": true, "PSJE": true,
	"PJE": true, "NRO": true, "SN": true, "S": true, "N": true,
	// Marcadores abreviados dentro del nombre ("Urb. Asoc. Marina"): el normalizador
	// los expande y la verdad tendría que replicar esa expansión.
	"URB": true, "ASOC": true, "COOP": true, "COND": true, "RES": true, "AAHH": true,
	"AH": true, "PJ": true,
}

// ordinalWords pasan a dígito solo antes de una palabra de agrupación ("Primera
// Etapa" -> "1 ETAPA"); en un nombre propio ("Quinta Heren") se conservan.
var ordinalWords = map[string]string{
	"PRIMERA": "1", "PRIMERO": "1", "PRIMER": "1", "SEGUNDA": "2", "SEGUNDO": "2",
	"TERCERA": "3", "TERCERO": "3", "TERCER": "3", "CUARTA": "4", "CUARTO": "4",
	"QUINTA": "5", "QUINTO": "5",
}

var groupingWords = map[string]bool{"ETAPA": true, "SECTOR": true, "ZONA": true, "GRUPO": true}

var reOrdinalSuffix = regexp.MustCompile(`^(\d+)(RA|ERA|ER|DA|DO|TA|TO|MA|VA|NA|AVO)$`)

// expectedUrbanization arma el valor esperado: marcador canónico y nombre en la
// forma de display, con los ordinales de agrupación como dígitos.
func expectedUrbanization(marker string, words []string) string {
	ws := strings.Fields(display(strings.Join(words, " ")))
	for i, w := range ws {
		if m := reOrdinalSuffix.FindStringSubmatch(w); m != nil {
			ws[i] = m[1]
			continue
		}
		if d, ok := ordinalWords[txt.NoEnye(w)]; ok && i+1 < len(ws) && groupingWords[ws[i+1]] {
			ws[i] = d
		}
	}
	return marker + " " + strings.Join(ws, " ")
}

func parseArea(name string, cat *catalog.Catalog) (marker string, words []string, reason string) {
	words = strings.Fields(name)
	keys := strings.Fields(display(name))
	// Un paréntesis es una referencia para el normalizador, no parte del nombre; y
	// "A. H." dentro del nombre es un marcador que el normalizador expande.
	if len(keys) != len(words) || strings.ContainsAny(name, "()") ||
		strings.Contains(" "+strings.Join(keys, " ")+" ", " A H ") {
		// La puntuación partió o unió palabras ("Urb.Los"): no hay correspondencia limpia.
		return "", nil, "puntuacion_ambigua"
	}
	marker = "URBANIZACION"
	for _, m := range areaMarkers {
		if len(keys) > len(m.words) && equalWords(keys[:len(m.words)], m.words) {
			marker = m.canon
			words, keys = words[len(m.words):], keys[len(m.words):]
			break
		}
	}
	for _, k := range keys {
		k = txt.NoEnye(k)
		if forbiddenInName[k] || streetTypes[k] {
			return "", nil, "palabra_que_cambia_el_parseo"
		}
	}
	if lk := cat.Lookup(txt.Key(strings.Join(keys, " "))); lk.Known() {
		return "", nil, "nombre_de_distrito"
	}
	return marker, words, ""
}

// containsOwnDistrict indica si el nombre contiene el distrito donde está el área sin
// un conector delante ("Unidad La Perla" en La Perla). Según las convenciones de
// etiquetado ese distrito es la ubicación, así que el nombre no sirve de verdad.
func containsOwnDistrict(words []string, e *catalog.Entry) bool {
	if e == nil {
		return false
	}
	keys := strings.Fields(txt.Key(strings.Join(words, " ")))
	names := []string{e.DistrictKey}
	for _, a := range e.Aliases {
		names = append(names, txt.Key(a))
	}
	connectors := map[string]bool{"DE": true, "DEL": true, "LA": true, "LAS": true, "LOS": true, "EL": true, "Y": true}
	for j := 1; j < len(keys); j++ {
		if connectors[keys[j-1]] {
			continue
		}
		for _, nm := range names {
			w := strings.Fields(nm)
			if j+len(w) <= len(keys) && strings.Join(keys[j:j+len(w)], " ") == nm {
				return true
			}
		}
	}
	return false
}

func equalWords(a, b []string) bool {
	for i := range a {
		if txt.NoEnye(a[i]) != b[i] {
			return false
		}
	}
	return true
}

func loadAreas(path string, cat *catalog.Catalog, index *geo.Districts) ([]area, loadStats, error) {
	st := loadStats{dropped: map[string]int{}}
	f, err := readOSM(path)
	if err != nil {
		return nil, st, err
	}
	seen := map[string]bool{}
	var out []area
	for _, e := range f.Elements {
		st.read++
		lat, lng := e.Lat, e.Lon
		if e.Center != nil {
			lat, lng = e.Center.Lat, e.Center.Lon
		}
		marker, words, reason := parseArea(e.Tags["name"], cat)
		if reason != "" {
			st.dropped[reason]++
			continue
		}
		codes := index.Locate(geo.Point{lng, lat})
		if len(codes) != 1 {
			st.dropped["fuera_o_en_borde"]++
			continue
		}
		if containsOwnDistrict(words, cat.ByCode(codes[0])) {
			st.dropped["nombre_con_su_distrito"]++
			continue
		}
		a := area{osmID: e.Type[:1] + strconv.FormatInt(e.ID, 10), marker: marker, words: words, ubigeo: codes[0], lat: lat, lng: lng}
		key := a.ubigeo + "|" + expectedUrbanization(marker, words)
		if seen[key] {
			st.dropped["duplicado"]++
			continue
		}
		seen[key] = true
		out = append(out, a)
	}
	return out, st, nil
}

func sampleAreas(areas []area, perDistrict int, seed uint64) []area {
	s := strconv.FormatUint(seed, 10)
	sort.Slice(areas, func(i, j int) bool {
		hi, hj := hash64(s, "area", areas[i].osmID), hash64(s, "area", areas[j].osmID)
		if hi != hj {
			return hi < hj
		}
		return areas[i].osmID < areas[j].osmID
	})
	count := map[string]int{}
	var out []area
	for _, a := range areas {
		if count[a.ubigeo] >= perDistrict {
			continue
		}
		count[a.ubigeo]++
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

// markerForms son maneras reales de escribir cada marcador. Incluyen formas que el
// léxico podría no reconocer: la evaluación debe mostrarlas.
var markerForms = map[string][]string{
	"URBANIZACION":        {"Urb.", "Urb", "URB.", "Urbanización", "urb."},
	"ASENTAMIENTO HUMANO": {"AA.HH.", "AAHH", "A.H.", "Asentamiento Humano", "AA. HH.", "A.A.H.H."},
	"PUEBLO JOVEN":        {"P.J.", "PJ", "Pueblo Joven"},
	"COOPERATIVA":         {"Coop.", "Cooperativa"},
	"ASOCIACION":          {"Asoc.", "Asociación", "ASOC.", "Asoc"},
	"RESIDENCIAL":         {"Res.", "Residencial"},
	"CONDOMINIO":          {"Cond.", "Condominio"},
	"SECTOR":              {"Sector"},
	"ZONA":                {"Zona"},
	"GRUPO":               {"Grupo"},
}

var (
	blockForms     = []string{"Mz. %s", "Mz %s", "MZ. %s", "Manzana %s", "Mza. %s", "mz %s", "Mz.%s"}
	lotForms       = []string{"Lt. %s", "Lt %s", "LT. %s", "Lote %s", "lote %s", "Lt.%s"}
	unitSeparators = []string{" ", " ", ", ", " - "}
	// Solo referencias entre paréntesis: con "Ref. ..." seguido del distrito, el
	// distrito queda dentro de la referencia y el texto es ambiguo de verdad.
	referenceForms = []string{"(frente al mercado)", "(cerca al colegio)", "(frente al parque)", "(altura del paradero)"}
)

// streetsForMzLt son vías genéricas que suelen acompañar a Mz/Lt ("Calle 5").
var streetsForMzLt = []struct{ text, typ, name string }{
	{"Calle 5", "CALLE", "5"}, {"Calle 12", "CALLE", "12"}, {"Psje. Los Pinos", "PASAJE", "LOS PINOS"},
	{"Jr. Las Flores", "JIRON", "LAS FLORES"}, {"Av. Central", "AVENIDA", "CENTRAL"},
	{"Pasaje 3", "PASAJE", "3"},
}

func (g *generator) areaRows(a area, variants int) []golden.Row {
	entry := g.cat.ByCode(a.ubigeo)
	prov, dept := titleCase(entry.Province), titleCase(entry.Department)
	district := g.districts.ByCode(a.ubigeo).Name
	urb := expectedUrbanization(a.marker, a.words)
	split := "dev"
	if hash64("split-area", a.ubigeo, urb)%100 < g.testPct {
		split = "test"
	}
	rng := rand.New(rand.NewPCG(g.seed, hash64("area", a.osmID)))
	var rows []golden.Row
	for k := 1; k <= variants; k++ {
		r := golden.Row{
			ID:                   fmt.Sprintf("MZL-%s-%d", a.osmID, k),
			Source:               "synthetic_mzlt",
			ExpectedUrbanization: urb,
			ExpectedUbigeo:       a.ubigeo,
			Lat:                  strconv.FormatFloat(a.lat, 'f', 6, 64),
			Lng:                  strconv.FormatFloat(a.lng, 'f', 6, 64),
			VerificationMethod:   "osm_area_center",
			Split:                split,
		}
		var notes []string

		block := blockValue(rng)
		lot := strconv.Itoa(1 + rng.IntN(40))
		if rng.IntN(20) == 0 {
			lot += "A"
		}
		r.ExpectedBlock, r.ExpectedLot = block, lot

		bf := pick(rng, blockForms)
		if len(block) == 1 && rng.IntN(6) == 0 {
			bf = "Mz%s" // pegado: MzB
		}
		lf := pick(rng, lotForms)
		units := fmt.Sprintf(bf, block) + pick(rng, unitSeparators) + fmt.Sprintf(lf, lot)
		notes = append(notes, "mz="+strings.TrimSpace(strings.TrimSuffix(bf, "%s")), "lt="+strings.TrimSpace(strings.TrimSuffix(lf, "%s")))

		mf := pick(rng, markerForms[a.marker])
		urbText := mf + " " + strings.Join(a.words, " ")
		notes = append(notes, "marcador="+mf)

		var addr string
		switch rng.IntN(4) {
		case 0:
			addr = units + " " + urbText
			notes = append(notes, "orden=mz_urb")
		case 1:
			addr = urbText + " " + units
			notes = append(notes, "orden=urb_mz")
		case 2:
			st := pick(rng, streetsForMzLt)
			r.ExpectedStreetType, r.ExpectedStreetName = st.typ, st.name
			addr = st.text + " " + units + " " + urbText
			notes = append(notes, "orden=via_mz_urb")
		case 3:
			st := pick(rng, streetsForMzLt)
			r.ExpectedStreetType, r.ExpectedStreetName = st.typ, st.name
			addr = urbText + " " + st.text + " " + units
			notes = append(notes, "orden=urb_via_mz")
		}
		if rng.IntN(100) < 15 {
			addr += " " + pick(rng, referenceForms)
			notes = append(notes, "referencia")
		}
		g.finishNoise(&r, addr, entry, district, prov, dept, notes, rng)
		rows = append(rows, r)
	}
	return rows
}

// blockValue devuelve una manzana como se usa en Lima: casi siempre una letra, a
// veces letra y dígito (B1) o un número.
func blockValue(rng *rand.Rand) string {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	l := string(letters[rng.IntN(len(letters))])
	switch n := rng.IntN(100); {
	case n < 75:
		return l
	case n < 88:
		return l + strconv.Itoa(1+rng.IntN(3))
	default:
		return strconv.Itoa(1 + rng.IntN(60))
	}
}
