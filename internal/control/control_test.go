package control

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"addrsvc/internal/catalog"
	"addrsvc/internal/geo"
	"addrsvc/internal/normalizer"
	"addrsvc/internal/resolver"
	"addrsvc/internal/snapshot"
	"addrsvc/internal/txt"
)

// Prueba de integración contra PostgreSQL + PostGIS con el esquema de migrations/ y
// los ubigeos cargados (cmd/dbload). Corre solo si ADDRSVC_TEST_DB está definido.
func TestReviewFlow(t *testing.T) {
	dsn := os.Getenv("ADDRSVC_TEST_DB")
	if dsn == "" {
		t.Skip("ADDRSVC_TEST_DB no definido")
	}
	ctx := context.Background()
	store, err := NewStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	// t.Cleanup corre en orden inverso: primero se limpia y después se cierra el pool.
	t.Cleanup(store.Close)
	const tag = "prueba-integracion"
	cleanup := func() {
		for _, q := range []string{
			`DELETE FROM decision_events WHERE actor = '` + tag + `'`,
			`DELETE FROM observations WHERE author = '` + tag + `'`,
			`DELETE FROM review_tickets WHERE source = 'import:` + tag + `'`,
			`DELETE FROM canonical_addresses c WHERE NOT EXISTS (SELECT 1 FROM observations o WHERE o.canonical_address_id = c.id)`,
		} {
			if _, err := store.db.Exec(ctx, q); err != nil {
				t.Errorf("limpieza: %v", err)
			}
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	root := filepath.Join("..", "..", "data")
	cat, _ := catalog.Load(filepath.Join(root, "catalog", "ubigeos.json"))
	lex, _ := normalizer.LoadLexicon(filepath.Join(root, "rules", "lexicon.json"))
	norm := normalizer.New(cat, lex, normalizer.Options{ActiveZones: map[string]bool{"LIMA_METRO": true}})
	street := func(id int, name string, lng float64) snapshot.Street {
		return snapshot.Street{ID: id, Ubigeo: "150116", Type: "CALLE", Name: name, Key: txt.Key(name), Phonetic: txt.Phonetic(name),
			Lines: [][]geo.Point{{{lng, -12.0850}, {lng + 0.0005, -12.0850}}}}
	}
	// Los ids de calle deben existir en la base (cmd/dbload): se usan dos calles reales de Lince.
	var id1, id2 int
	if err := store.db.QueryRow(ctx, `SELECT min(id), max(id) FROM streets WHERE ubigeo_code = '150116'`).Scan(&id1, &id2); err != nil || id1 == id2 {
		t.Skip("la base no tiene calles de Lince cargadas (cmd/dbload)")
	}
	snap := &snapshot.Snapshot{Format: snapshot.FormatVersion, Version: "test", Streets: []snapshot.Street{
		street(id1, "LOS NARANJOS INVENTADOS", -77.0350), street(id2, "LOS NARANJOS INVENTADOS", -77.0300),
	}}
	res := resolver.New(norm, snap, map[string]geo.Point{"150116": {-77.036, -12.083}})
	srv := httptest.NewServer(NewServer(store, res, map[string]Operator{HashToken("tok"): {Name: tag, Role: "operador"}},
		slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	call := func(method, path, body string) (int, map[string]any, []any) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("X-Admin-Token", "tok")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var obj map[string]any
		var arr []any
		if json.Unmarshal(raw, &obj) != nil {
			_ = json.Unmarshal(raw, &arr)
		}
		return resp.StatusCode, obj, arr
	}

	// Dos veces la misma dirección ambigua (homónimas sin número) y una sin calle.
	csv := "external_id,address,district\nA,Calle Los Naranjos Inventados,Lince\nB,CALLE LOS NARANJOS INVENTADOS,Lince\nC,Jr. Inexistente 10,Lince\n"
	code, stats, _ := call("POST", "/admin/api/import", csv)
	if code != 200 || stats["REVIEW"] != float64(3) || stats["tickets_nuevos"] != float64(2) || stats["tickets_repetidos"] != float64(1) {
		t.Fatalf("importación %d: %v", code, stats)
	}

	_, _, queue := call("GET", "/admin/api/tickets?status=open&limit=500", "")
	var id float64
	for _, q := range queue {
		m := q.(map[string]any)
		if strings.Contains(m["address"].(string), "Naranjos Inventados") {
			id = m["id"].(float64)
			if m["occurrences"] != float64(2) {
				t.Errorf("la dirección repetida debía sumar ocurrencias: %v", m)
			}
		}
	}
	if id == 0 {
		t.Fatal("el ticket no aparece en la cola")
	}
	path := "/admin/api/tickets/" + strings.TrimSuffix(strings.TrimSuffix(json.Number(jsonNum(id)).String(), ".0"), ".00")

	code, detail, _ := call("GET", path, "")
	if code != 200 || detail["district_geojson"] == nil || detail["streets_geojson"] == nil {
		t.Fatalf("detalle %d: faltan geometrías", code)
	}

	// Pin fuera de Lince y lejos de la calle: alertas sin confirmar.
	sid := jsonNum(float64(id1))
	if code, body, _ := call("POST", path+"/resolve", `{"action":"pin","lat":-12.0850,"lng":-77.0350,"street_id":999999999}`); code != http.StatusBadRequest {
		t.Errorf("calle inexistente: %d %v", code, body)
	}
	code, body, _ := call("POST", path+"/resolve", `{"action":"pin","lat":-12.20,"lng":-76.90,"street_id":`+sid+`}`)
	if code != http.StatusConflict || len(body["warnings"].([]any)) != 2 {
		t.Fatalf("se esperaban 2 alertas: %d %v", code, body)
	}
	// Irresoluble sin nota: error.
	if code, _, _ := call("POST", path+"/resolve", `{"action":"unresolvable"}`); code != http.StatusBadRequest {
		t.Errorf("irresoluble sin nota: %d", code)
	}
	// Pin confirmado: queda la observación y la auditoría.
	if code, body, _ := call("POST", path+"/resolve", `{"action":"pin","lat":-12.20,"lng":-76.90,"street_id":`+sid+`,"confirm":true}`); code != 200 {
		t.Fatalf("pin confirmado: %d %v", code, body)
	}
	var obs, events int
	store.db.QueryRow(ctx, `SELECT count(*) FROM observations WHERE author = $1 AND method = 'pin_operador' AND source_quality = 'oro'`, tag).Scan(&obs)
	store.db.QueryRow(ctx, `SELECT count(*) FROM decision_events WHERE actor = $1 AND before IS NOT NULL AND after->>'status' = 'resolved'`, tag).Scan(&events)
	if obs != 1 || events != 1 {
		t.Errorf("observaciones %d y eventos %d, se esperaba 1 y 1", obs, events)
	}
	if code, _, _ := call("POST", path+"/resolve", `{"action":"escalate"}`); code != http.StatusConflict {
		t.Errorf("un ticket resuelto no se puede volver a resolver: %d", code)
	}
}

func jsonNum(f float64) string { b, _ := json.Marshal(int64(f)); return string(b) }
