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
const Version = "0.4.0"

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
	rePJ       = regexp.MustCompile(`\bP\.\s?J\b\.?`)
	reNumLetHy = regexp.MustCompile(`(\d)-([A-Z])\b`)
	rePunct    = regexp.MustCompile(`[^A-Z0-9Ñ\s]`)
	reGlueMz   = regexp.MustCompile(`^MZ([A-Z])$`)
	reGlue     = regexp.MustCompile(`^(LT|INT|DPTO|OF|PISO|CALLE|CL|CLL|AV|JR|PSJE|PJE)(\d+[A-Z]?)$`)
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

	tokens, refs := n.tokenize(req.Address)
	if len(tokens) == 0 {
		flags.add("EMPTY_ADDRESS")
	}

	var tail []string
	tokens, tail = n.cutReference(tokens)
	refs = append(refs, tail...)

	suffix, rest := n.scanSuffix(tokens)
	sres := n.resolveNames(suffix, areaContext{province: txt.Key(req.Province), department: txt.Key(req.Department)})
	switch {
	case len(suffix) == 0:
	case len(rest) == 0 || (len(rest) == 1 && n.isStructural(rest[0])):
		flags.add("LOCATION_SUFFIX_SKIPPED")
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
		res.Components.Reference = strings.Join(refs, " ")
	}
	n.finish(&res, flags)
	res.Flags = flags.list
	if res.Flags == nil {
		res.Flags = []string{}
	}
	return res
}

// tokenize aplica las capas N0-N2: limpieza, tokenización y separación de pegados.
func (n *Normalizer) tokenize(raw string) (tokens, refs []string) {
	s := txt.Fold(txt.FixMojibake(raw), true)
	for _, m := range reParens.FindAllStringSubmatch(s, -1) {
		if r := txt.Collapse(rePunct.ReplaceAllString(m[1], " ")); r != "" {
			refs = append(refs, r)
		}
	}
	s = reParens.ReplaceAllString(s, " ")
	s = reSN.ReplaceAllString(s, " SINNUMERO ")
	s = reNroMark.ReplaceAllString(s, " NRO ")
	s = reAAHH.ReplaceAllString(s, " AAHH ")
	s = rePJ.ReplaceAllString(s, " PJ ")
	s = reNumLetHy.ReplaceAllString(s, "$1$2")
	s = rePunct.ReplaceAllString(s, " ")
	s = n.lex.applyPhrases(txt.Collapse(s))

	for _, t := range strings.Fields(s) {
		switch {
		case t == "NRO":
			continue
		case reGlueMz.MatchString(t):
			m := reGlueMz.FindStringSubmatch(t)
			tokens = append(tokens, "MZ", m[1])
		case reGlue.MatchString(t):
			m := reGlue.FindStringSubmatch(t)
			tokens = append(tokens, m[1], m[2])
		default:
			tokens = append(tokens, t)
		}
	}
	return tokens, refs
}

// cutReference separa lo que viene después de "frente a", "cerca de", etc.
func (n *Normalizer) cutReference(tokens []string) (addr, ref []string) {
	for i := 2; i < len(tokens); i++ {
		if n.lex.refMarkers[txt.NoEnye(tokens[i])] {
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

	var street, urb, trailing []string
	var interior []string
	inUrb, seenUnit, inStreet := false, false, false
	for i < len(tokens) {
		tk := tokens[i]
		k := txt.NoEnye(tk)
		switch {
		case n.startsLateStreet(tokens, i, c.StreetType, len(street), (inUrb && len(urb) > 1) || seenUnit):
			canon := n.lex.streetType[k]
			if tk != canon {
				flags.add("ABBREVIATION_EXPANDED")
			}
			c.StreetType = canon
			flags.remove("NO_STREET_TYPE")
			inUrb, inStreet = false, true
		case n.isUnit(tk):
			canon := n.lex.unit[k]
			val := ""
			if i+1 < len(tokens) && !n.isUnit(tokens[i+1]) && !n.isUrb(tokens[i+1]) {
				i++
				val = tokens[i]
			} else {
				flags.add("MISSING_UNIT_VALUE")
			}
			switch canon {
			case "MZ":
				c.Block = val
			case "LT":
				c.Lot = val
			default:
				interior = append(interior, strings.TrimSpace(canon+" "+val))
			}
			seenUnit = true
		case n.isUrb(tk):
			canon := n.lex.urb[k]
			if tk != canon {
				flags.add("ABBREVIATION_EXPANDED")
			}
			urb = append(urb, canon)
			inUrb, inStreet = true, false
		case inStreet:
			street = append(street, tk)
		case inUrb:
			urb = append(urb, n.urbToken(tk))
		case seenUnit:
			trailing = append(trailing, tk)
		default:
			street = append(street, tk)
		}
		i++
	}

	n.splitStreet(street, res, flags)
	if len(trailing) > 0 {
		flags.add("TRAILING_TEXT_MOVED_TO_REFERENCE")
		c.Reference = strings.Join(trailing, " ")
	}
	c.Interior = strings.Join(interior, " ")
	c.Urbanization = strings.Join(urb, " ")
	if len(urb) == 1 { // marcador sin nombre
		flags.add("EMPTY_URBANIZATION_NAME")
	}
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

func (n *Normalizer) urbToken(tk string) string {
	k := txt.NoEnye(tk)
	if w, ok := n.lex.ordinals[k]; ok {
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
		if last > 0 {
			c.Number = street[last]
			name, trailing = street[:last], street[last+1:]
			if len(trailing) == 1 && len(trailing[0]) == 1 && trailing[0][0] >= 'A' && trailing[0][0] <= 'Z' {
				c.Number += trailing[0]
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
