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
	byPhon  map[string][]*snapshot.Street
	byToken map[string][]*snapshot.Street
	areas   map[string]*snapshot.Area
}

// Resolver es seguro para uso concurrente: no muta estado después de New.
type Resolver struct {
	norm      *normalizer.Normalizer
	snap      *snapshot.Snapshot
	districts map[string]*districtIndex
	centroids map[string]geo.Point
}

// New indexa el snapshot. centroids da un punto representativo por distrito.
func New(norm *normalizer.Normalizer, snap *snapshot.Snapshot, centroids map[string]geo.Point) *Resolver {
	r := &Resolver{norm: norm, snap: snap, districts: map[string]*districtIndex{}, centroids: centroids}
	idx := func(u string) *districtIndex {
		d := r.districts[u]
		if d == nil {
			d = &districtIndex{byKey: map[string][]*snapshot.Street{}, byPhon: map[string][]*snapshot.Street{},
				byToken: map[string][]*snapshot.Street{}, areas: map[string]*snapshot.Area{}}
			r.districts[u] = d
		}
		return d
	}
	for i := range snap.Streets {
		s := &snap.Streets[i]
		d := idx(s.Ubigeo)
		d.byKey[s.Key] = append(d.byKey[s.Key], s)
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

	if c.StreetName != "" && d != nil {
		cands := r.candidates(d, c.StreetType, c.StreetName)
		if len(cands) > 0 {
			best, loc, ambiguous, how := pick(cands, c.Number)
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
	for _, s := range d.byPhon[phon] {
		add(s, 0.93, "PHONETIC")
	}
	if len(best) == 0 {
		seen := map[*snapshot.Street]bool{}
		for _, t := range strings.Fields(key) {
			for _, s := range d.byToken[t] {
				if seen[s] {
					continue
				}
				seen[s] = true
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

// location es la ubicación de un número sobre una calle.
type location struct {
	point     geo.Point
	precision string
	// tight indica que la ubicación es confiable para aceptar sin revisión: un
	// ancla exacta sin duplicados lejanos, o una interpolación entre anclas cercanas
	// del mismo lado.
	tight bool
}

// maxTightGap es la mayor distancia en números entre las anclas de una interpolación
// confiable (en Lima, ~100 números por cuadra).
const maxTightGap = 40

// pick elige la calle entre los mejores candidatos. Si los mejores son componentes
// homónimas del mismo distrito, desempata la única que tiene anclas para el número.
func pick(cands []candidate, number string) (candidate, location, bool, string) {
	best := cands[0]
	var top []candidate
	for _, c := range cands {
		if c.score >= best.score-0.05 {
			top = append(top, c)
		}
	}
	loc := locateOnStreet(best.street, number)
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
		locs[i] = locateOnStreet(c.street, number)
		if locs[i].precision == AddressPoint || locs[i].precision == SegmentInterpolated {
			covered = append(covered, i)
		}
	}
	if len(covered) == 1 {
		return top[covered[0]], locs[covered[0]], false, "STREET_BY_NUMBER_COVERAGE"
	}
	return best, loc, true, ""
}

// locateOnStreet ubica el número sobre la calle: ancla exacta, interpolación entre
// las dos anclas más cercanas (prefiriendo la misma paridad, que en Lima es el mismo
// lado) o, sin anclas útiles, un punto sobre la calle.
func locateOnStreet(s *snapshot.Street, number string) location {
	num, err := strconv.Atoi(strings.TrimRightFunc(number, func(r rune) bool { return r < '0' || r > '9' }))
	if err == nil && len(s.Anchors) > 0 {
		if l, ok := interpolate(s.Anchors, num, true); ok {
			return l
		}
		if l, ok := interpolate(s.Anchors, num, false); ok {
			l.tight = false // del otro lado de la calle
			return l
		}
	}
	return location{point: streetPoint(s), precision: Street, tight: streetSpan(s) <= maxStreetSpan}
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
		return location{exact[0].Point, AddressPoint, tight}, true
	}
	switch {
	case lo != nil && hi != nil && hi.Number-lo.Number <= 400 && dist(lo.Point, hi.Point) <= 1500:
		t := float64(num-lo.Number) / float64(hi.Number-lo.Number)
		p := geo.Point{lo.Point[0] + t*(hi.Point[0]-lo.Point[0]), lo.Point[1] + t*(hi.Point[1]-lo.Point[1])}
		tight := hi.Number-lo.Number <= maxTightGap && dist(lo.Point, hi.Point) <= 250
		return location{p, SegmentInterpolated, tight}, true
	case lo != nil && num-lo.Number <= 30:
		return location{lo.Point, SegmentInterpolated, false}, true
	case hi != nil && hi.Number-num <= 30:
		return location{hi.Point, SegmentInterpolated, false}, true
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
	case c.match == "EXACT" && c.score >= 0.99 && loc.tight && loc.precision != Street:
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
