// Package catalog carga el catálogo de ubigeos (departamento, provincia, distrito)
// y ofrece búsquedas por nombre normalizado.
package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"addrsvc/internal/txt"
)

// Entry es un distrito del catálogo.
type Entry struct {
	Code        string   `json:"code"`
	Department  string   `json:"department"`
	Province    string   `json:"province"`
	District    string   `json:"district"`
	Zone        string   `json:"zone"`
	Aliases     []string `json:"aliases"`
	WeakAliases []string `json:"weak_aliases"`

	DistrictKey   string `json:"-"`
	ProvinceKey   string `json:"-"`
	DepartmentKey string `json:"-"`
}

// DistrictHit es un distrito encontrado por nombre. Weak indica un alias ambiguo.
type DistrictHit struct {
	Entry *Entry
	Weak  bool
}

// Lookup resume qué puede ser un nombre: distrito(s), provincia y/o departamento.
type Lookup struct {
	Districts    []DistrictHit
	IsProvince   bool
	IsDepartment bool
}

// Known indica si el nombre existe en el catálogo con algún rol.
func (l Lookup) Known() bool {
	return len(l.Districts) > 0 || l.IsProvince || l.IsDepartment
}

type file struct {
	Meta    map[string]any `json:"_meta"`
	Entries []*Entry       `json:"entries"`
}

// Catalog es el catálogo indexado en memoria.
type Catalog struct {
	byCode      map[string]*Entry
	districts   map[string][]DistrictHit
	provinces   map[string]*Entry
	departments map[string]*Entry
	provDepts   map[string]map[string]bool
	maxWords    int
	size        int
}

// Load lee el catálogo desde un archivo JSON.
func Load(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse construye el catálogo desde JSON.
func Parse(data []byte) (*Catalog, error) {
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("catálogo inválido: %w", err)
	}
	c := &Catalog{
		byCode:      map[string]*Entry{},
		districts:   map[string][]DistrictHit{},
		provinces:   map[string]*Entry{},
		departments: map[string]*Entry{},
		provDepts:   map[string]map[string]bool{},
	}
	for _, e := range f.Entries {
		if len(e.Code) != 6 || e.Department == "" || e.Province == "" || e.District == "" {
			return nil, fmt.Errorf("entrada inválida: %+v", e)
		}
		if _, dup := c.byCode[e.Code]; dup {
			return nil, fmt.Errorf("ubigeo duplicado: %s", e.Code)
		}
		e.DistrictKey = txt.Key(e.District)
		e.ProvinceKey = txt.Key(e.Province)
		e.DepartmentKey = txt.Key(e.Department)
		c.byCode[e.Code] = e
		c.add(e.DistrictKey, e, false)
		for _, a := range e.Aliases {
			c.add(txt.Key(a), e, false)
		}
		for _, a := range e.WeakAliases {
			c.add(txt.Key(a), e, true)
		}
		if _, ok := c.provinces[e.ProvinceKey]; !ok {
			c.provinces[e.ProvinceKey] = e
		}
		if _, ok := c.departments[e.DepartmentKey]; !ok {
			c.departments[e.DepartmentKey] = e
		}
		if c.provDepts[e.ProvinceKey] == nil {
			c.provDepts[e.ProvinceKey] = map[string]bool{}
		}
		c.provDepts[e.ProvinceKey][e.DepartmentKey] = true
		c.observe(e.ProvinceKey)
		c.observe(e.DepartmentKey)
		c.size++
	}
	return c, nil
}

func (c *Catalog) add(key string, e *Entry, weak bool) {
	for _, h := range c.districts[key] {
		if h.Entry == e {
			return
		}
	}
	c.districts[key] = append(c.districts[key], DistrictHit{Entry: e, Weak: weak})
	c.observe(key)
}

func (c *Catalog) observe(key string) {
	if n := len(strings.Fields(key)); n > c.maxWords {
		c.maxWords = n
	}
}

// PartialDistricts busca distritos cuyo nombre (o alias fuerte) empieza con key,
// o que aparecen completos al inicio de key seguidos de algo más. Sirve para campos
// truncados ("SAN JUAN DE MIRAFLOR") o con agregados ("PUEBLO LIBRE MAGDAL").
// Exige un mínimo de letras para no aceptar prefijos cortos como "SAN".
func (c *Catalog) PartialDistricts(key string) []DistrictHit {
	const minPrefix = 12
	var out []DistrictHit
	seen := map[*Entry]bool{}
	for name, hits := range c.districts {
		match := (len(key) >= minPrefix && strings.HasPrefix(name, key)) ||
			(len(name) >= minPrefix && strings.HasPrefix(key, name+" "))
		if !match {
			continue
		}
		for _, h := range hits {
			if !h.Weak && !seen[h.Entry] {
				seen[h.Entry] = true
				out = append(out, h)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Entry.Code < out[j].Entry.Code })
	return out
}

// ByCode devuelve el distrito por código de ubigeo o nil.
func (c *Catalog) ByCode(code string) *Entry { return c.byCode[code] }

// Lookup busca un nombre ya normalizado con txt.Key.
func (c *Catalog) Lookup(key string) Lookup {
	_, isProv := c.provinces[key]
	_, isDept := c.departments[key]
	return Lookup{Districts: c.districts[key], IsProvince: isProv, IsDepartment: isDept}
}

// ProvinceEntry devuelve un distrito cualquiera de la provincia (sirve para leer sus nombres).
func (c *Catalog) ProvinceEntry(key string) *Entry { return c.provinces[key] }

// DepartmentEntry devuelve un distrito cualquiera del departamento.
func (c *Catalog) DepartmentEntry(key string) *Entry { return c.departments[key] }

// ProvinceInDepartment indica si existe una provincia con ese nombre en el departamento.
func (c *Catalog) ProvinceInDepartment(prov, dept string) bool { return c.provDepts[prov][dept] }

// ZoneOfProvince devuelve la zona de cobertura de una provincia ("" si no existe).
func (c *Catalog) ZoneOfProvince(key string) string {
	if e := c.provinces[key]; e != nil {
		return e.Zone
	}
	return ""
}

// MaxWords es el número máximo de palabras de un nombre del catálogo.
func (c *Catalog) MaxWords() int { return c.maxWords }

// Size es la cantidad de distritos cargados.
func (c *Catalog) Size() int { return c.size }
