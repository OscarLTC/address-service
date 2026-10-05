// Comando dbload: carga el catálogo de ubigeos, los límites distritales, las zonas de
// cobertura y el contenido de un snapshot (calles, tramos y anclas de OSM) en la base
// del plano de control. Reemplaza lo cargado antes con source = osm; no toca lo que
// crearon los operadores.
//
//	go run ./cmd/dbload -db postgres://addrsvc:addrsvc@localhost:55432/addrsvc
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"addrsvc/internal/catalog"
	"addrsvc/internal/geo"
	"addrsvc/internal/snapshot"
	"addrsvc/internal/txt"
)

func main() {
	dsn := flag.String("db", envOr("DATABASE_URL", "postgres://addrsvc:addrsvc@localhost:55432/addrsvc"), "conexión a PostgreSQL")
	dataDir := flag.String("data", "data", "directorio de datos")
	snapPath := flag.String("snapshot", "data/snapshot/lima.snap", "snapshot con las calles a cargar")
	flag.Parse()

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, *dsn)
	if err != nil {
		log.Fatalf("conexión: %v", err)
	}
	defer conn.Close(ctx)

	start := time.Now()
	tx, err := conn.Begin(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer tx.Rollback(ctx)

	n, err := loadUbigeos(ctx, tx, *dataDir)
	if err != nil {
		log.Fatalf("ubigeos: %v", err)
	}
	log.Printf("ubigeos: %d", n)
	if n, err = loadZones(ctx, tx, *dataDir); err != nil {
		log.Fatalf("zonas: %v", err)
	}
	log.Printf("zonas: %d", n)

	snap, err := snapshot.Load(*snapPath)
	if err != nil {
		log.Fatalf("snapshot: %v", err)
	}
	streets, segments, anchors, err := loadStreets(ctx, tx, snap)
	if err != nil {
		log.Fatalf("calles: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		log.Fatal(err)
	}
	log.Printf("calles %d, tramos %d, anclas %d (snapshot %s) en %s", streets, segments, anchors, snap.Version, time.Since(start).Round(time.Millisecond))
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func loadUbigeos(ctx context.Context, tx pgx.Tx, dataDir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, "catalog", "ubigeos.json"))
	if err != nil {
		return 0, err
	}
	var f struct {
		Entries []*catalog.Entry `json:"entries"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return 0, err
	}
	dist, err := geo.LoadDistricts(filepath.Join(dataDir, "geo", "districts.json"))
	if err != nil {
		return 0, err
	}
	batch := &pgx.Batch{}
	for _, e := range f.Entries {
		var wkt any
		if d := dist.ByCode(e.Code); d != nil {
			wkt = ringsWKT(d.Rings)
		}
		batch.Queue(`INSERT INTO ubigeos (code, department, province, district, normalized_name, zone, polygon)
			VALUES ($1,$2,$3,$4,$5,$6, CASE WHEN $7::text IS NULL THEN NULL ELSE ST_Multi(ST_BuildArea(ST_GeomFromText($7::text, 4326))) END)
			ON CONFLICT (code) DO UPDATE SET department=EXCLUDED.department, province=EXCLUDED.province,
				district=EXCLUDED.district, normalized_name=EXCLUDED.normalized_name, zone=EXCLUDED.zone,
				polygon=COALESCE(EXCLUDED.polygon, ubigeos.polygon)`,
			e.Code, e.Department, e.Province, e.District, txt.Key(e.District), e.Zone, wkt)
		for _, a := range e.Aliases {
			batch.Queue(`INSERT INTO ubigeo_aliases VALUES ($1,$2,$3,false) ON CONFLICT DO NOTHING`, e.Code, a, txt.Key(a))
		}
		for _, a := range e.WeakAliases {
			batch.Queue(`INSERT INTO ubigeo_aliases VALUES ($1,$2,$3,true) ON CONFLICT DO NOTHING`, e.Code, a, txt.Key(a))
		}
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return 0, err
	}
	return len(f.Entries), nil
}

// ringsWKT arma un MULTILINESTRING con los anillos; la base lo convierte en área con
// ST_BuildArea, que reconstruye los huecos (anillos interiores) correctamente.
func ringsWKT(rings [][]geo.Point) string {
	var parts []string
	for _, r := range rings {
		pts := make([]string, len(r))
		for i, p := range r {
			pts[i] = fmt.Sprintf("%.6f %.6f", p[0], p[1])
		}
		parts = append(parts, "("+strings.Join(pts, ",")+")")
	}
	return "MULTILINESTRING(" + strings.Join(parts, ",") + ")"
}

func loadZones(ctx context.Context, tx pgx.Tx, dataDir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, "config", "zones.json"))
	if err != nil {
		return 0, err
	}
	var f struct {
		Zones map[string]string `json:"zones"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return 0, err
	}
	for name, status := range f.Zones {
		if status == "active" || status == "shadow" || status == "off" {
			if _, err := tx.Exec(ctx, `INSERT INTO coverage_zones (name, status) VALUES ($1,$2)
				ON CONFLICT (name) DO UPDATE SET status = EXCLUDED.status`, name, status); err != nil {
				return 0, err
			}
		}
	}
	return len(f.Zones), nil
}

// loadStreets reemplaza las calles de OSM por las del snapshot. Las calles creadas por
// operadores (source <> 'osm') no se tocan.
func loadStreets(ctx context.Context, tx pgx.Tx, snap *snapshot.Snapshot) (int, int, int, error) {
	for _, q := range []string{
		`DELETE FROM anchors WHERE source = 'osm'`,
		`DELETE FROM street_segments WHERE street_id IN (SELECT id FROM streets WHERE source = 'osm')`,
		`DELETE FROM street_aliases WHERE street_id IN (SELECT id FROM streets WHERE source = 'osm')`,
		`DELETE FROM streets WHERE source = 'osm'`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			return 0, 0, 0, err
		}
	}
	rows := make([][]any, 0, len(snap.Streets))
	for _, s := range snap.Streets {
		rows = append(rows, []any{int64(s.ID), s.Ubigeo, s.Type, s.Name, s.Key, s.Phonetic, "osm", strings.Join(s.Refs, " ")})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"streets"},
		[]string{"id", "ubigeo_code", "street_type", "canonical_name", "normalized_name", "phonetic_name", "source", "source_ref"},
		pgx.CopyFromRows(rows)); err != nil {
		return 0, 0, 0, err
	}
	if _, err := tx.Exec(ctx, `SELECT setval('streets_id_seq', (SELECT max(id) FROM streets))`); err != nil {
		return 0, 0, 0, err
	}

	// Tramos y anclas: se cargan como texto WKT en tablas temporales y se convierten.
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE tmp_segments (street_id bigint, wkt text, source_ref text) ON COMMIT DROP;
		CREATE TEMP TABLE tmp_anchors (street_id bigint, house_number int, wkt text, source text) ON COMMIT DROP`); err != nil {
		return 0, 0, 0, err
	}
	var segRows, anchorRows [][]any
	for _, s := range snap.Streets {
		for i, l := range s.Lines {
			pts := make([]string, len(l))
			for j, p := range l {
				pts[j] = fmt.Sprintf("%.7f %.7f", p[0], p[1])
			}
			ref := ""
			if i < len(s.Refs) {
				ref = s.Refs[i]
			}
			segRows = append(segRows, []any{int64(s.ID), "LINESTRING(" + strings.Join(pts, ",") + ")", ref})
		}
		for _, a := range s.Anchors {
			anchorRows = append(anchorRows, []any{int64(s.ID), a.Number, fmt.Sprintf("POINT(%.7f %.7f)", a.Point[0], a.Point[1]), "osm"})
		}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"tmp_segments"}, []string{"street_id", "wkt", "source_ref"}, pgx.CopyFromRows(segRows)); err != nil {
		return 0, 0, 0, err
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"tmp_anchors"}, []string{"street_id", "house_number", "wkt", "source"}, pgx.CopyFromRows(anchorRows)); err != nil {
		return 0, 0, 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO street_segments (street_id, geom, source_ref)
		SELECT street_id, ST_GeomFromText(wkt, 4326), source_ref FROM tmp_segments`); err != nil {
		return 0, 0, 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO anchors (street_id, house_number, location, source)
		SELECT street_id, house_number, ST_GeomFromText(wkt, 4326), source FROM tmp_anchors`); err != nil {
		return 0, 0, 0, err
	}
	return len(rows), len(segRows), len(anchorRows), nil
}
