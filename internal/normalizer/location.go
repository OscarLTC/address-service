package normalizer

import (
	"strings"

	"addrsvc/internal/catalog"
	"addrsvc/internal/txt"
)

type nameMatch struct {
	key string
	lk  catalog.Lookup
}

type suffixResult struct {
	Entry      *catalog.Entry // resultado a nivel distrito
	Department string         // resultado a nivel provincia o departamento
	Province   string
	Weak       bool
	OK         bool
	Flags      []string
}

// scanSuffix busca, de derecha a izquierda, nombres de distrito, provincia y
// departamento al final del texto. No depende de comas ni guiones.
//
// Solo sigue consumiendo hacia la izquierda si el nombre recién consumido puede
// ser provincia o departamento: un distrito "puro" cierra el grupo, así una
// calle con nombre de distrito ("Jr. Santa Rosa, Rímac") no se confunde.
//
// Un nombre precedido por un conector o un tipo de vía, sin coma ni guion en medio,
// es el final de otro nombre ("Urb. Praderas de Lurín", "av. San Luis"), no la
// ubicación.
func (n *Normalizer) scanSuffix(tokens []string, sepBefore []bool) (names []nameMatch, rest []string) {
	rest = tokens
	for iter := 0; iter < 3; iter++ {
		found := false
		maxK := n.cat.MaxWords()
		if maxK > len(rest) {
			maxK = len(rest)
		}
		for k := maxK; k >= 1; k-- {
			key := txt.Key(strings.Join(rest[len(rest)-k:], " "))
			lk := n.cat.Lookup(key)
			if !lk.Known() || (len(names) > 0 && !n.nestsIn(nameMatch{key, lk}, names[0].key)) {
				continue
			}
			if start := len(rest) - k; start > 0 && !sepBefore[start] && n.endsName(rest[start-1]) {
				continue
			}
			names = append([]nameMatch{{key: key, lk: lk}}, names...)
			rest = rest[:len(rest)-k]
			found = true
			if !(lk.IsProvince || lk.IsDepartment) {
				return names, rest
			}
			break
		}
		if !found {
			break
		}
	}
	return names, rest
}

// nameConnectors son palabras que unen partes de un nombre propio.
var nameConnectors = map[string]bool{"DE": true, "DEL": true, "LA": true, "LAS": true, "LOS": true, "EL": true, "Y": true}

// endsName indica si prev obliga a leer lo que sigue como parte de un nombre: un
// conector ("de", "los") o un tipo de vía ("av", "jr").
func (n *Normalizer) endsName(prev string) bool {
	_, isType := n.lex.streetType[txt.NoEnye(prev)]
	return nameConnectors[prev] || isType
}

// nestsIn indica si el nombre puede estar dentro del área outer (provincia o
// departamento). Evita que en "Av. Arequipa, Lima" se tome "Arequipa" como
// ubicación: no hay un Arequipa dentro de Lima.
func (n *Normalizer) nestsIn(inner nameMatch, outer string) bool {
	if inner.key == outer {
		return true // "Lima, Lima"
	}
	for _, h := range inner.lk.Districts {
		if h.Entry.ProvinceKey == outer || h.Entry.DepartmentKey == outer {
			return true
		}
	}
	return inner.lk.IsProvince && n.cat.ProvinceInDepartment(inner.key, outer)
}

// activeDistrict elige, entre distritos con el mismo nombre, el único que cae en una
// zona activa. Sirve para que "Miraflores" sea el de Lima y no el de Arequipa cuando
// la cobertura es Lima. Si hay cero o más de uno en zonas activas, no elige.
func (n *Normalizer) activeDistrict(hits []catalog.DistrictHit) (catalog.DistrictHit, bool) {
	var found []catalog.DistrictHit
	for _, h := range hits {
		if n.opts.ActiveZones[h.Entry.Zone] {
			found = append(found, h)
		}
	}
	if len(found) != 1 {
		return catalog.DistrictHit{}, false
	}
	return found[0], true
}

// areaActive indica si el nombre, leído como provincia o departamento, cae en una
// zona activa ("Lima" sí; "San Miguel" de Cajamarca no, si solo Lima está activa).
func (n *Normalizer) areaActive(nm nameMatch) bool {
	var e *catalog.Entry
	if nm.lk.IsProvince {
		e = n.cat.ProvinceEntry(nm.key)
	} else if nm.lk.IsDepartment {
		e = n.cat.DepartmentEntry(nm.key)
	}
	return e != nil && n.opts.ActiveZones[e.Zone]
}

func flagFor(prefix, key string) string {
	return prefix + strings.ReplaceAll(key, " ", "_")
}

// areaContext son la provincia y el departamento enviados en campos (claves txt.Key).
// Sirven para desempatar un distrito del sufijo cuyo nombre se repite en el país.
type areaContext struct {
	province, department string
}

// resolveNames valida los nombres encontrados contra la jerarquía del catálogo.
func (n *Normalizer) resolveNames(names []nameMatch, ctx areaContext) suffixResult {
	switch len(names) {
	case 0:
		return suffixResult{}
	case 3:
		for _, h := range names[0].lk.Districts {
			if h.Entry.ProvinceKey == names[1].key && h.Entry.DepartmentKey == names[2].key {
				return suffixResult{Entry: h.Entry, Weak: h.Weak, OK: true}
			}
		}
	case 2:
		if names[0].key == names[1].key {
			if r, ok := n.areaLevel(names[0]); ok {
				return r
			}
		}
		for _, h := range names[0].lk.Districts {
			if h.Entry.ProvinceKey == names[1].key || h.Entry.DepartmentKey == names[1].key {
				return suffixResult{Entry: h.Entry, Weak: h.Weak, OK: true}
			}
		}
	case 1:
		nm := names[0]
		if h, ok := districtInContext(nm, ctx); ok {
			return suffixResult{Entry: h.Entry, Weak: h.Weak, OK: true}
		}
		ambiguous := len(nm.lk.Districts) > 1 || nm.lk.IsProvince || nm.lk.IsDepartment
		if ambiguous && !n.areaActive(nm) {
			if h, ok := n.activeDistrict(nm.lk.Districts); ok {
				return suffixResult{Entry: h.Entry, Weak: h.Weak, OK: true, Flags: []string{"DISTRICT_BY_ACTIVE_ZONE"}}
			}
			// "Bellavista" es provincia en San Martín y distrito en el Callao: leerlo
			// como provincia sería elegir en silencio.
			if districtOutsideArea(nm) {
				return suffixResult{Flags: []string{"AMBIGUOUS_DISTRICT"}}
			}
		}
		if r, ok := n.areaLevel(nm); ok {
			return r
		}
		switch len(nm.lk.Districts) {
		case 1:
			h := nm.lk.Districts[0]
			return suffixResult{Entry: h.Entry, Weak: h.Weak, OK: true}
		case 0:
		default:
			return suffixResult{Flags: []string{"AMBIGUOUS_DISTRICT"}}
		}
	}
	return suffixResult{Flags: []string{"LOCATION_INCONSISTENT"}}
}

// districtInContext elige el único distrito del nombre que cae en la provincia o el
// departamento enviados en campos. No aplica si el nombre es esa misma área
// ("Lima" con province="Lima"): ahí el nombre se lee como área, no como distrito.
func districtInContext(nm nameMatch, ctx areaContext) (catalog.DistrictHit, bool) {
	if ctx == (areaContext{}) || nm.key == ctx.province || nm.key == ctx.department {
		return catalog.DistrictHit{}, false
	}
	var found []catalog.DistrictHit
	for _, h := range nm.lk.Districts {
		if (ctx.province == "" || h.Entry.ProvinceKey == ctx.province) &&
			(ctx.department == "" || h.Entry.DepartmentKey == ctx.department) {
			found = append(found, h)
		}
	}
	if len(found) != 1 {
		return catalog.DistrictHit{}, false
	}
	return found[0], true
}

// districtOutsideArea indica si el nombre, que también es provincia o departamento,
// es además un distrito fuera de esa área. "Huaral" no lo es (su distrito está en la
// provincia Huaral); "Bellavista" sí.
func districtOutsideArea(nm nameMatch) bool {
	for _, h := range nm.lk.Districts {
		if h.Entry.ProvinceKey != nm.key && h.Entry.DepartmentKey != nm.key {
			return true
		}
	}
	return false
}

// areaLevel resuelve nombres como "Lima" o "Callao", que son a la vez distrito,
// provincia y departamento: no se asume distrito.
func (n *Normalizer) areaLevel(nm nameMatch) (suffixResult, bool) {
	if !(nm.lk.IsProvince || nm.lk.IsDepartment) {
		return suffixResult{}, false
	}
	var e *catalog.Entry
	if nm.lk.IsProvince {
		e = n.cat.ProvinceEntry(nm.key)
	} else {
		e = n.cat.DepartmentEntry(nm.key)
	}
	if e == nil {
		return suffixResult{}, false
	}
	r := suffixResult{Department: e.Department, OK: true}
	if nm.lk.IsProvince {
		r.Province = e.Province
	}
	if nm.lk.IsProvince && nm.lk.IsDepartment {
		r.Flags = []string{flagFor("AMBIGUOUS_", nm.key)}
	}
	return r, true
}

func fromEntry(e *catalog.Entry, source string) Location {
	return Location{
		Department: e.Department,
		Province:   e.Province,
		District:   e.District,
		Ubigeo:     e.Code,
		Source:     source,
	}
}

// locate aplica las precedencias: ubigeo > campos > sufijo del texto > área por defecto.
func (n *Normalizer) locate(req Request, sres suffixResult, flags *flagSet) Location {
	loc := Location{Source: "NONE"}

	if u := strings.TrimSpace(req.Ubigeo); u != "" {
		if e := n.cat.ByCode(u); e != nil {
			loc = fromEntry(e, "UBIGEO")
			if n.fieldContradicts(req.District, e) {
				flags.add("DISTRICT_CONFLICT")
			}
		} else {
			flags.add("INVALID_UBIGEO")
		}
	}
	if loc.Source == "NONE" {
		loc = n.fromFields(req, flags)
	}

	for _, f := range sres.Flags {
		flags.add(f)
	}
	switch {
	case !sres.OK:
	case loc.Source == "NONE":
		if sres.Entry != nil {
			loc = fromEntry(sres.Entry, "TEXT")
			if sres.Weak {
				flags.add("WEAK_DISTRICT_ALIAS")
			}
		} else {
			loc = Location{Department: sres.Department, Province: sres.Province, Source: "TEXT"}
		}
	case sres.Entry != nil && loc.District == "" &&
		(loc.Province == "" || txt.Key(sres.Entry.Province) == txt.Key(loc.Province)):
		loc = fromEntry(sres.Entry, "TEXT")
		if sres.Weak {
			flags.add("WEAK_DISTRICT_ALIAS")
		}
	case sres.Entry != nil && loc.Ubigeo != "" && sres.Entry.Code != loc.Ubigeo:
		flags.add("DISTRICT_CONFLICT")
	}

	if loc.Province == "" && loc.Department == "" && n.opts.DefaultProvince != "" {
		loc.Department = n.opts.DefaultDepartment
		loc.Province = n.opts.DefaultProvince
		loc.Source = "DEFAULT"
		flags.add("AREA_FROM_DEFAULT")
	}

	loc.Zone = n.cat.ZoneOfProvince(txt.Key(loc.Province))
	loc.InScope = loc.Zone != "" && n.opts.ActiveZones[loc.Zone]
	if loc.Source == "NONE" {
		flags.add("LOCATION_UNKNOWN")
	}
	return loc
}

// fieldContradicts indica si el campo district nombra un distrito conocido que no
// es el del ubigeo. Un nombre desconocido no cuenta como contradicción.
func (n *Normalizer) fieldContradicts(district string, e *catalog.Entry) bool {
	hits := n.cat.Lookup(txt.Key(district)).Districts
	for _, h := range hits {
		if h.Entry == e {
			return false
		}
	}
	return len(hits) > 0
}

// districtInName busca un nombre de distrito (sin alias débiles) dentro de un texto
// como el de una urbanización. Solo sirve de pista: no asigna la ubicación.
func (n *Normalizer) districtInName(name string) bool {
	words := strings.Fields(txt.Key(name))
	for i := range words {
		for k := 1; k <= n.cat.MaxWords() && i+k <= len(words); k++ {
			for _, h := range n.cat.Lookup(strings.Join(words[i:i+k], " ")).Districts {
				if !h.Weak {
					return true
				}
			}
		}
	}
	return false
}

// fromFields resuelve los campos estructurados district/province/department.
func (n *Normalizer) fromFields(req Request, flags *flagSet) Location {
	none := Location{Source: "NONE"}
	dk, pk, ek := txt.Key(req.District), txt.Key(req.Province), txt.Key(req.Department)
	if dk == "" && pk == "" && ek == "" {
		return none
	}
	if dk != "" {
		hits := n.cat.Lookup(dk).Districts
		var filtered []catalog.DistrictHit
		for _, h := range hits {
			if (pk == "" || h.Entry.ProvinceKey == pk) && (ek == "" || h.Entry.DepartmentKey == ek) {
				filtered = append(filtered, h)
			}
		}
		switch {
		case len(filtered) == 1:
			if filtered[0].Weak {
				flags.add("WEAK_DISTRICT_ALIAS")
			}
			return fromEntry(filtered[0].Entry, "FIELD")
		case len(filtered) > 1:
			if h, ok := n.activeDistrict(filtered); ok {
				flags.add("DISTRICT_BY_ACTIVE_ZONE")
				if h.Weak {
					flags.add("WEAK_DISTRICT_ALIAS")
				}
				return fromEntry(h.Entry, "FIELD")
			}
			flags.add("AMBIGUOUS_DISTRICT")
			return none
		case len(hits) == 1:
			flags.add("LOCATION_INCONSISTENT")
			return fromEntry(hits[0].Entry, "FIELD")
		case len(hits) > 1:
			flags.add("LOCATION_INCONSISTENT")
			return none
		}
		// Campo truncado o con agregados ("SAN JUAN DE MIRAFLOR", "PUEBLO LIBRE
		// (MAGDAL"): se acepta solo un candidato único dentro del área enviada.
		var partial []catalog.DistrictHit
		for _, h := range n.cat.PartialDistricts(dk) {
			if (pk == "" || h.Entry.ProvinceKey == pk) && (ek == "" || h.Entry.DepartmentKey == ek) {
				partial = append(partial, h)
			}
		}
		if len(partial) == 1 {
			flags.add("DISTRICT_FIELD_PARTIAL")
			return fromEntry(partial[0].Entry, "FIELD")
		}
		flags.add("UNKNOWN_DISTRICT")
	}
	if pk != "" {
		if e := n.cat.ProvinceEntry(pk); e != nil {
			return Location{Department: e.Department, Province: e.Province, Source: "FIELD"}
		}
		flags.add("UNKNOWN_PROVINCE")
	}
	if ek != "" {
		if e := n.cat.DepartmentEntry(ek); e != nil {
			return Location{Department: e.Department, Source: "FIELD"}
		}
		flags.add("UNKNOWN_DEPARTMENT")
	}
	return none
}
