// Package normalizer implementa la normalización determinística de direcciones
// peruanas (capas de limpieza y estandarización). No corrige nombres dudosos:
// eso pertenece a la resolución y siempre lleva un nivel de confianza.
package normalizer

import (
	"regexp"
	"strings"

	"addrsvc/internal/catalog"
	"addrsvc/internal/txt"
)

// Version identifica el conjunto de reglas del código. Súbela al cambiar el comportamiento.
const Version = "0.8.0"

// Request es la entrada. Todos los campos de ubicación son opcionales.
type Request struct {
	Address    string `json:"address"`
	District   string `json:"district,omitempty"`
	Province   string `json:"province,omitempty"`
	Department string `json:"department,omitempty"`
	Ubigeo     string `json:"ubigeo,omitempty"`
}

// Components son las partes de la dirección ya separadas.
type Components struct {
	StreetType   string `json:"street_type,omitempty"`
	StreetName   string `json:"street_name,omitempty"`
	Number       string `json:"number,omitempty"`
	Interior     string `json:"interior,omitempty"`
	Block        string `json:"block,omitempty"`
	Lot          string `json:"lot,omitempty"`
	Urbanization string `json:"urbanization,omitempty"`
	Reference    string `json:"reference,omitempty"`
}

// Location es la ubicación administrativa detectada.
type Location struct {
	Department string `json:"department,omitempty"`
	Province   string `json:"province,omitempty"`
	District   string `json:"district,omitempty"`
	Ubigeo     string `json:"ubigeo,omitempty"`
	Zone       string `json:"zone,omitempty"`
	InScope    bool   `json:"in_scope"`
	// Source: UBIGEO | FIELD | TEXT | DEFAULT | NONE
	Source string `json:"source"`
}

// Result es la salida del normalizador.
type Result struct {
	Raw         string     `json:"raw"`
	Normalized  string     `json:"normalized"`
	Components  Components `json:"components"`
	Location    Location   `json:"location"`
	MatchKey    string     `json:"match_key"`
	PhoneticKey string     `json:"phonetic_key"`
	Flags       []string   `json:"flags"`
	Version     string     `json:"normalizer_version"`
}

// Options configura la cobertura y los valores por defecto del cliente.
type Options struct {
	// ActiveZones indica qué zonas de cobertura están activas.
	ActiveZones map[string]bool
	// DefaultDepartment y DefaultProvince se usan si no se detecta ubicación.
	DefaultDepartment string
	DefaultProvince   string
}

// Normalizer es seguro para uso concurrente: no muta estado después de New.
type Normalizer struct {
	cat  *catalog.Catalog
	lex  *Lexicon
	opts Options
}

// New crea un normalizador.
func New(cat *catalog.Catalog, lex *Lexicon, opts Options) *Normalizer {
	return &Normalizer{cat: cat, lex: lex, opts: opts}
}

// VersionString devuelve la versión combinada de código y léxico.
func (n *Normalizer) VersionString() string {
	return "normalizer/" + Version + " lexicon/" + n.lex.Version
}

var (
	reParens   = regexp.MustCompile(`\(([^)]*)\)`)
	reSN       = regexp.MustCompile(`\bS\s*/\s*N\b`)
	reNroMark  = regexp.MustCompile(`#|\bN[°º]\.?|\bNRO\b\.?|\bNUM(?:ERO)?\b\.?`)
	reAAHH     = regexp.MustCompile(`\bA\.?\s?A\.?\s?H\.?\s?H\b\.?`)
	reAH       = regexp.MustCompile(`\bA\.\s?H\b\.?`) // "A.H" con punto tras la A: "A H" suelto es ambiguo
	reAsentH   = regexp.MustCompile(`\bASENT\.?\s?H(?:UM)?\b\.?`)
	reAPV      = regexp.MustCompile(`\bA\.\s?P\.\s?V\b\.?`)
	reFloorNum = regexp.MustCompile(`\b(\d{1,2})\s?(?:ER|RO|DO|DA|TO|TA|VO|NO|MO|RA)?\.?\s+(?:PISO|NIVEL)\b`)
	reFloorOrd = regexp.MustCompile(`\b(PRIMER|PRIMERO|PRIMERA|SEGUNDO|SEGUNDA|TERCER|TERCERO|TERCERA|CUARTO|CUARTA|QUINTO|QUINTA)\s+(?:PISO|NIVEL)\b`)
	reDigitsAt = regexp.MustCompile(`^\s+\d`)
	reGlueMzLt = regexp.MustCompile(`^MZ([A-Z]?\d{0,3}[A-Z]?)LT(\d+[A-Z]?)$`)
	reLetDig   = regexp.MustCompile(`\b([A-Z]{1,2})-(\d+)\b`) // "B-204" -> "B204"
	reLetLet   = regexp.MustCompile(`([A-Z])-([A-Z])`)        // "ROSALES-COMAS": el guion separa
	reValue    = regexp.MustCompile(`^([A-Z]{1,2}\d+[A-Z]?|\d+[A-Z]{0,2}|[A-Z])$`)
	reCode     = regexp.MustCompile(`^[A-Z]{1,2}\d+[A-Z]?$`)
	reShortNum = regexp.MustCompile(`^\d{1,2}$`)
	reSep      = regexp.MustCompile(`[,;]|\s-+\s?|-+\s`)
	reNumWord  = regexp.MustCompile(`(\d)-+([A-Z]{2})`) // "302-MIRAFLORES": el guion separa
	reUbigeo   = regexp.MustCompile(`^\d{6}$`)
	rePostal   = regexp.MustCompile(`^(0[1-9]|1\d|2[0-5])\d{3}$`)
	rePJ       = regexp.MustCompile(`\bP\.\s?J\b\.?`)
	reNumLetHy = regexp.MustCompile(`(\d)-([A-Z])\b`)
	rePunct    = regexp.MustCompile(`[^A-Z0-9Ñ\s]`)
	reGlueMz   = regexp.MustCompile(`^MZ([A-Z]\d?|\d+)$`)
	reGlue     = regexp.MustCompile(`^(LT|LOTE|INT|DPTO|DPT|DP|OF|PISO|CALLE|CL|CLL|AV|JR|PSJE|PJE)(\d+[A-Z]?)$`)
	reNumber   = regexp.MustCompile(`^\d+[A-Z]?$`)
	reOrdinal  = regexp.MustCompile(`^(\d+)(RA|ERA|ER|DA|DO|TA|TO|MA|VA|NA|AVO)$`)
)

type flagSet struct{ list []string }

func (f *flagSet) add(flag string) {
	for _, x := range f.list {
		if x == flag {
			return
		}
	}
	f.list = append(f.list, flag)
}

func (f *flagSet) remove(flag string) {
	for i, x := range f.list {
		if x == flag {
			f.list = append(f.list[:i], f.list[i+1:]...)
			return
		}
	}
}

// Normalize procesa una dirección. Nunca falla: los problemas se reportan en Flags.
func (n *Normalizer) Normalize(req Request) Result {
	res := Result{Raw: req.Address, Version: n.VersionString()}
	flags := &flagSet{}

	tokens, sepBefore, refs := n.tokenize(req.Address, flags)
	tokens, sepBefore = dedupeRepeat(tokens, sepBefore, flags)
	if len(tokens) == 0 {
		flags.add("EMPTY_ADDRESS")
	}

	var tail []string
	tokens, tail = n.cutReference(tokens, sepBefore)
	refs = append(refs, tail...)
	tokens = n.stripTail(tokens, sepBefore, flags)
	tokens, sepBefore = n.stripPrefix(tokens, sepBefore, req, &refs, flags)
	// Un tipo de vía suelto al final ("... urb Pro Lima calle") no tiene nombre.
	// Solo los tipos que casi nunca terminan un nombre ("Urb. Villa Parque" sí).
	if l := len(tokens); l > 1 && tokens[l-1] != "C" {
		if t := n.lex.streetType[txt.NoEnye(tokens[l-1])]; t == "CALLE" || t == "AVENIDA" || t == "JIRON" || t == "PASAJE" {
			refs = append([]string{tokens[l-1]}, refs...)
			tokens = tokens[:l-1]
		}
	}

	suffix, rest := n.scanSuffix(tokens, sepBefore)
	sres := n.resolveNames(suffix, areaContext{province: txt.Key(req.Province), department: txt.Key(req.Department)})
	switch {
	case len(suffix) == 0:
	case len(rest) == 0 || (len(rest) == 1 && n.isStructural(rest[0])):
		flags.add("LOCATION_SUFFIX_SKIPPED")
		suffix, sres = nil, suffixResult{}
	case districtGiven(req) && !sepBefore[len(rest)] && n.continuesUrbanization(rest, sepBefore):
		// El cliente ya dio el distrito y el sufijo, sin coma ni guion, continúa el
		// nombre de una urbanización ("Urb. Los Robles Salamanca", "Urb. Nuevo Lurín"):
		// es parte del nombre, no la ubicación.
		flags.add("DISTRICT_NAME_IN_TEXT")
		suffix, sres = nil, suffixResult{}
	case !sres.OK:
		flags.add("LOCATION_SUFFIX_UNRESOLVED")
		for _, f := range sres.Flags {
			flags.add(f)
		}
		suffix, sres = nil, suffixResult{}
	default:
		tokens = rest
	}

	res.Location = n.locate(req, sres, flags)
	n.parse(tokens, &res, flags)
	if res.Location.Source == "NONE" && n.districtInName(res.Components.Urbanization) {
		flags.add("DISTRICT_NAME_IN_URBANIZATION")
	}
	if len(refs) > 0 {
		// Se suma al texto final que el parseo ya movió a la referencia.
		res.Components.Reference = strings.TrimSpace(res.Components.Reference + " " + strings.Join(refs, " "))
	}
	n.finish(&res, flags)
	res.Flags = flags.list
	if res.Flags == nil {
		res.Flags = []string{}
	}
	return res
}

// stripTail quita del final lo que no es dirección ni ubicación: el país, un ubigeo
// de 6 dígitos o un código postal de 5 ("..., Lima 15801, Perú", "... 150140"). El
// país solo se quita tras una coma o un guion, para no romper "Mi Perú".
func (n *Normalizer) stripTail(tokens []string, sepBefore []bool, flags *flagSet) []string {
	for len(tokens) > 1 {
		last := tokens[len(tokens)-1]
		switch {
		case last == "PERU" && (sepBefore[len(tokens)-1] || reNumber.MatchString(tokens[len(tokens)-2]) ||
			n.cat.Lookup(txt.Key(tokens[len(tokens)-2])).Known()):
			flags.add("COUNTRY_REMOVED")
		case reShortNum.MatchString(last) && len(tokens) > 2 && sepBefore[len(tokens)-2] &&
			(tokens[len(tokens)-2] == "LIMA" || tokens[len(tokens)-2] == "CALLAO"):
			// "..., Lima 20": distrito postal antiguo.
			flags.add("POSTAL_CODE_IN_TEXT")
			tokens = tokens[:len(tokens)-1]
		case reUbigeo.MatchString(last) && n.cat.ByCode(last) != nil:
			flags.add("UBIGEO_IN_TEXT")
		case rePostal.MatchString(last) && hasNumberBefore(tokens[:len(tokens)-1]):
			flags.add("POSTAL_CODE_IN_TEXT")
		default:
			return tokens
		}
		tokens = tokens[:len(tokens)-1]
	}
	return tokens
}

func hasNumberBefore(tokens []string) bool {
	for _, t := range tokens {
		if reNumber.MatchString(t) {
			return true
		}
	}
	return false
}

// continuesUrbanization indica si el final de rest es el nombre de una urbanización
// todavía abierta: hacia atrás se llega a su marcador sin pasar por un número, una
// unidad, un tipo de vía ni un separador.
//
// Con un designador corto tras ZONA, SECTOR, GRUPO o ETAPA ("zona C", "sector 3") el
// nombre ya está completo: lo que sigue no lo continúa.
func (n *Normalizer) continuesUrbanization(rest []string, sepBefore []bool) bool {
	for i := len(rest) - 1; i >= 0; i-- {
		t := rest[i]
		if n.isUrb(t) {
			canon := n.lex.urb[txt.NoEnye(t)]
			short := i == len(rest)-2 && len(rest[i+1]) <= 2
			return !(short && designators[canon])
		}
		if _, isType := n.lex.streetType[txt.NoEnye(t)]; isType || n.isUnit(t) || reNumber.MatchString(t) || sepBefore[i] {
			return false
		}
	}
	return false
}

// designators son marcadores que suelen llevar solo una letra o un número.
var designators = map[string]bool{"ZONA": true, "SECTOR": true, "GRUPO": true, "ETAPA": true}

// stripPrefix quita del inicio lo que no es la dirección: el distrito del pedido
// antes de la urbanización o la Mz ("Villa El Salvador Sector 3 ..."), o un
// establecimiento separado de la vía ("Plaza Norte - Av. ..."), que va a referencia.
func (n *Normalizer) stripPrefix(tokens []string, sepBefore []bool, req Request, refs *[]string, flags *flagSet) ([]string, []bool) {
	if dk := txt.Key(req.District); dk != "" {
		for k := 1; k < len(tokens) && k <= n.cat.MaxWords(); k++ {
			if txt.Key(strings.Join(tokens[:k], " ")) == dk && (n.isUrb(tokens[k]) || n.isUnit(tokens[k])) {
				flags.add("DISTRICT_PREFIX_REMOVED")
				return tokens[k:], sepBefore[k:]
			}
		}
	}
	for s := 1; s < len(tokens)-1 && s <= 4; s++ {
		if !sepBefore[s] {
			continue
		}
		seg := tokens[:s]
		_, isType := n.lex.streetType[txt.NoEnye(tokens[s])]
		if isType && tokens[s] != "C" && !anyDigit(seg) && !n.isUrb(seg[0]) && !n.isUnit(seg[0]) {
			*refs = append(append([]string{}, seg...), *refs...)
			flags.add("ESTABLISHMENT_PREFIX")
			return tokens[s:], sepBefore[s:]
		}
		break
	}
	return tokens, sepBefore
}

// districtGiven indica si la petición trae el distrito en un campo o por ubigeo.
func districtGiven(req Request) bool {
	return strings.TrimSpace(req.District) != "" || strings.TrimSpace(req.Ubigeo) != ""
}

// sepMark reemplaza comas, punto y coma y guiones separadores durante la limpieza;
// solo letras, para sobrevivir a la eliminación de puntuación.
const sepMark = "QSEPQ"

// tokenize aplica las capas N0-N2: limpieza, tokenización y separación de pegados.
// sepBefore[i] indica si en el texto original había una coma o un guion separador
// justo antes de tokens[i]; tiene un elemento más para el final del texto.
func (n *Normalizer) tokenize(raw string, flags *flagSet) (tokens []string, sepBefore []bool, refs []string) {
	s := txt.Fold(txt.FixMojibake(raw), true)
	if n.lex.noisePrefix != nil && n.lex.noisePrefix.MatchString(s) {
		s = n.lex.noisePrefix.ReplaceAllString(s, " ")
		flags.add("NOISE_PREFIX_REMOVED")
	}
	for _, m := range reParens.FindAllStringSubmatch(s, -1) {
		if r := txt.Collapse(rePunct.ReplaceAllString(m[1], " ")); r != "" {
			refs = append(refs, r)
		}
	}
	s = reParens.ReplaceAllString(s, " ")
	s = reSN.ReplaceAllString(s, " SINNUMERO ")
	s = reNroMark.ReplaceAllString(s, " NRO ")
	s = reAAHH.ReplaceAllString(s, " AAHH ")
	s = reAH.ReplaceAllString(s, " AAHH ")
	s = reAsentH.ReplaceAllString(s, " AAHH ")
	s = reAPV.ReplaceAllString(s, " APV ")
	s = rePJ.ReplaceAllString(s, " PJ ")
	s = n.floorFirst(s)
	s = reNumLetHy.ReplaceAllString(s, "$1$2")
	s = reLetDig.ReplaceAllString(s, "$1$2")
	s = reNumWord.ReplaceAllString(s, "$1 - $2")
	s = reLetLet.ReplaceAllString(s, "$1 - $2")
	s = reSep.ReplaceAllString(s, " "+sepMark+" ")
	s = rePunct.ReplaceAllString(s, " ")
	s = n.lex.applyPhrases(txt.Collapse(s))

	sep := false
	add := func(ts ...string) {
		for i, t := range ts {
			tokens = append(tokens, t)
			sepBefore = append(sepBefore, sep && i == 0)
		}
		sep = false
	}
	fields := strings.Fields(s)
	for i, t := range fields {
		switch {
		case t == sepMark:
			sep = true
		case t == "NRO":
			continue
		case t == "SN" && len(tokens) > 0 && (i+1 == len(fields) || fields[i+1] == sepMark || n.isUnit(fields[i+1]) || n.isUrb(fields[i+1])):
			// "Jr. X sn - Torre 2": SN tras el nombre es "sin número", no "San".
			add("SINNUMERO")
		case reGlueMzLt.MatchString(t):
			m := reGlueMzLt.FindStringSubmatch(t)
			add("MZ", m[1], "LT", m[2])
		case t == "MZA" && i+1 < len(fields) && n.isBlockValue(fields[i+1]):
			// "Mza. J": abreviatura de manzana, no "Mz A" pegado.
			add("MZ")
		case reGlueMz.MatchString(t):
			m := reGlueMz.FindStringSubmatch(t)
			add("MZ", m[1])
		case reGlue.MatchString(t):
			m := reGlue.FindStringSubmatch(t)
			add(m[1], m[2])
		default:
			add(t)
		}
	}
	sepBefore = append(sepBefore, sep)
	return tokens, sepBefore, refs
}

// floorFirst reescribe el piso escrito antes de su marcador como "PISO n": "2 piso",
// "1er piso", "2do nivel", "primer piso". No toca "Calle 5 piso 2": si al marcador le
// sigue un número, el número de antes no es el piso.
func (n *Normalizer) floorFirst(s string) string {
	rewrite := func(re *regexp.Regexp, value func(string) string) {
		var b strings.Builder
		last := 0
		for _, m := range re.FindAllStringSubmatchIndex(s, -1) {
			if reDigitsAt.MatchString(s[m[1]:]) {
				continue
			}
			b.WriteString(s[last:m[0]])
			b.WriteString(" PISO " + value(s[m[2]:m[3]]) + " ")
			last = m[1]
		}
		b.WriteString(s[last:])
		s = b.String()
	}
	rewrite(reFloorNum, func(d string) string { return d })
	rewrite(reFloorOrd, func(w string) string { return n.lex.ordinals[w] })
	return s
}

// dedupeRepeat quita una copia repetida de la dirección al final del texto, aunque
// la copia venga truncada ("Mz K3 Lote 9 Sector X Mz K3 Lote 9 Sector").
func dedupeRepeat(tokens []string, sepBefore []bool, flags *flagSet) ([]string, []bool) {
	n := len(tokens)
	for k := 3; k <= n-3; k++ {
		m := n - k
		if m > k {
			continue
		}
		same := true
		for j := 0; j < m && same; j++ {
			a, b := tokens[k+j], tokens[j]
			if j == m-1 {
				same = strings.HasPrefix(b, a)
			} else {
				same = a == b
			}
		}
		if same {
			flags.add("DUPLICATED_TEXT")
			return tokens[:k], append(sepBefore[:k:k], sepBefore[n])
		}
	}
	return tokens, sepBefore
}

// unitApplies descarta marcadores que también son palabras de nombres propios:
// "Torre" es unidad en "Torre 3" pero no en "Haya de la Torre".
func (n *Normalizer) unitApplies(tokens []string, i int) bool {
	if !ambiguousUnits[n.lex.unit[txt.NoEnye(tokens[i])]] {
		return true
	}
	if i > 0 && nameConnectors[tokens[i-1]] {
		return false
	}
	return i+1 < len(tokens) && len(tokens[i+1]) <= 4 && reValue.MatchString(tokens[i+1])
}

// ambiguousUnits son marcadores que también son palabras comunes: solo cuentan como
// unidad si les sigue un valor ("Casa 7", "Torre B", "Tda 146A").
var ambiguousUnits = map[string]bool{"TORRE": true, "CASA": true, "TDA": true, "LOCAL": true, "SOTANO": true, "STAND": true}

// isBlockValue indica si el token puede ser el valor de una manzana (J, B1, 12).
func (n *Normalizer) isBlockValue(t string) bool {
	return len(t) <= 3 && t != sepMark && !n.isUnit(t) && !n.isUrb(t) && !reGlue.MatchString(t)
}

// cutReference separa lo que viene después de "frente a", "cerca de", etc.
// "Casa" sin valor es descripción ("casa de 2 pisos") salvo dentro del nombre de una
// urbanización ("Urb. Casa del Adulto Mayor"); al final o tras un separador, siempre lo es.
func (n *Normalizer) cutReference(tokens []string, sepBefore []bool) (addr, ref []string) {
	for i := 2; i < len(tokens); i++ {
		casaDesc := n.lex.unit[txt.NoEnye(tokens[i])] == "CASA" && !n.unitApplies(tokens, i) &&
			(i == len(tokens)-1 || sepBefore[i] || !n.continuesUrbanization(tokens[:i], sepBefore))
		if n.lex.refMarkers[txt.NoEnye(tokens[i])] || casaDesc {
			cut := i
			if p := tokens[i-1]; p == "AL" || p == "A" || p == "POR" {
				cut = i - 1
			}
			return tokens[:cut], tokens[cut:]
		}
	}
	return tokens, nil
}

func (n *Normalizer) isStructural(tok string) bool {
	k := txt.NoEnye(tok)
	_, a := n.lex.streetType[k]
	_, b := n.lex.urb[k]
	_, c := n.lex.unit[k]
	return a || b || c
}

func (n *Normalizer) isUnit(tok string) bool {
	_, ok := n.lex.unit[txt.NoEnye(tok)]
	return ok
}

func (n *Normalizer) isUrb(tok string) bool {
	_, ok := n.lex.urb[txt.NoEnye(tok)]
	return ok
}

// parse asigna roles: tipo de vía, nombre, número, Mz/Lt, interior y urbanización.
func (n *Normalizer) parse(tokens []string, res *Result, flags *flagSet) {
	c := &res.Components
	i := 0
	if len(tokens) > 0 {
		if canon, ok := n.lex.streetType[txt.NoEnye(tokens[0])]; ok {
			if tokens[0] == "C" {
				flags.add("GUARDED_C_AS_CALLE")
			}
			if tokens[0] != canon {
				flags.add("ABBREVIATION_EXPANDED")
			}
			c.StreetType = canon
			i = 1
		} else {
			flags.add("NO_STREET_TYPE")
		}
	}

	var street, urb, trailing, interior, loose, pendingEtapa []string
	inUrb, seenUnit, seenBlockLot, inStreet := false, false, false, false
	// closeLoose decide qué era el texto sin marcador que siguió a Mz/Lt: una vía
	// con número ("Los Jazmines 240"), una etapa que precede al marcador, texto
	// suelto antes de otra urbanización, o el nombre de un lugar sin marcador.
	closeLoose := func(beforeMarker bool) {
		if len(loose) == 0 {
			return
		}
		last := loose[len(loose)-1]
		switch {
		case beforeMarker && indexOf(loose, "ETAPA") >= 0:
			pendingEtapa = append(pendingEtapa, loose...)
		case len(street) == 0 && c.StreetType == "" && len(loose) > 1 && reNumber.MatchString(last) && len(last) >= 2:
			street = append(street, loose...)
			flags.add("STREET_AFTER_BLOCK")
		case beforeMarker || len(urb) > 0:
			trailing = append(trailing, loose...)
		default:
			urb = append(urb, loose...)
			flags.add("URBANIZATION_WITHOUT_MARKER")
		}
		loose = nil
	}
	for i < len(tokens) {
		tk := tokens[i]
		k := txt.NoEnye(tk)
		switch {
		case n.startsLateStreet(tokens, i, c.StreetType, len(street), (inUrb && len(urb) > 1) || seenUnit):
			closeLoose(false)
			canon := n.lex.streetType[k]
			if tk != canon {
				flags.add("ABBREVIATION_EXPANDED")
			}
			c.StreetType = canon
			flags.remove("NO_STREET_TYPE")
			inUrb, inStreet = false, true
		case n.isUnit(tk) && n.unitApplies(tokens, i):
			closeLoose(false)
			canon := n.lex.unit[k]
			val := ""
			// "Departamento de C": el conector no es el valor.
			if i+2 < len(tokens) && (tokens[i+1] == "DE" || tokens[i+1] == "DEL") && reValue.MatchString(tokens[i+2]) {
				i++
			}
			if i+1 < len(tokens) && !n.isUnit(tokens[i+1]) && !n.isUrb(tokens[i+1]) {
				i++
				val = tokens[i]
				// "Lote 02 C": la letra suelta al final o antes de otra unidad es del lote.
				if canon == "LT" && i+1 < len(tokens) && len(tokens[i+1]) == 1 && tokens[i+1][0] >= 'A' && tokens[i+1][0] <= 'Z' &&
					(i+2 == len(tokens) || n.isUnit(tokens[i+2]) || n.isUrb(tokens[i+2])) {
					i++
					val += tokens[i]
				}
				// "Lote 12, 12": el valor repetido se descarta.
				if i+1 < len(tokens) && tokens[i+1] == val {
					i++
					flags.add("REPEATED_VALUE")
				}
			} else {
				flags.add("MISSING_UNIT_VALUE")
			}
			switch canon {
			case "MZ":
				c.Block = val
				seenBlockLot = true
			case "LT":
				c.Lot = val
				seenBlockLot = true
			default:
				interior = append(interior, strings.TrimSpace(canon+" "+val))
			}
			seenUnit = true
		case n.isUrb(tk):
			closeLoose(true)
			canon := n.lex.urb[k]
			if tk != canon {
				flags.add("ABBREVIATION_EXPANDED")
			}
			urb = append(urb, canon)
			inUrb, inStreet = true, false
		case inStreet:
			street = append(street, tk)
		case inUrb && len(urb) > 1 && !seenUnit && len(tk) >= 3 && reNumber.MatchString(tk) &&
			(c.StreetType != "" || len(street) > 0) && !anyDigit(street):
			// "Jr. X zona B, 455", "Av. X Urb. Las Lomas 1210": el número que sigue al
			// nombre de la urbanización es la puerta de la vía. Se exigen 3 dígitos
			// para no tomar la etapa ("Urb. Maranga 4").
			street = append(street, tk)
			flags.add("NUMBER_AFTER_URBANIZATION")
		case inUrb && len(urb) > 1 && i == len(tokens)-1 && reCode.MatchString(tk) && anyDigit(street):
			// "Jr. X 120 Condominio Las Palmas B-204": código de unidad al final.
			interior = append(interior, tk)
			flags.add("INTERIOR_CODE_AFTER_URBANIZATION")
		case inUrb:
			next := ""
			if i+1 < len(tokens) {
				next = tokens[i+1]
			}
			urb = append(urb, n.urbToken(tk, next))
		case len(loose) > 0 || (seenBlockLot && len(urb) == 0 && !reNumber.MatchString(tk)):
			next := ""
			if i+1 < len(tokens) {
				next = tokens[i+1]
			}
			loose = append(loose, n.urbToken(tk, next))
		case seenUnit:
			trailing = append(trailing, tk)
		default:
			street = append(street, tk)
		}
		i++
	}
	closeLoose(false)

	n.splitStreet(street, res, flags)
	if len(trailing) > 0 {
		flags.add("TRAILING_TEXT_MOVED_TO_REFERENCE")
		c.Reference = strings.TrimSpace(c.Reference + " " + strings.Join(trailing, " "))
	}
	if interior = dedupeUnits(interior); len(interior) > 0 {
		c.Interior = strings.TrimSpace(c.Interior + " " + strings.Join(interior, " "))
	}
	if cut, ok := n.cutDistrictFromUrb(urb, res.Location.Ubigeo); ok {
		urb = cut
		flags.add("DISTRICT_REMOVED_FROM_URBANIZATION")
	}
	urb = append(urb, pendingEtapa...)
	c.Urbanization = strings.Join(urb, " ")
	if len(urb) == 1 && n.isUrb(urb[0]) { // marcador sin nombre
		flags.add("EMPTY_URBANIZATION_NAME")
	}
}

// dedupeUnits quita unidades repetidas ("DPTO 603 DPTO 603") y las que quedaron
// sin valor cuando la misma unidad aparece con valor ("DPTO 603 APT").
func dedupeUnits(units []string) []string {
	withValue := map[string]bool{}
	for _, u := range units {
		if f := strings.Fields(u); len(f) > 1 {
			withValue[f[0]] = true
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, u := range units {
		if seen[u] || (len(strings.Fields(u)) == 1 && withValue[u]) {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

// cutDistrictFromUrb corta el nombre de la urbanización donde aparece el distrito de
// la ubicación ("URBANIZACION LOS ROSALES COMAS" en Comas). No corta si el distrito
// sigue a un conector ("Los Prados de San Miguel") ni deja la urbanización sin nombre.
func (n *Normalizer) cutDistrictFromUrb(urb []string, ubigeo string) ([]string, bool) {
	e := n.cat.ByCode(ubigeo)
	if e == nil || len(urb) < 3 {
		return urb, false
	}
	names := [][]string{strings.Fields(e.DistrictKey)}
	for _, a := range e.Aliases {
		names = append(names, strings.Fields(txt.Key(a)))
	}
	keys := make([]string, len(urb))
	for i, w := range urb {
		keys[i] = txt.Key(w)
	}
	for j := 2; j < len(keys); j++ {
		if nameConnectors[keys[j-1]] {
			continue
		}
		for _, nm := range names {
			if j+len(nm) <= len(keys) && strings.Join(keys[j:j+len(nm)], " ") == strings.Join(nm, " ") {
				return urb[:j], true
			}
		}
	}
	return urb, false
}

// startsLateStreet indica si tokens[i] abre una vía que aparece después de la
// urbanización o de Mz/Lt ("Urb. Los Rosales Mz B Lt 12 Calle 5"). Solo aplica si
// aún no hay vía, si antes hubo un nombre de urbanización o una unidad, y si al tipo
// de vía le sigue algo: así "Urb. Villa Parque" no se corta. La "C" suelta se excluye
// por ambigua.
func (n *Normalizer) startsLateStreet(tokens []string, i int, streetType string, streetLen int, afterBlock bool) bool {
	if !afterBlock || streetType != "" || streetLen > 0 || i+1 >= len(tokens) || tokens[i] == "C" {
		return false
	}
	_, ok := n.lex.streetType[txt.NoEnye(tokens[i])]
	return ok
}

// groupingWords son las palabras ante las que un ordinal escrito pasa a dígito.
var groupingWords = map[string]bool{"ETAPA": true, "SECTOR": true, "ZONA": true, "GRUPO": true}

// urbToken normaliza un token del nombre de la urbanización. Un ordinal escrito
// pasa a dígito solo antes de una palabra de agrupación ("Primera Etapa" -> "1
// ETAPA"); en un nombre propio se conserva ("Primero de Setiembre", "Quinta Heren").
func (n *Normalizer) urbToken(tk, next string) string {
	k := txt.NoEnye(tk)
	if w, ok := n.lex.ordinals[k]; ok && groupingWords[next] {
		return w
	}
	if m := reOrdinal.FindStringSubmatch(tk); m != nil {
		return m[1]
	}
	return tk
}

// expandTitles expande Sta./Gral./etc. dentro de un nombre (nunca en el último token).
func (n *Normalizer) expandTitles(tokens []string, flags *flagSet) []string {
	out := make([]string, len(tokens))
	for i, t := range tokens {
		out[i] = t
		if i < len(tokens)-1 {
			if full, ok := n.lex.titles[txt.NoEnye(t)]; ok {
				out[i] = full
				flags.add("ABBREVIATION_EXPANDED")
			}
		}
	}
	return out
}

// splitStreet separa nombre de vía y número de puerta.
//
// Reglas: el último token numérico es el número de puerta solo si hay un nombre
// antes de él; un número al inicio (Calle 5) o seguido de "DE" (2 de Mayo)
// forma parte del nombre.
func (n *Normalizer) splitStreet(street []string, res *Result, flags *flagSet) {
	c := &res.Components
	var name, trailing []string

	if sn := indexOf(street, "SINNUMERO"); sn >= 0 {
		c.Number = "S/N"
		flags.add("NO_NUMBER")
		name, trailing = street[:sn], street[sn+1:]
	} else {
		last := -1
		for idx := len(street) - 1; idx >= 0; idx-- {
			if reNumber.MatchString(street[idx]) && !(idx+1 < len(street) && street[idx+1] == "DE") {
				last = idx
				break
			}
		}
		if last > 1 && street[last-1] == street[last] && !anyDigit(street[:last-1]) {
			c.Number = street[last]
			flags.add("REPEATED_NUMBER")
			name, trailing = street[:last-1], street[last+1:]
		} else if last > 1 && reNumber.MatchString(street[last-1]) && !anyDigit(street[:last-1]) {
			// "Los Laureles 610 204": el primer número es la puerta y el
			// segundo, el interior. Solo si el nombre no tiene dígitos, para no
			// confundir "Bloc 5 502" o "Calle 5 245".
			c.Number = street[last-1]
			c.Interior = street[last]
			flags.add("INTERIOR_FROM_BARE_NUMBER")
			name, trailing = street[:last-1], street[last+1:]
		} else if last > 0 {
			c.Number = street[last]
			name, trailing = street[:last], street[last+1:]
			if len(trailing) == 1 && len(trailing[0]) == 1 && trailing[0][0] >= 'A' && trailing[0][0] <= 'Z' {
				c.Number += trailing[0]
				trailing = nil
			} else if len(trailing) == 1 && reCode.MatchString(trailing[0]) {
				// "120 B204": código de unidad sin marcador justo después de la puerta.
				c.Interior = trailing[0]
				flags.add("INTERIOR_FROM_BARE_NUMBER")
				trailing = nil
			}
		} else {
			name = street
		}
	}
	if len(trailing) > 0 {
		flags.add("TRAILING_TEXT_MOVED_TO_REFERENCE")
		c.Reference = strings.Join(trailing, " ")
	}
	c.StreetName = strings.Join(n.expandTitles(name, flags), " ")
	if c.StreetType != "" && c.StreetName == "" {
		flags.add("NO_STREET_NAME")
	}
}

func anyDigit(tokens []string) bool {
	for _, t := range tokens {
		if strings.ContainsAny(t, "0123456789") {
			return true
		}
	}
	return false
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// finish arma la cadena normalizada y las claves de comparación.
func (n *Normalizer) finish(res *Result, flags *flagSet) {
	c := res.Components
	if c.Number == "" && c.Block == "" && c.Lot == "" {
		flags.add("NO_NUMBER")
	}
	var parts []string
	add := func(s string) {
		if s != "" {
			parts = append(parts, s)
		}
	}
	add(c.StreetType)
	add(c.StreetName)
	add(c.Number)
	if c.Block != "" {
		add("MZ " + c.Block)
	}
	if c.Lot != "" {
		add("LT " + c.Lot)
	}
	add(c.Interior)
	add(c.Urbanization)
	res.Normalized = strings.Join(parts, " ")

	key := func(f func(string) string) string {
		out := []string{c.StreetType, f(c.StreetName), c.Number}
		if c.Block != "" {
			out = append(out, "MZ:"+c.Block)
		}
		if c.Lot != "" {
			out = append(out, "LT:"+c.Lot)
		}
		if c.Urbanization != "" {
			out = append(out, "URB:"+f(c.Urbanization))
		}
		var nz []string
		for _, s := range out {
			if s != "" {
				nz = append(nz, s)
			}
		}
		return strings.Join(nz, "|")
	}
	res.MatchKey = key(txt.Key)
	res.PhoneticKey = key(txt.Phonetic)
}
