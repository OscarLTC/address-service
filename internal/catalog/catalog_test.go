package catalog

import (
	"path/filepath"
	"testing"
)

func TestSeedCatalog(t *testing.T) {
	c, err := Load(filepath.Join("..", "..", "data", "catalog", "ubigeos_seed.json"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Size() != 50 {
		t.Errorf("esperaba 50 distritos (43 Lima + 7 Callao), hay %d", c.Size())
	}
	lima, callao := 0, 0
	for _, code := range []string{"150101", "150143", "070101", "070107"} {
		if c.ByCode(code) == nil {
			t.Errorf("falta el ubigeo %s", code)
		}
	}
	for i := 150101; i <= 150143; i++ {
		if e := c.ByCode(itoa(i)); e != nil && e.Zone == "LIMA_METRO" {
			lima++
		}
	}
	for i := 70101; i <= 70107; i++ {
		if e := c.ByCode("0" + itoa(i)); e != nil && e.Zone == "CALLAO" {
			callao++
		}
	}
	if lima != 43 || callao != 7 {
		t.Errorf("zonas incorrectas: lima=%d callao=%d", lima, callao)
	}
	if e := c.ByCode("150103"); e == nil || e.District != "ATE" {
		t.Errorf("150103 debería ser ATE")
	}
}

func TestOfficialCatalog(t *testing.T) {
	c, err := Load(filepath.Join("..", "..", "data", "catalog", "ubigeos.json"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Size() != 1891 {
		t.Errorf("esperaba 1891 distritos (INEI 2022), hay %d", c.Size())
	}
	seed, err := Load(filepath.Join("..", "..", "data", "catalog", "ubigeos_seed.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Cada distrito de la semilla conserva código, nombre, zona y alias.
	for code := 0; code <= 999999; code++ {
		s := seed.ByCode(pad6(code))
		if s == nil {
			continue
		}
		e := c.ByCode(s.Code)
		if e == nil || e.DistrictKey != s.DistrictKey || e.Zone != s.Zone || len(e.Aliases) < len(s.Aliases) || len(e.WeakAliases) < len(s.WeakAliases) {
			t.Errorf("%s no coincide con la semilla: %+v", s.Code, e)
		}
	}
	if e := c.ByCode("100106"); e == nil || e.District != "QUISQUI" || len(e.Aliases) != 1 {
		t.Errorf("100106 debería ser QUISQUI con alias KICHKI: %+v", e)
	}
	if e := c.ByCode("030602"); e == nil || e.District != "ANCO HUALLO" {
		t.Errorf("030602 debería ser ANCO HUALLO: %+v", e)
	}
	if !c.ProvinceInDepartment("TRUJILLO", "LA LIBERTAD") || c.ProvinceInDepartment("AREQUIPA", "LIMA") {
		t.Error("ProvinceInDepartment no respeta la jerarquía")
	}
}

func pad6(n int) string {
	s := itoa(n)
	for len(s) < 6 {
		s = "0" + s
	}
	return s
}

func itoa(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(digits[n%10]) + s
		n /= 10
	}
	return s
}
