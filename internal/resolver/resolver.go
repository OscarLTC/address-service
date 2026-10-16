// Package resolver resuelve una dirección normalizada a una coordenada con su nivel
// de precisión y una decisión por riesgo. Trabaja solo en memoria, sobre un snapshot
// inmutable: no hace llamadas de red ni a la base de datos por request.
//
// v0: el puntaje de confianza no está calibrado; sirve para ordenar candidatos y
// decidir, no como probabilidad (docs/plan.md, Apéndice C).
package resolver

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"addrsvc/internal/geo"
	"addrsvc/internal/normalizer"
	"addrsvc/internal/snapshot"
	"addrsvc/internal/txt"
)

// Niveles de precisión, de más fino a más grueso.
const (
	AddressPoint        = "ADDRESS_POINT"
	SegmentInterpolated = "SEGMENT_INTERPOLATED"
	Street              = "STREET"
	Zone                = "ZONE"
	District            = "DISTRICT"
)

// Decisiones internas (Apéndice C).
const (
	AutoAccept    = "AUTO_ACCEPT"
	AcceptFlagged = "ACCEPT_FLAGGED"
	Review        = "REVIEW"
	Reject        = "REJECT"
)

// Result es la respuesta del resolver.
type Result struct {
	Status         string            `json:"status"` // RESOLVED | REVIEW_REQUIRED | UNRESOLVED | OUT_OF_SCOPE
	Decision       string            `json:"decision"`
	Ubigeo         string            `json:"ubigeo,omitempty"`
	StreetID       int               `json:"street_id,omitempty"`
	Canonical      string            `json:"canonical_address,omitempty"`
	Location       *Point            `json:"location,omitempty"`
	PrecisionLevel string            `json:"precision_level,omitempty"`
	Score          float64           `json:"score"`
	ResolutionType string            `json:"resolution_type,omitempty"`
	Flags          []string          `json:"flags"`
	Candidates     []Candidate       `json:"candidates,omitempty"`
	Normalized     normalizer.Result `json:"normalized"`
	Reason         string            `json:"reason,omitempty"`
}

// Point es una coordenada en la respuesta.
type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Candidate es una calle candidata con su puntaje.
type Candidate struct {
	StreetID int     `json:"street_id"`
	Name     string  `json:"name"`
	Score    float64 `json:"score"`
	Match    string  `json:"match"`
}

type districtIndex struct {
	byKey   map[string][]*snapshot.Street
	byAlias map[string][]*snapshot.Street
	// byOtherName son los nombres de OSM que no son una variante del canónico.
	byOtherName map[string][]*snapshot.Street
	byPhon      map[string][]*snapshot.Street
	byToken     map[string][]*snapshot.Street
	areas       map[string]*snapshot.Area
}

// Resolver es seguro para uso concurrente: no muta estado después de New.
type Resolver struct {
	norm      *normalizer.Normalizer
	snap      *snapshot.Snapshot
	districts map[string]*districtIndex
	centroids map[string]geo.Point
	verified  map[string]*snapshot.Verified
	hints     map[*snapshot.Street][]snapshot.Anchor
	byID      map[int]*snapshot.Street
}

// New indexa el snapshot. centroids da un punto representativo por distrito.
func New(norm *normalizer.Normalizer, snap *snapshot.Snapshot, centroids map[string]geo.Point) *Resolver {
	r := &Resolver{norm: norm, snap: snap, districts: map[string]*districtIndex{}, centroids: centroids, verified: map[string]*snapshot.Verified{}}
	for i := range snap.Verified {
		v := &snap.Verified[i]
		r.verified[v.KeyHash] = v
	}
	idx := func(u string) *districtIndex {
		d := r.districts[u]
		if d == nil {
			d = &districtIndex{byKey: map[string][]*snapshot.Street{}, byAlias: map[string][]*snapshot.Street{}, byOtherName: map[string][]*snapshot.Street{}, byPhon: map[string][]*snapshot.Street{},
				byToken: map[string][]*snapshot.Street{}, areas: map[string]*snapshot.Area{}}
			r.districts[u] = d
		}
		return d
	}
	r.byID = make(map[int]*snapshot.Street, len(snap.Streets))
	for i := range snap.Streets {
		s := &snap.Streets[i]
		r.byID[s.ID] = s
		d := idx(s.Ubigeo)
		d.byKey[s.Key] = append(d.byKey[s.Key], s)
		for _, a := range s.Promoted {
			d.byAlias[a] = append(d.byAlias[a], s)
		}
		for _, a := range s.Aliases {
			if sameName(a, s.Key) {
				d.byAlias[a] = append(d.byAlias[a], s)
			} else {
				d.byOtherName[a] = append(d.byOtherName[a], s)
			}
		}
		d.byPhon[s.Phonetic] = append(d.byPhon[s.Phonetic], s)
		for _, t := range strings.Fields(s.Key) {
			if len(t) >= 3 && !stopword[t] {
				d.byToken[t] = append(d.byToken[t], s)
			}
		}
	}
	for i := range snap.Areas {
		a := &snap.Areas[i]
		idx(a.Ubigeo).areas[a.Key] = a
	}
	r.hints = ParallelHints(snap.Streets)
	return r
}

var stopword = map[string]bool{"LOS": true, "LAS": true, "DEL": true, "SAN": true, "SANTA": true}

// Version identifica el snapshot cargado.
func (r *Resolver) Version() string { return r.snap.Version }

// Geocode normaliza y resuelve una dirección.
func (r *Resolver) Geocode(req normalizer.Request) Result {
	n := r.norm.Normalize(req)
	res := Result{Normalized: n, Ubigeo: n.Location.Ubigeo, Flags: append([]string{}, n.Flags...)}
	c := n.Components

	switch {
	case n.Location.Ubigeo == "":
		res.Status, res.Decision, res.Reason = "REVIEW_REQUIRED", Review, "sin distrito"
		return res
	case !n.Location.InScope:
		res.Status, res.Decision, res.Reason = "OUT_OF_SCOPE", Reject, "fuera de las zonas activas"
		return res
	}
	d := r.districts[n.Location.Ubigeo]

	// Dirección ya verificada por un operador o por GPS de entrega: es la respuesta.
	if v := r.verified[snapshot.AddressKeyHash(n.MatchKey, n.Location.Ubigeo)]; v != nil && n.MatchKey != "" {
		res.Location, res.PrecisionLevel, res.ResolutionType = toPoint(v.Point), AddressPoint, "VERIFIED"
		res.Score, res.Decision, res.Status = 1, AutoAccept, "RESOLVED"
		res.Canonical = n.Normalized
		res.Flags = append(res.Flags, "VERIFIED_"+strings.ToUpper(v.Method))
		return res
	}

	if c.StreetName != "" && d != nil {
		cands := r.candidates(d, c.StreetType, c.StreetName)
		if len(cands) > 0 {
			// La urbanización de la dirección, si está en el índice, ubica la calle.
			var near *geo.Point
			if a := d.areas[areaKey(c.Urbanization)]; a != nil && c.Urbanization != "" {
				near = &a.Point
			}
			best, loc, ambiguous, how := pick(cands, c.Number, near, r.hints)
			res.StreetID, res.Score, res.ResolutionType = best.street.ID, best.score, best.match
			res.Canonical = strings.TrimSpace(best.street.Type + " " + best.street.Name)
			for i, cd := range cands {
				if i == 4 {
					break
				}
				res.Candidates = append(res.Candidates, Candidate{StreetID: cd.street.ID, Name: strings.TrimSpace(cd.street.Type + " " + cd.street.Name), Score: round(cd.score), Match: cd.match})
			}
			res.Location, res.PrecisionLevel = toPoint(loc.point), loc.precision
			if c.Number != "" && c.Number != "S/N" {
				res.Canonical += " " + c.Number
			}
			if how != "" {
				res.Flags = append(res.Flags, how)
			}
			if ambiguous {
				res.Flags = append(res.Flags, "AMBIGUOUS_STREET")
			}
			switch {
			case loc.note != "":
				res.Flags = append(res.Flags, loc.note)
			case !loc.tight && (loc.precision == AddressPoint || loc.precision == SegmentInterpolated):
				res.Flags = append(res.Flags, "WIDE_INTERPOLATION")
			case !loc.tight && loc.precision == Street:
				res.Flags = append(res.Flags, "LONG_STREET_WITHOUT_ANCHOR")
			}
			res.Decision = decide(best, loc, ambiguous, n.Flags)
			res.Status = statusOf(res.Decision)
			return res
		}
		res.Flags = append(res.Flags, "STREET_NOT_FOUND")
	}

	if c.Urbanization != "" && d != nil {
		if a := d.areas[areaKey(c.Urbanization)]; a != nil {
			res.Location, res.PrecisionLevel, res.ResolutionType = toPoint(a.Point), Zone, "AREA_MATCH"
			res.Score = 0.7
			res.Decision = AcceptFlagged
			if c.StreetName != "" { // la calle no se encontró: la zona sola no basta
				res.Decision = Review
			}
			res.Status = statusOf(res.Decision)
			return res
		}
	}

	if p, ok := r.centroids[n.Location.Ubigeo]; ok {
		res.Location, res.PrecisionLevel, res.ResolutionType = toPoint(p), District, "DISTRICT_CENTROID"
	}
	res.Decision, res.Status, res.Reason = Review, "REVIEW_REQUIRED", "sin calle ni zona en el índice"
	return res
}

type candidate struct {
	street *snapshot.Street
	score  float64
	match  string
}

// candidates busca la calle en el distrito: exacto, fonético y aproximado por tokens.
func (r *Resolver) candidates(d *districtIndex, typ, name string) []candidate {
	key, phon := txt.Key(name), txt.Phonetic(name)
	best := map[*snapshot.Street]candidate{}
	add := func(s *snapshot.Street, score float64, match string) {
		if typ != "" && s.Type != "" && s.Type != typ {
			score -= 0.08 // el tipo escrito no coincide (Av. frente a Jr.)
		}
		if cur, ok := best[s]; !ok || score > cur.score {
			best[s] = candidate{s, score, match}
		}
	}
	for _, s := range d.byKey[key] {
		add(s, 1.0, "EXACT")
	}
	// Alias promovido por operadores, o grafía de OSM del mismo nombre: tan
	// confiable como el nombre canónico.
	for _, s := range d.byAlias[key] {
		add(s, 0.995, "ALIAS")
	}
	// Otro nombre de OSM (en general old_name): los nombres viejos se reusan en
	// otras calles del distrito. Contra el GPS de entrega fallaban la mitad de las
	// veces, a cientos de metros o kilómetros: va a revisión.
	for _, s := range d.byOtherName[key] {
		add(s, otherNameScore, "OSM_OTHER_NAME")
	}
	for _, s := range d.byPhon[phon] {
		add(s, 0.93, "PHONETIC")
	}
	if len(best) == 0 {
		words := strings.Fields(key)
		seen := map[*snapshot.Street]bool{}
		for _, t := range words {
			for _, s := range d.byToken[t] {
				if seen[s] {
					continue
				}
				seen[s] = true
				// El nombre de uso común es parte del oficial ("Larco" por "José
				// Larco") o al revés.
				if own := strings.Fields(s.Key); subset(words, own) || subset(own, words) {
					add(s, partialScore, "PARTIAL")
				}
				if sim := similarity(key, s.Key); sim >= 0.82 {
					add(s, 0.9*sim, "FUZZY")
				}
			}
		}
	}
	out := make([]candidate, 0, len(best))
	for _, c := range best {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return len(out[i].street.Anchors) > len(out[j].street.Anchors)
	})
	return out
}

// partialScore es el puntaje de una calle cuyo nombre contiene al buscado o está
// contenido en él: alcanza para aceptar con marca, nunca para AUTO_ACCEPT.
const partialScore = 0.88

// otherNameScore es el puntaje de una calle encontrada por otro nombre de OSM: no
// alcanza para aceptar.
const otherNameScore = 0.75

// sameName indica si dos claves son grafías del mismo nombre: las palabras de una
// están en la otra, iguales o con a lo más una letra de diferencia ("DALIAS" y "LOS
// DALIAS", "ALCAFORES" y "ALCANFORES", "ROCA Y BOLOGNA" y "ROCA Y BOLOÑA").
func sameName(a, b string) bool {
	wa, wb := strings.Fields(txt.NoEnye(a)), strings.Fields(txt.NoEnye(b))
	if len(wa) > len(wb) {
		wa, wb = wb, wa
	}
	if len(wa) == 0 {
		return false
	}
	for _, x := range wa {
		found := false
		for _, y := range wb {
			found = found || x == y || (len(x) >= 4 && oneEdit(x, y))
		}
		if !found {
			return false
		}
	}
	return true
}

// oneEdit indica si a y b difieren en a lo más una letra (cambio, inserción o
// borrado).
func oneEdit(a, b string) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	if len(b)-len(a) > 1 {
		return false
	}
	i := 0
	for i < len(a) && a[i] == b[i] {
		i++
	}
	if len(a) == len(b) {
		return a[i+1:] == b[i+1:]
	}
	return a[i:] == b[i+1:]
}

// subset indica si todas las palabras de a están en b.
func subset(a, b []string) bool {
	for _, x := range a {
		found := false
		for _, y := range b {
			found = found || x == y
		}
		if !found {
			return false
		}
	}
	return len(a) > 0
}

// location es la ubicación de un número sobre una calle.
type location struct {
	point     geo.Point
	precision string
	// tight indica que la ubicación es confiable para aceptar sin revisión: un
	// ancla exacta sin duplicados lejanos, o una interpolación entre anclas cercanas
	// del mismo lado.
	tight bool
	// note es el flag que explica una ubicación estimada sin interpolar.
	note string
}

// maxTightGap es la mayor distancia en números entre las anclas de una interpolación
// confiable (en Lima, ~100 números por cuadra).
const maxTightGap = 40

// pick elige la calle entre los mejores candidatos. Si los mejores son componentes
// homónimas del mismo distrito, desempata la única que tiene anclas para el número
// o, si no, la que queda claramente más cerca de la urbanización (near).
func pick(cands []candidate, number string, near *geo.Point, hints map[*snapshot.Street][]snapshot.Anchor) (candidate, location, bool, string) {
	best := cands[0]
	var top []candidate
	for _, c := range cands {
		if c.score >= best.score-0.05 {
			top = append(top, c)
		}
	}
	loc := locateOnStreet(best.street, number, near, hints[best.street])
	if len(top) == 1 {
		return best, loc, false, ""
	}
	sameName := true
	for _, c := range top {
		sameName = sameName && c.street.Key == best.street.Key
	}
	if !sameName {
		return best, loc, true, ""
	}
	var covered []int
	locs := make([]location, len(top))
	for i, c := range top {
		locs[i] = locateOnStreet(c.street, number, near, hints[c.street])
		if locs[i].precision == AddressPoint || locs[i].precision == SegmentInterpolated {
			covered = append(covered, i)
		}
	}
	if len(covered) == 1 {
		return top[covered[0]], locs[covered[0]], false, "STREET_BY_NUMBER_COVERAGE"
	}
	if near != nil {
		closest, d1, d2 := 0, math.Inf(1), math.Inf(1)
		for i, c := range top {
			switch _, d := closestOnStreet(c.street, *near); {
			case d < d1:
				closest, d1, d2 = i, d, d1
			case d < d2:
				d2 = d
			}
		}
		if d1 <= maxAreaDistance && d2 >= 2*d1+areaMargin {
			return top[closest], locs[closest], false, "STREET_BY_AREA"
		}
	}
	return best, loc, true, ""
}

// maxAreaDistance es la mayor distancia entre una calle y el centro de la
// urbanización para creer que la calle pasa por ella. areaMargin es la ventaja
// mínima sobre la siguiente homónima para desempatar.
const (
	maxAreaDistance = 800.0
	areaMargin      = 200.0
)

// locateOnStreet ubica el número sobre la calle: ancla exacta, interpolación entre
// las dos anclas más cercanas (prefiriendo la misma paridad, que en Lima es el mismo
// lado), modelo de cuadras, extrapolación desde las anclas o, sin anclas útiles, un punto sobre la
// calle: el más cercano a la urbanización (near) si se conoce, o el central.
func locateOnStreet(s *snapshot.Street, number string, near *geo.Point, hints []snapshot.Anchor) location {
	num, err := strconv.Atoi(strings.TrimRightFunc(number, func(r rune) bool { return r < '0' || r > '9' }))
	// Una calle sin números de puerta pero con cuadras marcadas por operadores también
	// se ubica con el modelo de cuadras.
	if err == nil && (len(s.Anchors) > 0 || len(s.Marks) > 0) {
		if l, ok := interpolate(s.Anchors, num, true); ok {
			return l
		}
		// Sin anclas del mismo lado que encierren el número, la cuadra lo ubica mejor
		// que las anclas del otro lado o que extrapolar.
		if p, sure, ok := locateByBlocks(s, num); ok && sure {
			return location{point: p, precision: SegmentInterpolated, note: "BLOCK_RANGE"}
		} else if ok {
			// Cuadra deducida lejos de un dato: la calle es segura, la cuadra no.
			return location{point: p, precision: Street, tight: streetSpan(s) <= maxStreetSpan, note: "BLOCK_SEQUENCE_FAR"}
		}
		if l, ok := interpolate(s.Anchors, num, false); ok {
			l.tight = false // del otro lado de la calle
			return l
		}
	}
	// Sin anclas propias que sirvan, la numeración de las calles paralelas.
	if err == nil && len(hints) > 0 {
		borrowed := *s
		borrowed.Anchors = hints
		if p, _, ok := locateByBlocks(&borrowed, num); ok {
			return location{point: p, precision: Street, tight: streetSpan(s) <= maxStreetSpan, note: "PARALLEL_BLOCKS"}
		}
	}
	if err == nil && len(s.Anchors) > 0 {
		if p, ok := extrapolate(s, num); ok {
			return location{point: p, precision: Street, note: "EXTRAPOLATED_FROM_ANCHORS"}
		}
	}
	if near != nil {
		if p, d := closestOnStreet(s, *near); d <= maxAreaDistance {
			return location{point: p, precision: Street, tight: streetSpan(s) <= maxStreetSpan, note: "STREET_NEAR_AREA"}
		}
	}
	return location{point: streetPoint(s), precision: Street, tight: streetSpan(s) <= maxStreetSpan}
}

// Locate ubica un número sobre una calle y devuelve el punto, el nivel de precisión
// y, si la ubicación es estimada, el flag que dice cómo. Sirve para medir el error
// de ubicación contra anclas reservadas (cmd/anchoreval).
func Locate(s *snapshot.Street, number string, hints []snapshot.Anchor) (geo.Point, string, string) {
	l := locateOnStreet(s, number, nil, hints)
	return l.point, l.precision, l.note
}

// Suggestion es la ubicación estimada de un número sobre una calle.
type Suggestion struct {
	StreetID  int    `json:"street_id"`
	Location  Point  `json:"location"`
	Precision string `json:"precision_level"`
	How       string `json:"how,omitempty"`
	// Pins es cuántos pines de operadores, posteriores al snapshot, se usaron.
	Pins int `json:"pins"`
}

// Suggest ubica un número sobre una calle del snapshot sumando anclas que todavía
// no están en él (pines de operadores guardados después de construirlo). Así el
// admin aprovecha cada pin en las demás direcciones de la calle sin esperar al
// snapshot siguiente. No modifica el snapshot.
func (r *Resolver) Suggest(streetID int, number string, extra []snapshot.Anchor, marks []snapshot.BlockMark) (Suggestion, bool) {
	local, ok := r.withEdits(streetID, extra, marks)
	if !ok {
		return Suggestion{}, false
	}
	l := locateOnStreet(&local, number, nil, r.hints[r.byID[streetID]])
	return Suggestion{StreetID: streetID, Location: *toPoint(l.point), Precision: l.precision, How: l.note, Pins: len(extra)}, true
}

// withEdits copia una calle del snapshot con las ediciones vigentes de la base:
// extra son los números puestos a mano y los pines de operadores, marks las cuadras
// marcadas. La base manda sobre el snapshot: los números puestos a mano y las marcas
// del snapshot se reemplazan por los de la base, así lo que un operador quita deja
// de contar sin esperar al snapshot siguiente. Un ancla nueva reemplaza a la del
// mismo número.
func (r *Resolver) withEdits(streetID int, extra []snapshot.Anchor, marks []snapshot.BlockMark) (snapshot.Street, bool) {
	s := r.byID[streetID]
	if s == nil {
		return snapshot.Street{}, false
	}
	local := *s
	local.Anchors = nil
	replaced := map[int]bool{}
	for _, a := range extra {
		replaced[a.Number] = true
	}
	for _, a := range s.Anchors {
		if !replaced[a.Number] && !strings.HasPrefix(a.Ref, "manual:") {
			local.Anchors = append(local.Anchors, a)
		}
	}
	local.Anchors = append(local.Anchors, extra...)
	sort.Slice(local.Anchors, func(i, j int) bool { return local.Anchors[i].Number < local.Anchors[j].Number })
	local.Marks = append([]snapshot.BlockMark{}, marks...)
	return local, true
}

// StreetInfo identifica una calle del snapshot.
type StreetInfo struct {
	ID      int    `json:"street_id"`
	Name    string `json:"name"`
	Ubigeo  string `json:"ubigeo"`
	Anchors int    `json:"anchors"`
	Marks   int    `json:"marks"`
}

// FindStreets busca calles de un distrito cuyo nombre contiene el texto. Las que
// no tienen números de puerta van primero: son las que más ganan con la edición.
func (r *Resolver) FindStreets(ubigeo, q string, limit int) []StreetInfo {
	key := txt.Key(q)
	var out []StreetInfo
	for i := range r.snap.Streets {
		s := &r.snap.Streets[i]
		if s.Ubigeo != ubigeo || (key != "" && !strings.Contains(s.Key, key)) {
			continue
		}
		out = append(out, StreetInfo{ID: s.ID, Name: strings.TrimSpace(s.Type + " " + s.Name), Ubigeo: s.Ubigeo, Anchors: len(s.Anchors), Marks: len(s.Marks)})
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Anchors == 0) != (out[j].Anchors == 0) {
			return out[i].Anchors == 0
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Estado de una calle según cuántas de sus cuadras tienen centena.
const (
	CoverageFull    = "completa" // todas las cuadras numeradas
	CoveragePartial = "parcial"  // algunas
	CoverageNone    = "sin_datos"
)

// StreetShape es una calle en la vista general de un distrito.
type StreetShape struct {
	StreetInfo
	Coverage string         `json:"coverage"`
	Blocks   int            `json:"blocks"`
	Numbered int            `json:"numbered"`
	Lines    [][][2]float64 `json:"lines"`
	Points   []AnchorInfo   `json:"anchor_points"`
}

// Overview devuelve todas las calles de un distrito con lo que se sabe de cada una:
// sus números de puerta y cuántas de sus cuadras tienen centena. edits suma, por
// calle, las anclas y marcas que todavía no están en el snapshot.
func (r *Resolver) Overview(ubigeo string, edits func(streetID int) ([]snapshot.Anchor, []snapshot.BlockMark)) []StreetShape {
	out := []StreetShape{}
	for i := range r.snap.Streets {
		if r.snap.Streets[i].Ubigeo != ubigeo {
			continue
		}
		var extra []snapshot.Anchor
		var marks []snapshot.BlockMark
		if edits != nil {
			extra, marks = edits(r.snap.Streets[i].ID)
		}
		d, ok := r.Detail(r.snap.Streets[i].ID, extra, marks)
		if !ok {
			continue
		}
		sh := StreetShape{StreetInfo: d.StreetInfo, Lines: d.Lines, Points: d.Anchors, Blocks: len(d.Blocks), Coverage: CoverageNone}
		for _, b := range d.Blocks {
			if b.Hundred != nil {
				sh.Numbered++
			}
		}
		switch {
		case sh.Blocks > 0 && sh.Numbered == sh.Blocks:
			sh.Coverage = CoverageFull
		case sh.Numbered > 0 || len(d.Anchors) > 0:
			sh.Coverage = CoveragePartial
		}
		out = append(out, sh)
	}
	return out
}

// Block es una cuadra de una calle: el tramo entre dos cruces, con su centena si se
// conoce y de dónde sale.
type Block struct {
	From    Point  `json:"from"`
	To      Point  `json:"to"`
	Hundred *int   `json:"hundred"`
	Source  string `json:"source,omitempty"`
}

// AnchorInfo es un número de puerta con ubicación conocida.
type AnchorInfo struct {
	Number   int    `json:"number"`
	Location Point  `json:"location"`
	Ref      string `json:"ref,omitempty"`
}

// StreetDetail es lo que el editor del admin dibuja de una calle.
type StreetDetail struct {
	StreetInfo
	Lines   [][][2]float64 `json:"lines"` // tramos como listas de [lng, lat]
	Blocks  []Block        `json:"blocks"`
	Anchors []AnchorInfo   `json:"anchor_points"`
}

// Detail devuelve la geometría de una calle, sus cuadras numeradas y sus números de
// puerta, sumando las anclas y marcas que todavía no están en el snapshot.
func (r *Resolver) Detail(streetID int, extra []snapshot.Anchor, marks []snapshot.BlockMark) (StreetDetail, bool) {
	s, ok := r.withEdits(streetID, extra, marks)
	if !ok {
		return StreetDetail{}, false
	}
	d := StreetDetail{StreetInfo: StreetInfo{ID: s.ID, Name: strings.TrimSpace(s.Type + " " + s.Name), Ubigeo: s.Ubigeo, Anchors: len(s.Anchors), Marks: len(s.Marks)},
		Blocks: []Block{}, Anchors: []AnchorInfo{}}
	for _, l := range s.Lines {
		line := make([][2]float64, len(l))
		for i, p := range l {
			line[i] = [2]float64{p[0], p[1]}
		}
		d.Lines = append(d.Lines, line)
	}
	for _, a := range s.Anchors {
		d.Anchors = append(d.Anchors, AnchorInfo{Number: a.Number, Location: *toPoint(a.Point), Ref: a.Ref})
	}
	m := newBlockModel(&s)
	if m == nil {
		m = bareBlocks(&s)
	}
	if m == nil {
		return d, true
	}
	for j := range m.hund {
		b := Block{From: *toPoint(m.axis.at(m.bounds[j])), To: *toPoint(m.axis.at(m.bounds[j+1])), Source: m.source[j]}
		if m.source[j] != "" {
			h := m.hund[j]
			b.Hundred = &h
		}
		d.Blocks = append(d.Blocks, b)
	}
	return d, true
}

// bareBlocks arma las cuadras de una calle sin datos, solo para dibujarlas.
func bareBlocks(s *snapshot.Street) *blockModel {
	if len(s.Lines) == 0 {
		return nil
	}
	probe := *s
	probe.Marks = []snapshot.BlockMark{{Hundred: 0, Point: s.Lines[0][0]}}
	m := newBlockModel(&probe)
	if m == nil {
		return nil
	}
	for j := range m.source {
		m.source[j] = ""
	}
	return m
}

// maxStreetSpan es la mayor extensión de una calle cuyo punto medio sirve como
// ubicación aceptable sin número (umbral de error STREET: 150 m a cada lado).
const maxStreetSpan = 300.0

// streetSpan es la mayor distancia entre dos extremos de los tramos de la calle.
func streetSpan(s *snapshot.Street) float64 {
	var ends []geo.Point
	for _, l := range s.Lines {
		ends = append(ends, l[0], l[len(l)-1])
	}
	span := 0.0
	for i := range ends {
		for j := i + 1; j < len(ends); j++ {
			span = max(span, dist(ends[i], ends[j]))
		}
	}
	return span
}

func interpolate(anchors []snapshot.Anchor, num int, sameParity bool) (location, bool) {
	var lo, hi *snapshot.Anchor
	var exact []*snapshot.Anchor
	for i := range anchors {
		a := &anchors[i]
		if sameParity && a.Number%2 != num%2 {
			continue
		}
		if a.Number == num {
			exact = append(exact, a)
		}
		if a.Number < num && (lo == nil || a.Number > lo.Number) {
			lo = a
		}
		if a.Number > num && (hi == nil || a.Number < hi.Number) {
			hi = a
		}
	}
	if len(exact) > 0 {
		tight := true
		for _, a := range exact[1:] {
			tight = tight && dist(a.Point, exact[0].Point) <= 50
		}
		return location{point: exact[0].Point, precision: AddressPoint, tight: tight}, true
	}
	switch {
	case lo != nil && hi != nil && hi.Number-lo.Number <= 400 && dist(lo.Point, hi.Point) <= 1500:
		t := float64(num-lo.Number) / float64(hi.Number-lo.Number)
		p := geo.Point{lo.Point[0] + t*(hi.Point[0]-lo.Point[0]), lo.Point[1] + t*(hi.Point[1]-lo.Point[1])}
		tight := hi.Number-lo.Number <= maxTightGap && dist(lo.Point, hi.Point) <= 250
		return location{point: p, precision: SegmentInterpolated, tight: tight}, true
	case lo != nil && num-lo.Number <= 30:
		return location{point: lo.Point, precision: SegmentInterpolated, tight: false}, true
	case hi != nil && hi.Number-num <= 30:
		return location{point: hi.Point, precision: SegmentInterpolated, tight: false}, true
	}
	return location{}, false
}

// streetPoint devuelve el vértice central del tramo más largo de la calle.
func streetPoint(s *snapshot.Street) geo.Point {
	var best []geo.Point
	bestLen := -1.0
	for _, l := range s.Lines {
		total := 0.0
		for i := 1; i < len(l); i++ {
			total += dist(l[i-1], l[i])
		}
		if total > bestLen {
			best, bestLen = l, total
		}
	}
	return best[len(best)/2]
}

func decide(c candidate, loc location, ambiguous bool, flags []string) string {
	for _, f := range flags {
		if f == "DISTRICT_CONFLICT" || f == "AMBIGUOUS_LIMA" || f == "DUPLICATED_TEXT" {
			return Review
		}
	}
	switch {
	case ambiguous:
		return Review
	case loc.precision == Street && !loc.tight:
		// Sin número ubicable en una calle larga, el punto puede estar a kilómetros.
		return Review
	case (c.match == "EXACT" || c.match == "ALIAS") && c.score >= 0.99 && loc.tight && loc.precision != Street:
		return AutoAccept
	case c.score >= 0.8:
		return AcceptFlagged
	}
	return Review
}

func statusOf(decision string) string {
	switch decision {
	case AutoAccept, AcceptFlagged:
		return "RESOLVED"
	case Review:
		return "REVIEW_REQUIRED"
	}
	return "UNRESOLVED"
}

func areaKey(urb string) string {
	k := txt.Key(urb)
	for _, m := range []string{"ASENTAMIENTO HUMANO", "PUEBLO JOVEN", "ASOCIACION PRO VIVIENDA", "URBANIZACION",
		"ASOCIACION", "COOPERATIVA", "RESIDENCIAL", "CONDOMINIO", "SECTOR", "ZONA", "GRUPO"} {
		if strings.HasPrefix(k, m+" ") {
			return strings.TrimPrefix(k, m+" ")
		}
	}
	return k
}

// similarity es 1 - distancia de Levenshtein normalizada.
func similarity(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 && len(rb) == 0 {
		return 1
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return 1 - float64(prev[len(rb)])/float64(max(len(ra), len(rb)))
}

// dist es la distancia aproximada en metros (equirectangular; suficiente en Lima).
func dist(a, b geo.Point) float64 { return Distance(a, b) }

// Distance devuelve la distancia aproximada en metros entre dos puntos.
func Distance(a, b geo.Point) float64 {
	const r = 6371000.0
	lat := (a[1] + b[1]) / 2 * math.Pi / 180
	dx := (b[0] - a[0]) * math.Pi / 180 * math.Cos(lat)
	dy := (b[1] - a[1]) * math.Pi / 180
	return r * math.Sqrt(dx*dx+dy*dy)
}

func toPoint(p geo.Point) *Point { return &Point{Lat: round6(p[1]), Lng: round6(p[0])} }

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

func round(v float64) float64 { return math.Round(v*1000) / 1000 }
