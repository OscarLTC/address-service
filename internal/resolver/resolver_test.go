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
	}, Areas: []snapshot.Area{
		{Ubigeo: "150116", Name: "Los Jardines Inventados", Key: txt.Key("Los Jardines Inventados"), Point: geo.Point{-77.0348, -12.0752}},
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
		{"homonimas_desempatadas_por_urbanizacion", "Calle Los Naranjos, Urb. Los Jardines Inventados", "Lince", resolver.AcceptFlagged, resolver.Street, "STREET_BY_AREA", 4},
		{"numero_lejos_de_las_anclas_se_extrapola", "Av. Las Palmeras 190", "Lince", resolver.Review, resolver.Street, "EXTRAPOLATED_FROM_ANCHORS", 1},
		{"grafia_distinta_no_es_auto_accept", "Av. Las Palmeraz 120", "Lince", resolver.AcceptFlagged, resolver.AddressPoint, "", 1},
		{"nombre_comun_dentro_del_oficial", "Av. Palmeras 120", "Lince", resolver.AcceptFlagged, resolver.AddressPoint, "", 1},
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

// La extrapolación avanza desde la última ancla en el sentido de la numeración.
func TestExtrapolationFollowsNumbering(t *testing.T) {
	got := newResolver(t).Geocode(normalizer.Request{Address: "Av. Las Palmeras 190", District: "Lince"})
	if got.Location == nil || got.Location.Lng <= -77.0396 || got.Location.Lng > -77.0390 {
		t.Errorf("el 190 debe quedar después del 140, dentro de la calle: %+v", got.Location)
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

// Con los cruces de la calle, un número lejos de las anclas se ubica en su cuadra:
// las anclas fijan las cuadras 1 y 2, y el 450 cae en la cuarta.
func TestBlockRange(t *testing.T) {
	root := filepath.Join("..", "..", "data")
	cat, _ := catalog.Load(filepath.Join(root, "catalog", "ubigeos.json"))
	lex, _ := normalizer.LoadLexicon(filepath.Join(root, "rules", "lexicon.json"))
	norm := normalizer.New(cat, lex, normalizer.Options{ActiveZones: map[string]bool{"LIMA_METRO": true}})
	s := street(1, "150116", "AVENIDA", "LAS PALMERAS", [][]geo.Point{{{-77.0400, -12.0800}, {-77.0340, -12.0800}}},
		a(110, -77.0399, -12.0800), a(150, -77.0395, -12.0800), a(230, -77.0387, -12.0800))
	for _, lng := range []float64{-77.0390, -77.0380, -77.0370, -77.0360, -77.0350} {
		s.Crossings = append(s.Crossings, geo.Point{lng, -12.0800})
	}
	r := resolver.New(norm, &snapshot.Snapshot{Format: snapshot.FormatVersion, Version: "test", Streets: []snapshot.Street{s}}, nil)
	// El 350 está a una cuadra de la última con anclas: se ubica a nivel de cuadra.
	got := r.Geocode(normalizer.Request{Address: "Av. Las Palmeras 350", District: "Lince"})
	if !contains(got.Flags, "BLOCK_RANGE") || got.PrecisionLevel != resolver.SegmentInterpolated || got.Decision != resolver.AcceptFlagged {
		t.Fatalf("se esperaba la ubicación por cuadra: %s %s %v", got.PrecisionLevel, got.Decision, got.Flags)
	}
	if got.Location.Lng <= -77.0380 || got.Location.Lng >= -77.0370 {
		t.Errorf("el 350 debe quedar en la tercera cuadra: %+v", got.Location)
	}
	// El 450, a dos: el punto sale del mismo modelo, pero la cuadra no es segura.
	got = r.Geocode(normalizer.Request{Address: "Av. Las Palmeras 450", District: "Lince"})
	if !contains(got.Flags, "BLOCK_SEQUENCE_FAR") || got.PrecisionLevel != resolver.Street {
		t.Fatalf("se esperaba la cuadra deducida lejos del dato: %s %s %v", got.PrecisionLevel, got.Decision, got.Flags)
	}
	if got.Location.Lng <= -77.0370 || got.Location.Lng >= -77.0360 {
		t.Errorf("el 450 debe quedar en la cuarta cuadra: %+v", got.Location)
	}
}

// Un pin de operador posterior al snapshot mejora la sugerencia de sus vecinos: con
// el 200 fijado, el 190 queda entre el 140 y el 200.
func TestSuggestUsesNewPins(t *testing.T) {
	r := newResolver(t)
	before, ok := r.Suggest(1, "190", nil, nil)
	if !ok || before.Precision != resolver.Street {
		t.Fatalf("sin pines se esperaba una ubicación a nivel de calle: %+v", before)
	}
	got, ok := r.Suggest(1, "190", []snapshot.Anchor{a(200, -77.0392, -12.0800)}, nil)
	if !ok || got.Precision != resolver.SegmentInterpolated || got.Pins != 1 {
		t.Fatalf("con el pin se esperaba una interpolación: %+v", got)
	}
	if got.Location.Lng <= -77.0396 || got.Location.Lng >= -77.0392 {
		t.Errorf("el 190 debe quedar entre el 140 y el pin del 200: %+v", got.Location)
	}
	if _, ok := r.Suggest(999, "190", nil, nil); ok {
		t.Error("una calle que no está en el snapshot no tiene sugerencia")
	}
}

// Un alias promovido por operadores resuelve como el nombre canónico.
func TestPromotedAlias(t *testing.T) {
	root := filepath.Join("..", "..", "data")
	cat, _ := catalog.Load(filepath.Join(root, "catalog", "ubigeos.json"))
	lex, _ := normalizer.LoadLexicon(filepath.Join(root, "rules", "lexicon.json"))
	norm := normalizer.New(cat, lex, normalizer.Options{ActiveZones: map[string]bool{"LIMA_METRO": true}})
	s := street(1, "150116", "AVENIDA", "LAS PALMERAS", [][]geo.Point{{{-77.0400, -12.0800}, {-77.0390, -12.0800}}},
		a(100, -77.0400, -12.0800), a(120, -77.0398, -12.0800))
	s.Promoted = []string{txt.Key("PALMERAS REALES")}
	r := resolver.New(norm, &snapshot.Snapshot{Format: snapshot.FormatVersion, Version: "test", Streets: []snapshot.Street{s}}, nil)
	got := r.Geocode(normalizer.Request{Address: "Av. Palmeras Reales 120", District: "Lince"})
	if got.ResolutionType != "ALIAS" || got.StreetID != 1 || got.Decision != resolver.AutoAccept {
		t.Errorf("se esperaba el alias promovido: %s %d %s", got.ResolutionType, got.StreetID, got.Decision)
	}
}

// Un nombre de OSM que es una grafía del canónico vale como él; uno distinto (un
// old_name que puede ser de otra calle) va a revisión.
func TestOSMAliases(t *testing.T) {
	root := filepath.Join("..", "..", "data")
	cat, _ := catalog.Load(filepath.Join(root, "catalog", "ubigeos.json"))
	lex, _ := normalizer.LoadLexicon(filepath.Join(root, "rules", "lexicon.json"))
	norm := normalizer.New(cat, lex, normalizer.Options{ActiveZones: map[string]bool{"LIMA_METRO": true}})
	s := street(1, "150116", "CALLE", "LOS ALCANFORES", [][]geo.Point{{{-77.0400, -12.0800}, {-77.0390, -12.0800}}},
		a(100, -77.0400, -12.0800), a(120, -77.0398, -12.0800))
	s.Aliases = []string{txt.Key("ALCAFORES"), txt.Key("TUDELA Y VARELA")}
	r := resolver.New(norm, &snapshot.Snapshot{Format: snapshot.FormatVersion, Version: "test", Streets: []snapshot.Street{s}}, nil)
	if got := r.Geocode(normalizer.Request{Address: "Calle Alcafores 120", District: "Lince"}); got.ResolutionType != "ALIAS" || got.Decision != resolver.AutoAccept {
		t.Errorf("la grafía debe valer como el nombre: %s %s", got.ResolutionType, got.Decision)
	}
	if got := r.Geocode(normalizer.Request{Address: "Calle Tudela y Varela 120", District: "Lince"}); got.ResolutionType != "OSM_OTHER_NAME" || got.Decision != resolver.Review {
		t.Errorf("otro nombre debe ir a revisión: %s %s", got.ResolutionType, got.Decision)
	}
}

// Dos marcas con centenas distintas en un mismo tramo entre cruces de OSM señalan
// un cruce que falta: la cuadra se parte entre ellas.
func TestMarksSplitBlock(t *testing.T) {
	// ~600 m de este a oeste; OSM solo tiene cruces a ~200 m y ~400 m.
	s := street(1, "150122", "CALLE", "LAS PRUEBAS", [][]geo.Point{{{-77.0400, -12.0800}, {-77.0345, -12.0800}}})
	s.Crossings = []geo.Point{{-77.03817, -12.0800}, {-77.03633, -12.0800}}
	// La esquina entre la cuadra 1 y la 2 está a ~100 m: una marca a cada lado.
	s.Marks = []snapshot.BlockMark{{Hundred: 1, Point: geo.Point{-77.03912, -12.0800}}, {Hundred: 2, Point: geo.Point{-77.03904, -12.0800}}}
	p, prec, note := resolver.Locate(&s, "250", nil)
	// La cuadra 2 va de ~100 m a ~200 m: el 250 queda a ~150 m del inicio.
	if want := -77.03862; prec != resolver.SegmentInterpolated || p[0] < want-0.00023 || p[0] > want+0.00023 {
		t.Errorf("el 250 debe quedar a la mitad de la cuadra 2: %v %s %s", p, prec, note)
	}
}
