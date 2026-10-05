package resolver_test

import (
	"path/filepath"
	"testing"

	"addrsvc/internal/catalog"
	"addrsvc/internal/geo"
	"addrsvc/internal/normalizer"
	"addrsvc/internal/resolver"
	"addrsvc/internal/snapshot"
	"addrsvc/internal/txt"
)

// Snapshot mínimo e inventado en Lince (150116): una calle con anclas, una calle
// larga sin anclas y dos calles homónimas separadas.
func street(id int, ubigeo, typ, name string, lines [][]geo.Point, anchors ...snapshot.Anchor) snapshot.Street {
	return snapshot.Street{ID: id, Ubigeo: ubigeo, Type: typ, Name: name, Key: txt.Key(name),
		Phonetic: txt.Phonetic(name), Lines: lines, Anchors: anchors}
}

func a(n int, lng, lat float64) snapshot.Anchor {
	return snapshot.Anchor{Number: n, Point: geo.Point{lng, lat}}
}

func newResolver(t *testing.T) *resolver.Resolver {
	t.Helper()
	root := filepath.Join("..", "..", "data")
	cat, err := catalog.Load(filepath.Join(root, "catalog", "ubigeos.json"))
	if err != nil {
		t.Fatal(err)
	}
	lex, err := normalizer.LoadLexicon(filepath.Join(root, "rules", "lexicon.json"))
	if err != nil {
		t.Fatal(err)
	}
	norm := normalizer.New(cat, lex, normalizer.Options{ActiveZones: map[string]bool{"LIMA_METRO": true}})
	snap := &snapshot.Snapshot{Format: snapshot.FormatVersion, Version: "test", Streets: []snapshot.Street{
		street(1, "150116", "AVENIDA", "LAS PALMERAS", [][]geo.Point{{{-77.0400, -12.0800}, {-77.0390, -12.0800}}},
			a(100, -77.0400, -12.0800), a(120, -77.0398, -12.0800), a(140, -77.0396, -12.0800)),
		street(2, "150116", "AVENIDA", "LOS CEREZOS", [][]geo.Point{{{-77.0500, -12.0700}, {-77.0300, -12.0700}}}),
		street(3, "150116", "CALLE", "LOS NARANJOS", [][]geo.Point{{{-77.0450, -12.0850}, {-77.0445, -12.0850}}},
			a(200, -77.0450, -12.0850), a(220, -77.0448, -12.0850)),
		street(4, "150116", "CALLE", "LOS NARANJOS", [][]geo.Point{{{-77.0350, -12.0750}, {-77.0345, -12.0750}}},
			a(800, -77.0350, -12.0750), a(820, -77.0348, -12.0750)),
	}}
	snap.SortAnchors()
	return resolver.New(norm, snap, map[string]geo.Point{"150116": {-77.036, -12.083}})
}

func TestGeocode(t *testing.T) {
	r := newResolver(t)
	cases := []struct {
		name, address, district      string
		decision, precision, flagHas string
		streetID                     int
	}{
		{"ancla_exacta", "Av. Las Palmeras 120", "Lince", resolver.AutoAccept, resolver.AddressPoint, "", 1},
		{"interpolacion_entre_anclas_cercanas", "Av. Las Palmeras 130", "Lince", resolver.AutoAccept, resolver.SegmentInterpolated, "", 1},
		{"calle_larga_sin_anclas_va_a_revision", "Av. Los Cerezos 500", "Lince", resolver.Review, resolver.Street, "LONG_STREET_WITHOUT_ANCHOR", 2},
		{"homonimas_desempatadas_por_numero", "Calle Los Naranjos 810", "Lince", resolver.AutoAccept, resolver.SegmentInterpolated, "STREET_BY_NUMBER_COVERAGE", 4},
		{"homonimas_sin_numero_van_a_revision", "Calle Los Naranjos", "Lince", resolver.Review, resolver.Street, "AMBIGUOUS_STREET", 0},
		{"grafia_distinta_no_es_auto_accept", "Av. Las Palmeraz 120", "Lince", resolver.AcceptFlagged, resolver.AddressPoint, "", 1},
		{"calle_inexistente_va_al_centroide", "Jr. Inventado 10", "Lince", resolver.Review, resolver.District, "STREET_NOT_FOUND", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.Geocode(normalizer.Request{Address: c.address, District: c.district})
			if got.Decision != c.decision || got.PrecisionLevel != c.precision {
				t.Errorf("decisión %s / precisión %s, se esperaba %s / %s (flags %v)", got.Decision, got.PrecisionLevel, c.decision, c.precision, got.Flags)
			}
			if c.streetID != 0 && got.StreetID != c.streetID {
				t.Errorf("calle %d, se esperaba %d", got.StreetID, c.streetID)
			}
			if c.flagHas != "" && !contains(got.Flags, c.flagHas) {
				t.Errorf("falta el flag %s (flags %v)", c.flagHas, got.Flags)
			}
		})
	}
}

func TestGeocodeOutOfScope(t *testing.T) {
	got := newResolver(t).Geocode(normalizer.Request{Address: "Av. Los Olivos 100", District: "Bellavista", Province: "Callao"})
	if got.Status != "OUT_OF_SCOPE" || got.Location != nil {
		t.Errorf("se esperaba OUT_OF_SCOPE sin coordenada, se obtuvo %s %v", got.Status, got.Location)
	}
}

func TestInterpolationIsBetweenAnchors(t *testing.T) {
	got := newResolver(t).Geocode(normalizer.Request{Address: "Av. Las Palmeras 130", District: "Lince"})
	if got.Location == nil || got.Location.Lng <= -77.0398 || got.Location.Lng >= -77.0396 {
		t.Errorf("el 130 debe quedar entre el 120 y el 140: %+v", got.Location)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// BenchmarkGeocodeRealSnapshot mide la latencia con el snapshot completo de Lima.
func BenchmarkGeocodeRealSnapshot(b *testing.B) {
	path := filepath.Join("..", "..", "data", "snapshot", "lima.snap")
	snap, err := snapshot.Load(path)
	if err != nil {
		b.Skip("no hay snapshot: go run ./cmd/snapshotbuild")
	}
	root := filepath.Join("..", "..", "data")
	cat, _ := catalog.Load(filepath.Join(root, "catalog", "ubigeos.json"))
	lex, _ := normalizer.LoadLexicon(filepath.Join(root, "rules", "lexicon.json"))
	districts, _ := geo.LoadDistricts(filepath.Join(root, "geo", "districts.json"))
	r := resolver.New(normalizer.New(cat, lex, normalizer.Options{ActiveZones: map[string]bool{"LIMA_METRO": true}}), snap, districts.Centroids())
	reqs := []normalizer.Request{
		{Address: "Av. Arequipa 2450", District: "Lince"},
		{Address: "Jr. Los Pinos 512 dpto 104", District: "San Borja"},
		{Address: "Av. Javier Prado Este 4200", District: "Santiago de Surco"},
		{Address: "Mz C Lt 14 Urb. Los Jardines", District: "Comas"},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.Geocode(reqs[i%len(reqs)])
	}
}

// Una dirección verificada (pin de operador) se responde con su punto, sin buscar.
func TestVerifiedAddressWins(t *testing.T) {
	root := filepath.Join("..", "..", "data")
	cat, _ := catalog.Load(filepath.Join(root, "catalog", "ubigeos.json"))
	lex, _ := normalizer.LoadLexicon(filepath.Join(root, "rules", "lexicon.json"))
	norm := normalizer.New(cat, lex, normalizer.Options{ActiveZones: map[string]bool{"LIMA_METRO": true}})
	n := norm.Normalize(normalizer.Request{Address: "Mz C Lt 14 Urb. Los Jardines Inventados", District: "Comas"})
	snap := &snapshot.Snapshot{Format: snapshot.FormatVersion, Version: "test", Verified: []snapshot.Verified{{
		KeyHash: snapshot.AddressKeyHash(n.MatchKey, n.Location.Ubigeo), Ubigeo: n.Location.Ubigeo,
		Point: geo.Point{-77.05, -11.94}, Method: "pin_operador",
	}}}
	r := resolver.New(norm, snap, nil)
	got := r.Geocode(normalizer.Request{Address: "MZ. C LT. 14 URB LOS JARDINES INVENTADOS", District: "Comas"})
	if got.ResolutionType != "VERIFIED" || got.Decision != resolver.AutoAccept || got.Location == nil || got.Location.Lng != -77.05 {
		t.Errorf("se esperaba la dirección verificada: %+v", got)
	}
}
