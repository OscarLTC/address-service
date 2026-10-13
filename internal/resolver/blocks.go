package resolver

import (
	"sort"

	"addrsvc/internal/geo"
	"addrsvc/internal/snapshot"
)

// Parámetros del modelo de cuadras.
const (
	// minBlockLength une cruces muy juntos (las dos calzadas de una avenida).
	minBlockLength = 25.0
	// maxBlockSpread es cuántas cuadras se numeran más allá de la última con dato.
	maxBlockSpread = 8
	// markWeight hace que la marca de un operador pese más que cualquier ancla.
	markWeight = 1000
)

// Origen de la centena de una cuadra.
const (
	BlockFromMark     = "marca"     // la fijó un operador
	BlockFromAnchor   = "ancla"     // sale de los números de puerta de la cuadra
	BlockFromSequence = "secuencia" // se dedujo contando cuadras desde una con dato
)

// blockModel es una calle partida en cuadras (los tramos entre cruces) con la
// centena de cada una. En Lima cada cuadra lleva una centena: las anclas y las
// marcas de operadores dicen cuál le toca a su cuadra y en qué sentido crece la
// numeración; las cuadras vecinas sin dato se numeran en secuencia.
type blockModel struct {
	axis   *axis
	bounds []float64 // progresivas de los cruces; la cuadra j va de bounds[j] a bounds[j+1]
	hund   []int
	source []string // "" si la cuadra no tiene centena
	dir    int      // +1 si la numeración crece con la progresiva, -1 si decrece, 0 si no se sabe
}

// newBlockModel arma las cuadras de la calle. Devuelve nil si no hay con qué
// numerarlas: hacen falta cruces y anclas, o al menos una marca de operador.
func newBlockModel(s *snapshot.Street) *blockModel {
	if len(s.Lines) == 0 || (len(s.Marks) == 0 && (len(s.Crossings) == 0 || len(s.Anchors) == 0)) {
		return nil
	}
	a := newAxis(s)
	cuts := make([]float64, len(s.Crossings))
	for i, p := range s.Crossings {
		cuts[i] = a.chainage(p)
	}
	sort.Float64s(cuts)
	bounds := []float64{a.lo}
	for _, c := range cuts {
		if c-bounds[len(bounds)-1] >= minBlockLength {
			bounds = append(bounds, c)
		}
	}
	if a.hi-bounds[len(bounds)-1] >= minBlockLength || len(bounds) == 1 {
		bounds = append(bounds, a.hi)
	} else {
		bounds[len(bounds)-1] = a.hi
	}
	n := len(bounds) - 1
	if n < 1 || bounds[n] <= bounds[0] {
		return nil
	}
	m := &blockModel{axis: a, bounds: bounds, hund: make([]int, n), source: make([]string, n)}
	blockOf := func(p geo.Point) int {
		return min(n-1, max(0, sort.SearchFloat64s(bounds, a.chainage(p))-1))
	}

	// Centena de cada cuadra con dato: la más votada. La marca de un operador manda.
	votes := make([]map[int]int, n)
	vote := func(j, hundred, weight int) {
		if votes[j] == nil {
			votes[j] = map[int]int{}
		}
		votes[j][hundred] += weight
	}
	marked := make([]bool, n)
	for _, mk := range s.Marks {
		j := blockOf(mk.Point)
		vote(j, mk.Hundred, markWeight)
		marked[j] = true
	}
	var sn, sc, snn, snc float64
	for _, an := range s.Anchors {
		vote(blockOf(an.Point), an.Number/100, 1)
		num, c := float64(an.Number), a.chainage(an.Point)
		sn, sc, snn, snc = sn+num, sc+c, snn+num*num, snc+num*c
	}
	var voted []int
	for j, v := range votes {
		best, bestN := 0, 0
		for h, cnt := range v {
			if cnt > bestN || (cnt == bestN && h < best) {
				best, bestN = h, cnt
			}
		}
		if bestN == 0 {
			continue
		}
		m.hund[j], m.source[j] = best, BlockFromAnchor
		if marked[j] {
			m.source[j] = BlockFromMark
		}
		voted = append(voted, j)
	}

	// Sentido: dos cuadras marcadas lo fijan; si no, la pendiente de las anclas.
	var firstMark = -1
	for _, j := range voted {
		if !marked[j] {
			continue
		}
		if firstMark >= 0 && m.hund[j] != m.hund[firstMark] {
			m.dir = 1
			if m.hund[j] < m.hund[firstMark] {
				m.dir = -1
			}
			break
		}
		if firstMark < 0 {
			firstMark = j
		}
	}
	if k := float64(len(s.Anchors)); m.dir == 0 && k*snn-sn*sn > 0 {
		if slope := (k*snc - sn*sc) / (k*snn - sn*sn); slope > 0 {
			m.dir = 1
		} else if slope < 0 {
			m.dir = -1
		}
	}
	if m.dir == 0 {
		return m
	}

	// Entre dos cuadras con dato: en secuencia si las centenas cuadran con los cruces.
	for i := 1; i < len(voted); i++ {
		p, q := voted[i-1], voted[i]
		if m.hund[q]-m.hund[p] == m.dir*(q-p) {
			for j := p + 1; j < q; j++ {
				m.hund[j], m.source[j] = m.hund[p]+m.dir*(j-p), BlockFromSequence
			}
		}
	}
	// Más allá de la primera y la última: en secuencia, unas pocas cuadras.
	first, last := voted[0], voted[len(voted)-1]
	for j := first - 1; j >= 0 && first-j <= maxBlockSpread; j-- {
		if h := m.hund[first] - m.dir*(first-j); h >= 0 {
			m.hund[j], m.source[j] = h, BlockFromSequence
		}
	}
	for j := last + 1; j < n && j-last <= maxBlockSpread; j++ {
		if h := m.hund[last] + m.dir*(j-last); h >= 0 {
			m.hund[j], m.source[j] = h, BlockFromSequence
		}
	}
	return m
}

// locate ubica un número: dentro de su cuadra en proporción a sus dos últimas cifras
// o, si su centena no cuadra con ninguna, interpolando entre las cuadras que lo
// encierran.
func (m *blockModel) locate(num int) (float64, bool) {
	target := num / 100
	if m.dir == 0 {
		// Sin sentido conocido solo se puede decir en qué cuadra está.
		for j := range m.hund {
			if m.source[j] != "" && m.hund[j] == target {
				return (m.bounds[j] + m.bounds[j+1]) / 2, true
			}
		}
		return 0, false
	}
	// Cada cuadra numerada da dos puntos de control: su centena en un extremo y la
	// siguiente en el otro.
	type control struct{ x, c float64 }
	var cps []control
	for j := range m.hund {
		if m.source[j] == "" {
			continue
		}
		from, to := m.bounds[j], m.bounds[j+1]
		if m.dir < 0 {
			from, to = to, from
		}
		if m.hund[j] == target {
			return from + float64(num%100)/100*(to-from), true
		}
		cps = append(cps, control{float64(m.hund[j]), from}, control{float64(m.hund[j] + 1), to})
	}
	sort.Slice(cps, func(i, j int) bool { return cps[i].x < cps[j].x })
	x := float64(num) / 100
	for i := 1; i < len(cps); i++ {
		if lo, hi := cps[i-1], cps[i]; lo.x <= x && x <= hi.x && hi.x > lo.x {
			return lo.c + (x-lo.x)/(hi.x-lo.x)*(hi.c-lo.c), true
		}
	}
	return 0, false
}

// locateByBlocks ubica un número con el modelo de cuadras, del lado de la calle que
// le toca por su paridad.
func locateByBlocks(s *snapshot.Street, num int) (geo.Point, bool) {
	m := newBlockModel(s)
	if m == nil {
		return geo.Point{}, false
	}
	c, ok := m.locate(num)
	if !ok {
		return geo.Point{}, false
	}
	return m.axis.toSide(m.axis.at(c), s.Anchors, num), true
}
