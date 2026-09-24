package normalizer_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"addrsvc/internal/catalog"
	"addrsvc/internal/normalizer"
)

type testCase struct {
	Name         string             `json:"name"`
	Input        normalizer.Request `json:"input"`
	Expect       map[string]string  `json:"expect"`
	FlagsContain []string           `json:"flags_contain"`
	FlagsAbsent  []string           `json:"flags_absent"`
}

func newNormalizer(t testing.TB) *normalizer.Normalizer {
	t.Helper()
	root := filepath.Join("..", "..")
	cat, err := catalog.Load(filepath.Join(root, "data", "catalog", "ubigeos.json"))
	if err != nil {
		t.Fatal(err)
	}
	lex, err := normalizer.LoadLexicon(filepath.Join(root, "data", "rules", "lexicon.json"))
	if err != nil {
		t.Fatal(err)
	}
	return normalizer.New(cat, lex, normalizer.Options{ActiveZones: map[string]bool{"LIMA_METRO": true}})
}

func field(r normalizer.Result, key string) (string, bool) {
	switch key {
	case "street_type":
		return r.Components.StreetType, true
	case "street_name":
		return r.Components.StreetName, true
	case "number":
		return r.Components.Number, true
	case "interior":
		return r.Components.Interior, true
	case "block":
		return r.Components.Block, true
	case "lot":
		return r.Components.Lot, true
	case "urbanization":
		return r.Components.Urbanization, true
	case "reference":
		return r.Components.Reference, true
	case "normalized":
		return r.Normalized, true
	case "ubigeo":
		return r.Location.Ubigeo, true
	case "district":
		return r.Location.District, true
	case "province":
		return r.Location.Province, true
	case "zone":
		return r.Location.Zone, true
	case "location_source":
		return r.Location.Source, true
	case "in_scope":
		return strconv.FormatBool(r.Location.InScope), true
	}
	return "", false
}

func has(flags []string, f string) bool {
	for _, x := range flags {
		if x == f {
			return true
		}
	}
	return false
}

// Los casos viven en testdata/normalize_cases.json, un formato independiente del
// lenguaje: si algún día el normalizador se porta a otro lenguaje, se reutilizan.
func TestGoldenCases(t *testing.T) {
	n := newNormalizer(t)
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "normalize_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []testCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	for _, tc := range file.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			r := n.Normalize(tc.Input)
			for key, want := range tc.Expect {
				got, ok := field(r, key)
				if !ok {
					t.Fatalf("campo desconocido en el caso: %s", key)
				}
				if got != want {
					t.Errorf("%s: obtuvo %q, esperaba %q (flags=%v)", key, got, want, r.Flags)
				}
			}
			for _, f := range tc.FlagsContain {
				if !has(r.Flags, f) {
					t.Errorf("falta el flag %s (flags=%v)", f, r.Flags)
				}
			}
			for _, f := range tc.FlagsAbsent {
				if has(r.Flags, f) {
					t.Errorf("no debía tener el flag %s (flags=%v)", f, r.Flags)
				}
			}
		})
	}
}

// La grafía distinta (Sauces / Saucez) no se corrige en la normalización, pero
// la clave fonética permite al matching relacionarlas con menor confianza.
func TestPhoneticKeyRelatesTypos(t *testing.T) {
	n := newNormalizer(t)
	a := n.Normalize(normalizer.Request{Address: "C. Los Sauces 245 Ate"})
	b := n.Normalize(normalizer.Request{Address: "Calle Los Saucez 245 - Ate"})
	if a.MatchKey == b.MatchKey {
		t.Errorf("match_key no debería corregir la grafía: %q", a.MatchKey)
	}
	if a.PhoneticKey != b.PhoneticKey {
		t.Errorf("phonetic_key debería coincidir: %q vs %q", a.PhoneticKey, b.PhoneticKey)
	}
}

// normalize(normalize(x)) == normalize(x)
func TestIdempotence(t *testing.T) {
	n := newNormalizer(t)
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "normalize_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []testCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	for _, tc := range file.Cases {
		first := n.Normalize(tc.Input)
		if first.Normalized == "" {
			continue
		}
		second := n.Normalize(normalizer.Request{Address: first.Normalized, Ubigeo: first.Location.Ubigeo})
		if first.Normalized != second.Normalized {
			t.Errorf("%s: no es idempotente: %q -> %q", tc.Name, first.Normalized, second.Normalized)
		}
	}
}

func BenchmarkNormalize(b *testing.B) {
	n := newNormalizer(b)
	req := normalizer.Request{Address: "Clle. Los Sáuces #245, Ate, Lima, Lima"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = n.Normalize(req)
	}
}
