package geo

import (
	"os"
	"path/filepath"
	"testing"
)

func square(x0, y0, x1, y1 float64) Ring {
	return Ring{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}, {x0, y0}}
}

func TestAreaContainsWithHole(t *testing.T) {
	a := NewArea([]Ring{square(0, 0, 10, 10), square(4, 4, 6, 6)})
	cases := []struct {
		p    Point
		want bool
	}{
		{Point{1, 1}, true},
		{Point{5, 5}, false}, // dentro del hueco
		{Point{11, 5}, false},
		{Point{-1, -1}, false},
	}
	for _, c := range cases {
		if got := a.Contains(c.p); got != c.want {
			t.Errorf("Contains(%v) = %v, se esperaba %v", c.p, got, c.want)
		}
	}
}

func TestAssembleRingsOutOfOrderAndReversed(t *testing.T) {
	lines := [][]Point{
		{{10, 10}, {0, 10}},
		{{0, 0}, {10, 0}},
		{{0, 0}, {0, 10}}, // en sentido contrario al recorrido
		{{10, 0}, {10, 10}},
	}
	rings, err := AssembleRings(lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(rings) != 1 || len(rings[0]) != 5 {
		t.Fatalf("se esperaba un anillo de 5 puntos, se obtuvo %v", rings)
	}
	if !NewArea(rings).Contains(Point{5, 5}) {
		t.Error("el anillo armado no contiene su centro")
	}
}

func TestAssembleRingsOpen(t *testing.T) {
	if _, err := AssembleRings([][]Point{{{0, 0}, {1, 0}}, {{1, 0}, {1, 1}}}); err != ErrOpenRing {
		t.Fatalf("se esperaba ErrOpenRing, se obtuvo %v", err)
	}
}

func TestSimplifyKeepsShape(t *testing.T) {
	r := Ring{{0, 0}, {5, 0.0001}, {10, 0}, {10, 10}, {0, 10}, {0, 0}}
	s := Simplify(r, 0.001)
	if len(s) != 5 || s[0] != s[len(s)-1] {
		t.Fatalf("simplificación inesperada: %v", s)
	}
}

// TestDistrictsFile valida el archivo versionado de límites distritales.
func TestDistrictsFile(t *testing.T) {
	path := filepath.Join("..", "..", "data", "geo", "districts.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("data/geo/districts.json no existe; se genera con make golden")
	}
	d, err := LoadDistricts(path)
	if err != nil {
		t.Fatal(err)
	}
	if d.Len() != 50 {
		t.Errorf("se esperaban 50 distritos (43 de Lima y 7 del Callao), hay %d", d.Len())
	}
	cases := map[string]Point{
		"150122": {-77.0300, -12.1219}, // Parque Kennedy, Miraflores
		"150101": {-77.0300, -12.0464}, // Plaza Mayor, Cercado de Lima
		"070101": {-77.1500, -12.0560}, // Plaza Grau, Callao
		"150103": {-76.9200, -12.0300}, // Ate
	}
	for code, p := range cases {
		got := d.Locate(p)
		if len(got) != 1 || got[0] != code {
			t.Errorf("Locate(%v) = %v, se esperaba %s", p, got, code)
		}
	}
}

func TestDistances(t *testing.T) {
	a, b := Point{-77.03, -12.12}, Point{-77.03, -12.121}
	if d := DistanceM(a, b); d < 110 || d > 112 {
		t.Errorf("DistanceM = %.1f, se esperaba ~111 m", d)
	}
	line := []Point{{-77.031, -12.12}, {-77.029, -12.12}}
	if d := LineDistanceM(b, line); d < 110 || d > 112 {
		t.Errorf("LineDistanceM = %.1f, se esperaba ~111 m", d)
	}
}
