package golden

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	in := []Row{{ID: "X-1", Source: "synthetic", RawAddress: `Av. "Los Sauces", 245`, ExpectedUbigeo: "150103", Split: "dev"}}
	var buf bytes.Buffer
	if err := Write(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0] != in[0] {
		t.Fatalf("ida y vuelta distinta: %+v", out)
	}
}

func TestReadRejectsWrongHeader(t *testing.T) {
	bad := strings.Replace(strings.Join(Header, ","), "raw_address", "address", 1) + "\n"
	if _, err := Read(strings.NewReader(bad)); err == nil {
		t.Fatal("se esperaba un error por la cabecera")
	}
}

// Los CSV versionados deben respetar el formato de la plantilla.
func TestVersionedFiles(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "goldenset", "*.csv"))
	if len(files) == 0 {
		t.Skip("no hay CSV en goldenset/")
	}
	for _, f := range files {
		rows, err := ReadFile(f)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		seen := map[string]bool{}
		for _, r := range rows {
			if seen[r.ID] {
				t.Errorf("%s: id repetido %s", f, r.ID)
			}
			seen[r.ID] = true
			if r.Split != "dev" && r.Split != "test" {
				t.Errorf("%s: %s tiene split %q", f, r.ID, r.Split)
			}
		}
	}
}
