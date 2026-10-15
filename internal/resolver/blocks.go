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
	// maxSureSequence es a cuántas cuadras de una con dato la centena deducida en
	// secuencia todavía ubica el número a nivel de cuadra. Más lejos, un cruce de más
	// o de menos en OSM corre la numeración: reservando un ancla, a 2 cuadras solo el
	// 45 % queda a 50 m o menos, y a 3 o más, menos del 35 %.
	maxSureSequence = 1
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
	// inBlock son las anclas de cada cuadra cuya centena es la de la cuadra, como
	// puntos de control (número/100, progresiva).
	inBlock [][]control
	// gap es a cuántas cuadras está cada una de la cuadra con dato más cercana.
	gap []int
}

// control es un punto de control de la numeración: x es el número dividido por
// 100 y c, su progresiva sobre el eje.
type control struct{ x, c float64 }

// splitByMarks parte una cuadra en la que caen marcas de operadores con centenas
// distintas: OSM no tiene ese cruce. El corte va entre las dos marcas, que en
// general se ponen a ambos lados de la esquina.
func splitByMarks(a *axis, bounds []float64, marks []snapshot.BlockMark) []float64 {
	type mk struct {
		c float64
		h int
	}
	ms := make([]mk, len(marks))
	for i, x := range marks {
		ms[i] = mk{a.chainage(x.Point), x.Hundred}
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].c < ms[j].c })
	blockOf := func(c float64) int { return sort.SearchFloat64s(bounds, c) }
	var extra []float64
	for i := 1; i < len(ms); i++ {
		if p, q := ms[i-1], ms[i]; p.h != q.h && blockOf(p.c) == blockOf(q.c) {
			extra = append(extra, (p.c+q.c)/2)
		}
	}
	if len(extra) == 0 {
		return bounds
	}
	out := append(append([]float64{}, bounds...), extra...)
	sort.Float64s(out)
	return out
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
	bounds = splitByMarks(a, bounds, s.Marks)
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
	m.gap = make([]int, n)
	for j := range m.gap {
		m.gap[j] = n
		for _, v := range voted {
			m.gap[j] = min(m.gap[j], max(j-v, v-j))
		}
	}
	m.inBlock = make([][]control, n)
	for _, an := range s.Anchors {
		if j := blockOf(an.Point); m.source[j] != "" && an.Number/100 == m.hund[j] {
			m.inBlock[j] = append(m.inBlock[j], control{float64(an.Number) / 100, a.chainage(an.Point)})
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

// locate ubica un número y dice si la ubicación es segura a nivel de cuadra: lo es
// dentro de una cuadra con dato o a maxSureSequence cuadras de una; no lo es entre
// cuadras. Ubica dentro de su cuadra, interpolando entre los extremos de la
// cuadra y las anclas que tenga (de cualquier lado: dicen dónde cae cada número
// mejor que la proporción de sus dos últimas cifras) o, si su centena no cuadra con
// ninguna, interpolando entre las cuadras que lo encierran.
func (m *blockModel) locate(num int) (c float64, sure, ok bool) {
	target := num / 100
	if m.dir == 0 {
		// Sin sentido conocido solo se puede decir en qué cuadra está.
		for j := range m.hund {
			if m.source[j] != "" && m.hund[j] == target {
				return (m.bounds[j] + m.bounds[j+1]) / 2, true, true
			}
		}
		return 0, false, false
	}
	// Cada cuadra numerada da dos puntos de control: su centena en un extremo y la
	// siguiente en el otro.
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
			return interpolateControls(append([]control{{float64(target), from}, {float64(target + 1), to}}, m.inBlock[j]...), float64(num)/100), m.gap[j] <= maxSureSequence, true
		}
		cps = append(cps, control{float64(m.hund[j]), from}, control{float64(m.hund[j] + 1), to})
	}
	sort.Slice(cps, func(i, j int) bool { return cps[i].x < cps[j].x })
	x := float64(num) / 100
	for i := 1; i < len(cps); i++ {
		if lo, hi := cps[i-1], cps[i]; lo.x <= x && x <= hi.x && hi.x > lo.x {
			return lo.c + (x-lo.x)/(hi.x-lo.x)*(hi.c-lo.c), false, true
		}
	}
	return 0, false, false
}

// interpolateControls interpola linealmente la progresiva de x entre los puntos de
// control que lo encierran. Los dos primeros son los extremos de la cuadra y siempre
// lo encierran.
func interpolateControls(cps []control, x float64) float64 {
	sort.SliceStable(cps, func(i, j int) bool { return cps[i].x < cps[j].x })
	for i := 1; i < len(cps); i++ {
		if lo, hi := cps[i-1], cps[i]; lo.x <= x && x <= hi.x {
			if hi.x == lo.x {
				return (lo.c + hi.c) / 2
			}
			return lo.c + (x-lo.x)/(hi.x-lo.x)*(hi.c-lo.c)
		}
	}
	return cps[0].c
}

// locateByBlocks ubica un número con el modelo de cuadras, del lado de la calle que
// le toca por su paridad, y dice si la ubicación es segura a nivel de cuadra.
func locateByBlocks(s *snapshot.Street, num int) (p geo.Point, sure, ok bool) {
	m := newBlockModel(s)
	if m == nil {
		return geo.Point{}, false, false
	}
	c, sure, ok := m.locate(num)
	if !ok {
		return geo.Point{}, false, false
	}
	return m.axis.toSide(m.axis.at(c), s.Anchors, num), sure, true
}
