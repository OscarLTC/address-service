// Comando resolver: plano de datos. Expone /v1/normalize y, si hay un snapshot,
// /v1/geocode. Responde solo desde memoria: no consulta la base de datos por request.
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
	"addrsvc/internal/geo"
	"addrsvc/internal/normalizer"
	"addrsvc/internal/resolver"
	"addrsvc/internal/snapshot"
)

type geocodeRequest struct {
	ExternalID string `json:"external_id,omitempty"`
	normalizer.Request
}

type geocodeResponse struct {
	ExternalID string `json:"external_id,omitempty"`
	resolver.Result
	DatasetVersion string `json:"dataset_version"`
	ProcessingUS   int64  `json:"processing_us"`
}

type normalizeResponse struct {
	normalizer.Result
	ProcessingUS int64 `json:"processing_us"`
}

func main() {
	addr := flag.String("addr", ":8080", "dirección de escucha")
	dataDir := flag.String("data", "data", "directorio de datos (catálogo, reglas, zonas)")
	snapPath := flag.String("snapshot", "data/snapshot/lima.snap", "snapshot del resolver (sin él, /v1/geocode no se expone)")
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

	if snap, err := snapshot.Load(*snapPath); err != nil {
		log.Printf("sin snapshot (%v): /v1/geocode no disponible", err)
	} else {
		districts, err := geo.LoadDistricts(filepath.Join(*dataDir, "geo", "districts.json"))
		if err != nil {
			log.Fatalf("límites: %v", err)
		}
		res := resolver.New(norm, snap, districts.Centroids())
		log.Printf("snapshot %s: %d calles", snap.Version, len(snap.Streets))
		mux.HandleFunc("POST /v1/geocode", func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			var req geocodeRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON inválido"})
				return
			}
			start := time.Now()
			out := res.Geocode(req.Request)
			writeJSON(w, http.StatusOK, geocodeResponse{ExternalID: req.ExternalID, Result: out,
				DatasetVersion: res.Version(), ProcessingUS: time.Since(start).Microseconds()})
		})
	}

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
