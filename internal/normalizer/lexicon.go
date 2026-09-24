package normalizer

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"

	"addrsvc/internal/txt"
)

type lexiconFile struct {
	Version          string              `json:"version"`
	StreetTypes      map[string][]string `json:"street_types"`
	UnitMarkers      map[string][]string `json:"unit_markers"`
	UrbMarkers       map[string][]string `json:"urbanization_markers"`
	Phrases          map[string]string   `json:"phrases"`
	Titles           map[string]string   `json:"titles"`
	ReferenceMarkers []string            `json:"reference_markers"`
	OrdinalWords     map[string]string   `json:"ordinal_words"`
}

type phrase struct {
	re *regexp.Regexp
	to string
}

// Lexicon contiene los diccionarios de reglas (datos, no código).
type Lexicon struct {
	Version    string
	streetType map[string]string
	unit       map[string]string
	urb        map[string]string
	titles     map[string]string
	refMarkers map[string]bool
	ordinals   map[string]string
	phrases    []phrase
}

// LoadLexicon lee el léxico desde un archivo JSON.
func LoadLexicon(path string) (*Lexicon, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseLexicon(data)
}

// ParseLexicon construye el léxico desde JSON.
func ParseLexicon(data []byte) (*Lexicon, error) {
	var f lexiconFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("léxico inválido: %w", err)
	}
	lx := &Lexicon{
		Version:    f.Version,
		streetType: invert(f.StreetTypes),
		unit:       invert(f.UnitMarkers),
		urb:        invert(f.UrbMarkers),
		titles:     map[string]string{},
		refMarkers: map[string]bool{},
		ordinals:   map[string]string{},
	}
	for k, v := range f.Titles {
		lx.titles[txt.Key(k)] = v
	}
	for _, k := range f.ReferenceMarkers {
		lx.refMarkers[txt.Key(k)] = true
	}
	for k, v := range f.OrdinalWords {
		lx.ordinals[txt.Key(k)] = v
	}
	for from, to := range f.Phrases {
		lx.phrases = append(lx.phrases, phrase{
			re: regexp.MustCompile(`\b` + regexp.QuoteMeta(txt.Key(from)) + `\b`),
			to: to,
		})
	}
	// Las frases más largas primero para evitar reemplazos parciales.
	sort.Slice(lx.phrases, func(i, j int) bool {
		return len(lx.phrases[i].re.String()) > len(lx.phrases[j].re.String())
	})
	return lx, nil
}

func invert(m map[string][]string) map[string]string {
	out := map[string]string{}
	for canon, variants := range m {
		out[txt.Key(canon)] = canon
		for _, v := range variants {
			out[txt.Key(v)] = canon
		}
	}
	return out
}

func (lx *Lexicon) applyPhrases(s string) string {
	for _, p := range lx.phrases {
		s = p.re.ReplaceAllString(s, p.to)
	}
	return s
}
