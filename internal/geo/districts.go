package geo

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// District es el límite de un distrito identificado por su ubigeo.
type District struct {
	Ubigeo string    `json:"ubigeo"`
	Name   string    `json:"name"`
	OSMID  int64     `json:"osm_relation"`
	Rings  [][]Point `json:"rings"`

	area *Area
}

// DistrictsFile es el formato de data/geo/districts.json.
type DistrictsFile struct {
	Meta      map[string]any `json:"_meta"`
	Districts []*District    `json:"districts"`
}

// Districts indexa los límites distritales para ubicar puntos.
type Districts struct {
	list   []*District
	byCode map[string]*District
}

// LoadDistricts lee data/geo/districts.json.
func LoadDistricts(path string) (*Districts, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f DistrictsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("límites distritales inválidos: %w", err)
	}
	return NewDistricts(f.Districts)
}

// NewDistricts construye el índice a partir de una lista de distritos.
func NewDistricts(list []*District) (*Districts, error) {
	d := &Districts{byCode: map[string]*District{}}
	for _, x := range list {
		if len(x.Ubigeo) != 6 || len(x.Rings) == 0 {
			return nil, fmt.Errorf("distrito inválido: %s %q", x.Ubigeo, x.Name)
		}
		if _, dup := d.byCode[x.Ubigeo]; dup {
			return nil, fmt.Errorf("ubigeo duplicado: %s", x.Ubigeo)
		}
		rings := make([]Ring, len(x.Rings))
		for i, r := range x.Rings {
			rings[i] = r
		}
		x.area = NewArea(rings)
		d.byCode[x.Ubigeo] = x
		d.list = append(d.list, x)
	}
	return d, nil
}

// Locate devuelve los ubigeos cuyo límite contiene el punto. Normalmente hay uno;
// cero si el punto cae fuera y más de uno si los límites se superponen.
func (d *Districts) Locate(p Point) []string {
	var out []string
	for _, x := range d.list {
		if x.area.Contains(p) {
			out = append(out, x.Ubigeo)
		}
	}
	return out
}

// ByCode devuelve el distrito o nil.
func (d *Districts) ByCode(code string) *District { return d.byCode[code] }

// Len es la cantidad de distritos.
func (d *Districts) Len() int { return len(d.list) }

// Centroids devuelve un punto representativo por distrito: el centroide del anillo
// de mayor área. Sirve como ubicación de último recurso (precisión DISTRICT).
func (d *Districts) Centroids() map[string]Point {
	out := map[string]Point{}
	for _, x := range d.list {
		var best Ring
		bestArea := -1.0
		for _, r := range x.area.Rings {
			if a := math.Abs(signedArea(r)); a > bestArea {
				best, bestArea = r, a
			}
		}
		out[x.Ubigeo] = ringCentroid(best)
	}
	return out
}

func signedArea(r Ring) float64 {
	s := 0.0
	for i := 0; i+1 < len(r); i++ {
		s += r[i][0]*r[i+1][1] - r[i+1][0]*r[i][1]
	}
	return s / 2
}

func ringCentroid(r Ring) Point {
	a := signedArea(r)
	if a == 0 {
		return r[0]
	}
	var cx, cy float64
	for i := 0; i+1 < len(r); i++ {
		f := r[i][0]*r[i+1][1] - r[i+1][0]*r[i][1]
		cx += (r[i][0] + r[i+1][0]) * f
		cy += (r[i][1] + r[i+1][1]) * f
	}
	return Point{cx / (6 * a), cy / (6 * a)}
}
