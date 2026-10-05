package api

import (
	"bufio"
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

func testServer(t *testing.T, cfg Config) *httptest.Server {
	t.Helper()
	root := filepath.Join("..", "..", "data")
	cat, err := catalog.Load(filepath.Join(root, "catalog", "ubigeos.json"))
	if err != nil {
		t.Fatal(err)
	}
	lex, err := normalizer.LoadLexicon(filepath.Join(root, "rules", "lexicon.json"))
	if err != nil {
		t.Fatal(err)
	}
	norm := normalizer.New(cat, lex, normalizer.Options{ActiveZones: map[string]bool{"LIMA_METRO": true}})
	snap := &snapshot.Snapshot{Format: snapshot.FormatVersion, Version: "test", Streets: []snapshot.Street{{
		ID: 1, Ubigeo: "150116", Type: "AVENIDA", Name: "LAS PALMERAS", Key: txt.Key("LAS PALMERAS"), Phonetic: txt.Phonetic("LAS PALMERAS"),
		Lines:   [][]geo.Point{{{-77.0400, -12.0800}, {-77.0390, -12.0800}}},
		Anchors: []snapshot.Anchor{{Number: 100, Point: geo.Point{-77.0400, -12.0800}}, {Number: 120, Point: geo.Point{-77.0398, -12.0800}}},
	}}}
	cfg.Normalizer = norm
	cfg.Resolver = resolver.New(norm, snap, map[string]geo.Point{"150116": {-77.036, -12.083}})
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(New(cfg).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, url, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestGeocodeAndAuth(t *testing.T) {
	srv := testServer(t, Config{KeyHashes: map[string]string{HashKey("secreta"): "cliente-a"}})
	if r := post(t, srv.URL+"/v1/geocode", `{"address":"Av. Las Palmeras 120","district":"Lince"}`, nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("sin API key: código %d, se esperaba 401", r.StatusCode)
	}
	r := post(t, srv.URL+"/v1/geocode", `{"external_id":"P-1","address":"Av. Las Palmeras 120","district":"Lince"}`, map[string]string{"X-API-Key": "secreta"})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("código %d", r.StatusCode)
	}
	var g GeocodeResponse
	if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
		t.Fatal(err)
	}
	if g.ExternalID != "P-1" || g.Decision != resolver.AutoAccept || g.DatasetVersion != "test" {
		t.Errorf("respuesta inesperada: %+v", g)
	}
}

func TestRateLimit(t *testing.T) {
	srv := testServer(t, Config{RatePerSecond: 1})
	codes := map[int]int{}
	for i := 0; i < 5; i++ {
		codes[post(t, srv.URL+"/v1/normalize", `{"address":"Jr. Ica 345"}`, nil).StatusCode]++
	}
	if codes[http.StatusTooManyRequests] == 0 {
		t.Errorf("se esperaba al menos un 429: %v", codes)
	}
}

func TestBatchOrderDedupAndIdempotency(t *testing.T) {
	dir := t.TempDir()
	events, err := NewEventBuffer(filepath.Join(dir, "events.ndjson"), 100)
	if err != nil {
		t.Fatal(err)
	}
	srv := testServer(t, Config{Events: events, MaxBatch: 10})
	body := `{"items":[
		{"external_id":"A","address":"Av. Las Palmeras 120","district":"Lince"},
		{"external_id":"B","address":"Jr. Inventado 10","district":"Lince"},
		{"external_id":"C","address":"Av. Las Palmeras 120","district":"Lince"}]}`
	r := post(t, srv.URL+"/v1/geocode/batch", body, map[string]string{"Idempotency-Key": "k1"})
	if r.StatusCode != http.StatusOK || r.Header.Get("X-Batch-Unique") != "2" {
		t.Fatalf("código %d, únicas %q", r.StatusCode, r.Header.Get("X-Batch-Unique"))
	}
	var ids, decisions []string
	sc := bufio.NewScanner(r.Body)
	for sc.Scan() {
		var g GeocodeResponse
		if err := json.Unmarshal(sc.Bytes(), &g); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, g.ExternalID)
		decisions = append(decisions, g.Decision)
	}
	if strings.Join(ids, ",") != "A,B,C" || decisions[0] != decisions[2] || decisions[1] != resolver.Review {
		t.Errorf("orden o decisiones inesperadas: %v %v", ids, decisions)
	}
	if r2 := post(t, srv.URL+"/v1/geocode/batch", body, map[string]string{"Idempotency-Key": "k1"}); r2.Header.Get("Idempotent-Replay") != "true" {
		t.Error("la segunda solicitud con la misma Idempotency-Key debía ser un replay")
	}
	if r3 := post(t, srv.URL+"/v1/geocode/batch", `{"items":[]}`, nil); r3.StatusCode != http.StatusBadRequest {
		t.Errorf("lote vacío: código %d, se esperaba 400", r3.StatusCode)
	}

	events.Close()
	data, _ := os.ReadFile(filepath.Join(dir, "events.ndjson"))
	if n := strings.Count(string(data), "\n"); n != 2 {
		t.Errorf("se esperaban 2 eventos (direcciones únicas), hay %d", n)
	}
	if strings.Contains(string(data), "Palmeras") {
		t.Error("los eventos no deben contener el texto de la dirección")
	}
}

func TestReadyAndMetrics(t *testing.T) {
	srv := testServer(t, Config{})
	post(t, srv.URL+"/v1/geocode", `{"address":"Av. Las Palmeras 120","district":"Lince"}`, nil)
	resp, err := http.Get(srv.URL + "/readyz")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("readyz: %v %v", err, resp.StatusCode)
	}
	resp.Body.Close()
	m, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Body.Close()
	b, _ := io.ReadAll(m.Body)
	for _, want := range []string{`addrsvc_requests_total{route="geocode",code="200"} 1`, `addrsvc_decisions_total{decision="AUTO_ACCEPT"`, `addrsvc_request_duration_ms_count{route="geocode"} 1`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("faltan métricas %q en:\n%s", want, b)
		}
	}
}
