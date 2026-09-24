// Package txt agrupa utilidades de texto sin dependencias externas.
package txt

import (
	"regexp"
	"strings"
	"unicode"
)

var mojibake = strings.NewReplacer(
	"Ã±", "ñ", "Ã‘", "Ñ", "Ã¡", "á", "Ã©", "é", "Ã\u00ad", "í", "Ã³", "ó", "Ãº", "ú",
)

// FixMojibake corrige las secuencias UTF-8 leídas como Latin-1 más comunes.
func FixMojibake(s string) string { return mojibake.Replace(s) }

// Fold pasa a mayúsculas, quita tildes y reemplaza caracteres de control por espacio.
// Si keepEnye es true conserva la Ñ (útil para mostrar); si no, la convierte en N.
func Fold(s string, keepEnye bool) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToUpper(s) {
		switch r {
		case 'Á', 'À', 'Â', 'Ä':
			b.WriteRune('A')
		case 'É', 'È', 'Ê', 'Ë':
			b.WriteRune('E')
		case 'Í', 'Ì', 'Î', 'Ï':
			b.WriteRune('I')
		case 'Ó', 'Ò', 'Ô', 'Ö':
			b.WriteRune('O')
		case 'Ú', 'Ù', 'Û', 'Ü':
			b.WriteRune('U')
		case 'Ñ':
			if keepEnye {
				b.WriteRune('Ñ')
			} else {
				b.WriteRune('N')
			}
		default:
			if unicode.IsControl(r) {
				b.WriteRune(' ')
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

var nonAlnum = regexp.MustCompile(`[^A-Z0-9\s]`)

// Collapse colapsa espacios repetidos.
func Collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// NoEnye convierte Ñ en N.
func NoEnye(s string) string { return strings.ReplaceAll(s, "Ñ", "N") }

// Key devuelve una clave de comparación: mayúsculas, sin tildes ni Ñ,
// sin puntuación y con espacios colapsados.
func Key(s string) string {
	return Collapse(nonAlnum.ReplaceAllString(Fold(s, false), " "))
}

// El orden importa: strings.Replacer prueba los patrones en el orden dado.
var phon = strings.NewReplacer(
	"LL", "Y", "QU", "K", "CE", "SE", "CI", "SI", "CH", "X",
	"GE", "JE", "GI", "JI", "Z", "S", "V", "B", "C", "K", "H", "", "W", "U",
)

// Phonetic devuelve una clave fonética simple para español. Sirve para comparar
// grafías distintas (Sauces / Saucez); nunca para reemplazar el texto original.
func Phonetic(s string) string { return Collapse(phon.Replace(Key(s))) }
