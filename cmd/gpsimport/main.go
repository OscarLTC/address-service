// Comando gpsimport: convierte el GPS de entrega exportado con
// scripts/emsd/gps_entregas.sql en un dataset de coordenadas de verdad (ADR 0005).
//
// Agrupa las entregas por dirección normalizada (match_key + ubigeo), valida cada
// grupo con internal/truth y escribe una fila por dirección en el formato del
// dataset de oro, con verification_method gps_entrega_oro o gps_entrega_plata.
// Entrada y salida contienen direcciones de clientes: solo dentro de data/private/.
//
//	go run ./cmd/gpsimport -in data/private/emsd_gps.csv -out data/private/golden_gps_v1.csv
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"addrsvc/internal/catalog"
	"addrsvc/internal/geo"
	"addrsvc/internal/golden"
	"addrsvc/internal/normalizer"
	"addrsvc/internal/truth"
)

type delivery struct {
	id, address, number, reference, district, province, department, ubigeo, day string
	point                                                                       geo.Point
	arrival                                                                     *geo.Point
}

func private(p string) bool {
	return strings.HasPrefix(filepath.ToSlash(filepath.Clean(p)), "data/private/")
}

func main() {
	in := flag.String("in", "data/private/emsd_gps.csv", "GPS de entrega exportado")
	out := flag.String("out", "data/private/golden_gps_v1.csv", "dataset de coordenadas de verdad")
	dataDir := flag.String("data", "data", "directorio de datos")
	testPct := flag.Uint64("test-pct", 30, "porcentaje de direcciones en la partición test")
	flag.Parse()
	if !private(*in) || !private(*out) {
		log.Fatal("la entrada y la salida deben estar dentro de data/private/")
	}

	cat, err := catalog.Load(filepath.Join(*dataDir, "catalog", "ubigeos.json"))
	if err != nil {
		log.Fatal(err)
	}
	lex, err := normalizer.LoadLexicon(filepath.Join(*dataDir, "rules", "lexicon.json"))
	if err != nil {
		log.Fatal(err)
	}
	norm := normalizer.New(cat, lex, normalizer.Options{})
	districts, err := geo.LoadDistricts(filepath.Join(*dataDir, "geo", "districts.json"))
	if err != nil {
		log.Fatal(err)
	}

	ds, err := read(*in)
	if err != nil {
		log.Fatal(err)
	}
	groups := map[string][]delivery{}
	var order []string
	skipped := 0
	for _, d := range ds {
		r := norm.Normalize(normalizer.Request{Address: d.address, Number: d.number, District: d.district,
			Province: d.province, Department: d.department, Ubigeo: d.ubigeo})
		if r.MatchKey == "" || r.Location.Ubigeo == "" {
			skipped++
			continue
		}
		k := r.MatchKey + "|" + r.Location.Ubigeo
		if groups[k] == nil {
			order = append(order, k)
		}
		groups[k] = append(groups[k], d)
	}

	stats := map[string]int{}
	var rows []golden.Row
	for _, k := range order {
		g := groups[k]
		addr := truth.Address{Key: k, Ubigeo: g[0].ubigeo}
		for _, d := range g {
			addr.Deliveries = append(addr.Deliveries, truth.Delivery{Day: d.day, Point: d.point, Arrival: d.arrival})
		}
		res := truth.Resolve(addr, districts)
		if res.Quality == "" {
			stats["descartada:"+res.Reason]++
			continue
		}
		stats[res.Quality]++
		first := g[0]
		raw := first.address
		if first.number != "" {
			raw += " " + first.number
		}
		split := "dev"
		if h := fnv.New64a(); true {
			h.Write([]byte(k))
			if h.Sum64()%100 < *testPct {
				split = "test"
			}
		}
		rows = append(rows, golden.Row{
			ID: "GPS-" + first.id, Source: "emsd_gps", RawAddress: raw,
			DistrictField: first.district, ProvinceField: first.province, DepartmentField: first.department,
			ExpectedUbigeo:     first.ubigeo,
			Lat:                strconv.FormatFloat(res.Point[1], 'f', 6, 64),
			Lng:                strconv.FormatFloat(res.Point[0], 'f', 6, 64),
			VerificationMethod: "gps_entrega_" + res.Quality, VerifiedBy: "app_motorizado",
			Split: split, Notes: fmt.Sprintf("%s; entregas válidas %d de %d", res.Reason, res.Valid, len(g)),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	if err := golden.Write(f, rows); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
	log.Printf("%s: %d entregas, %d direcciones, %d sin parseo; %v", *out, len(ds), len(groups), skipped, stats)
}

func read(path string) ([]delivery, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	head, err := r.Read()
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i, h := range head {
		idx[strings.TrimSpace(h)] = i
	}
	need := []string{"id_muestra", "direccion", "numero", "referencia", "distrito", "provincia", "departamento",
		"ubigeo", "fecha_entrega", "lat_entrega", "lng_entrega", "lat_llegada", "lng_llegada"}
	for _, c := range need {
		if _, ok := idx[c]; !ok {
			return nil, fmt.Errorf("falta la columna %q", c)
		}
	}
	var out []delivery
	for {
		rec, err := r.Read()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		get := func(c string) string { return strings.TrimSpace(rec[idx[c]]) }
		lat, err1 := strconv.ParseFloat(get("lat_entrega"), 64)
		lng, err2 := strconv.ParseFloat(get("lng_entrega"), 64)
		if err1 != nil || err2 != nil {
			continue
		}
		d := delivery{id: get("id_muestra"), address: get("direccion"), number: get("numero"), reference: get("referencia"),
			district: get("distrito"), province: get("provincia"), department: get("departamento"), ubigeo: get("ubigeo"),
			day: get("fecha_entrega"), point: geo.Point{lng, lat}}
		if la, e1 := strconv.ParseFloat(get("lat_llegada"), 64); e1 == nil {
			if lo, e2 := strconv.ParseFloat(get("lng_llegada"), 64); e2 == nil {
				d.arrival = &geo.Point{lo, la}
			}
		}
		out = append(out, d)
	}
}
