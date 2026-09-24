package txt

import "testing"

func TestFold(t *testing.T) {
	if got := Fold("Sáuces Ñandú", true); got != "SAUCES ÑANDU" {
		t.Errorf("got %q", got)
	}
	if got := Fold("Ñandú", false); got != "NANDU" {
		t.Errorf("got %q", got)
	}
}

func TestKey(t *testing.T) {
	if got := Key("  San  Martín-de Porres. "); got != "SAN MARTIN DE PORRES" {
		t.Errorf("got %q", got)
	}
}

func TestPhonetic(t *testing.T) {
	if Phonetic("Sauces") != Phonetic("Saucez") {
		t.Error("Sauces y Saucez deberían coincidir")
	}
	if Phonetic("Los Olivos") == Phonetic("Los Olivares") {
		t.Error("Olivos y Olivares no deben coincidir")
	}
}
