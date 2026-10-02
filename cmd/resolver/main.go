// Comando resolver: por ahora expone /v1/normalize (Fase 1 del plan).
// Más adelante aquí vivirá el snapshot en RAM y /v1/geocode.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"addrsvc/internal/catalog"
	"addrsvc/internal/normalizer"
)

type normalizeResponse struct {
	normalizer.Result
	ProcessingUS int64 `json:"processing_us"`
}

func main() {
	addr := flag.String("addr", ":8080", "dirección de escucha")
	dataDir := flag.String("data", "data", "directorio de datos (catálogo, reglas, zonas)")
	flag.Parse()

	cat, err := catalog.Load(filepath.Join(*dataDir, "catalog", "ubigeos.json"))
	if err != nil {
		log.Fatalf("catálogo: %v", err)
	}
	lex, err := normalizer.LoadLexicon(filepath.Join(*dataDir, "rules", "lexicon.json"))
	if err != nil {
		log.Fatalf("léxico: %v", err)
	}
	zones, err := normalizer.LoadZones(filepath.Join(*dataDir, "config", "zones.json"))
	if err != nil {
		log.Fatalf("zonas: %v", err)
	}
	norm := normalizer.New(cat, lex, normalizer.Options{
		ActiveZones:       zones,
		DefaultDepartment: os.Getenv("DEFAULT_DEPARTMENT"),
		DefaultProvince:   os.Getenv("DEFAULT_PROVINCE"),
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok", "ubigeos": cat.Size(), "version": norm.VersionString(),
		})
	})
	mux.HandleFunc("POST /v1/normalize", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var req normalizer.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON inválido"})
			return
		}
		start := time.Now()
		res := norm.Normalize(req)
		writeJSON(w, http.StatusOK, normalizeResponse{Result: res, ProcessingUS: time.Since(start).Microseconds()})
	})

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	log.Printf("escuchando en %s (%s, %d ubigeos)", *addr, norm.VersionString(), cat.Size())
	log.Fatal(srv.ListenAndServe())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
