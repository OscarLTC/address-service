// Package snapshot define el índice inmutable que el resolver carga en memoria:
// calles por distrito con su geometría, puntos ancla para interpolar números y
// centros de urbanizaciones. Se construye en el plano de control y se publica
// versionado; el resolver nunca consulta la base de datos por request.
package snapshot

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"os"
	"sort"

	"addrsvc/internal/geo"
)

// FormatVersion cambia cuando cambia la estructura serializada.
const FormatVersion = 2

// Anchor es un punto con número de puerta conocido sobre una calle.
type Anchor struct {
	Number int
	Point  geo.Point
	Ref    string // origen, p. ej. "osm:n123"
}

// Street es una calle canónica dentro de un distrito.
type Street struct {
	ID       int
	Ubigeo   string
	Type     string // tipo de vía canónico, "" si el nombre no lo trae
	Name     string // nombre en forma de display
	Key      string // txt.Key(Name)
	Phonetic string // txt.Phonetic(Name)
	Lines    [][]geo.Point
	Anchors  []Anchor // ordenados por número
	Refs     []string // ways de OSM de origen
}

// Area es una urbanización, AA.HH. o asociación con nombre y centro conocido.
type Area struct {
	Ubigeo string
	Name   string // nombre sin marcador, display
	Key    string
	Point  geo.Point
}

// Verified es una dirección con coordenada verificada (pin de operador o GPS de
// entrega validado). KeyHash es sha256(match_key + "|" + ubigeo) en hex, el mismo
// address_hash de canonical_addresses.
type Verified struct {
	KeyHash string
	Ubigeo  string
	Point   geo.Point
	Method  string // pin_operador | gps_entrega
	Ref     string // p. ej. "obs:123"
}

// Snapshot es el contenido completo de una versión.
type Snapshot struct {
	Format   int
	Version  string
	Meta     map[string]string
	Streets  []Street
	Areas    []Area
	Verified []Verified
}

// AddressKeyHash calcula la clave de una dirección verificada.
func AddressKeyHash(matchKey, ubigeo string) string {
	sum := sha256.Sum256([]byte(matchKey + "|" + ubigeo))
	return hex.EncodeToString(sum[:])
}

// SortAnchors deja los anclas de cada calle ordenados por número.
func (s *Snapshot) SortAnchors() {
	for i := range s.Streets {
		a := s.Streets[i].Anchors
		sort.Slice(a, func(x, y int) bool { return a[x].Number < a[y].Number })
	}
}

// Save escribe el snapshot comprimido.
func Save(path string, s *Snapshot) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	if err := gob.NewEncoder(zw).Encode(s); err != nil {
		f.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Load lee un snapshot y valida su formato.
func Load(path string) (*Snapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := gob.NewDecoder(zr).Decode(&s); err != nil {
		return nil, fmt.Errorf("snapshot inválido: %w", err)
	}
	if s.Format != FormatVersion {
		return nil, fmt.Errorf("formato de snapshot %d, se esperaba %d", s.Format, FormatVersion)
	}
	return &s, nil
}
