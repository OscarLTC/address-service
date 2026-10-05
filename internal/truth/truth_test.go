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
			{Day: "2026-09-08", Point: near, Arrival: &near},
		}, Gold, "entregas_coinciden_en_dias_distintos"},
		{"un_solo_dia_es_plata", []Delivery{
			{Day: "2026-09-01", Point: base, Arrival: &base},
		}, Silver, "una_sola_fecha"},
		{"entrega_lejos_de_la_llegada_no_cuenta", []Delivery{
			{Day: "2026-09-01", Point: base, Arrival: &far},
		}, "", "sin_entregas_validas:lejos_de_la_llegada"},
		{"fuera_del_distrito_no_cuenta", []Delivery{
			{Day: "2026-09-01", Point: geo.Point{-77.10, -12.08}, Arrival: pt(-77.10, -12.08)},
		}, "", "sin_entregas_validas:fuera_del_distrito"},
		{"entregas_que_se_contradicen_se_descartan", []Delivery{
			{Day: "2026-09-01", Point: base, Arrival: &base},
			{Day: "2026-09-08", Point: far, Arrival: &far},
		}, "", "entregas_contradictorias"},
		{"sin_llegada_no_cuenta", []Delivery{
			{Day: "2026-09-01", Point: base},
		}, "", "sin_entregas_validas:sin_llegada"},
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
