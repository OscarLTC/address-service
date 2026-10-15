package truth

import (
	"testing"

	"addrsvc/internal/geo"
)

// fakeDistricts ubica todo punto con longitud mayor a -77.05 en el distrito "150116".
type fakeDistricts struct{}

func (fakeDistricts) Locate(p geo.Point) []string {
	if p[0] > -77.05 {
		return []string{"150116"}
	}
	return nil
}

func pt(lng, lat float64) *geo.Point { p := geo.Point{lng, lat}; return &p }

func TestResolve(t *testing.T) {
	base := geo.Point{-77.0300, -12.0800}
	near := geo.Point{-77.03005, -12.08005} // ~8 m
	far := geo.Point{-77.0290, -12.0800}    // ~109 m
	cases := []struct {
		name    string
		ds      []Delivery
		quality string
		reason  string
	}{
		{"dos_dias_coinciden_es_oro", []Delivery{
			{Day: "2026-09-01", Point: base, Arrival: pt(-77.0300, -12.0801)},
			{Day: "2026-09-08", Point: near, Arrival: &base},
		}, Gold, "entregas_coinciden_en_dias_distintos"},
		{"un_solo_dia_es_plata", []Delivery{
			{Day: "2026-09-01", Point: base, Arrival: &near},
		}, Silver, "una_sola_fecha"},
		{"entrega_lejos_de_la_llegada_no_cuenta", []Delivery{
			{Day: "2026-09-01", Point: base, Arrival: &far},
		}, "", "sin_entregas_validas:lejos_de_la_llegada"},
		{"fuera_del_distrito_no_cuenta", []Delivery{
			{Day: "2026-09-01", Point: geo.Point{-77.10, -12.08}, Arrival: pt(-77.10005, -12.08)},
		}, "", "sin_entregas_validas:fuera_del_distrito"},
		{"entregas_que_se_contradicen_se_descartan", []Delivery{
			{Day: "2026-09-01", Point: base, Arrival: &near},
			{Day: "2026-09-08", Point: far, Arrival: pt(-77.02905, -12.08005)},
		}, "", "entregas_contradictorias"},
		{"sin_llegada_no_cuenta", []Delivery{
			{Day: "2026-09-01", Point: base},
		}, "", "sin_entregas_validas:sin_llegada"},
		{"llegada_identica_no_cuenta", []Delivery{
			{Day: "2026-09-01", Point: base, Arrival: pt(-77.0300, -12.0800)},
		}, "", "sin_entregas_validas:llegada_identica"},
		{"marcada_en_lote_no_cuenta", []Delivery{
			{Day: "2026-09-01", Point: base, Arrival: &near, Batch: 3},
		}, "", "sin_entregas_validas:marcada_en_lote"},
		{"lote_chico_cuenta", []Delivery{
			{Day: "2026-09-01", Point: base, Arrival: &near, Batch: 2},
		}, Silver, "una_sola_fecha"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Resolve(Address{Ubigeo: "150116", Deliveries: c.ds}, fakeDistricts{})
			if r.Quality != c.quality || r.Reason != c.reason {
				t.Errorf("calidad %q motivo %q, se esperaba %q %q", r.Quality, r.Reason, c.quality, c.reason)
			}
		})
	}
}

func TestBatchCounts(t *testing.T) {
	a, b, c := geo.Point{-77.0300, -12.0800}, geo.Point{-77.03005, -12.08005}, geo.Point{-77.0200, -12.0800}
	ds := []Delivery{
		{Day: "2026-09-01", Point: a}, // con b (otra dirección, mismo día, ~8 m)
		{Day: "2026-09-01", Point: b},
		{Day: "2026-09-01", Point: a}, // misma dirección que la primera: no cuenta entre ellas
		{Day: "2026-09-02", Point: a}, // otro día
		{Day: "2026-09-01", Point: c}, // lejos
	}
	got := BatchCounts(ds, []string{"x", "y", "x", "z", "w"})
	want := []int{1, 2, 1, 0, 0}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("BatchCounts = %v, se esperaba %v", got, want)
		}
	}
}
