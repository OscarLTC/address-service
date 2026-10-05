// Comando resolver: plano de datos. Expone /v1/normalize, /v1/geocode y
// /v1/geocode/batch (si hay snapshot), más /healthz, /readyz y /metrics. Responde
// solo desde memoria: no consulta la base de datos por request.
//
//	go run ./cmd/resolver -snapshot data/snapshot/lima.snap
//	go run ./cmd/resolver -hash-key <api-key>    # hash para el archivo de API keys
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"addrsvc/internal/api"
	"addrsvc/internal/catalog"
	"addrsvc/internal/geo"
	"addrsvc/internal/normalizer"
	"addrsvc/internal/resolver"
	"addrsvc/internal/snapshot"
)

func main() {
	addr := flag.String("addr", ":8080", "dirección de escucha")
	dataDir := flag.String("data", "data", "directorio de datos (catálogo, reglas, zonas, geo)")
	snapPath := flag.String("snapshot", "data/snapshot/lima.snap", "snapshot del resolver (sin él, /v1/geocode responde 503)")
	keysPath := flag.String("api-keys", os.Getenv("API_KEYS_FILE"), "JSON {sha256(api key): cliente}; vacío = sin autenticación (solo desarrollo)")
	rate := flag.Float64("rate", 0, "solicitudes por segundo por cliente (0 = sin límite)")
	maxBatch := flag.Int("max-batch", 5000, "máximo de direcciones por lote")
	eventsPath := flag.String("events", "", "archivo NDJSON de eventos de resolución (vacío = no se registran)")
	hashKey := flag.String("hash-key", "", "imprime el hash de una API key y termina")
	flag.Parse()

	if *hashKey != "" {
		fmt.Println(api.HashKey(*hashKey))
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	fatal := func(msg string, err error) {
		logger.Error(msg, "error", err)
		os.Exit(1)
	}

	cat, err := catalog.Load(filepath.Join(*dataDir, "catalog", "ubigeos.json"))
	if err != nil {
		fatal("catálogo", err)
	}
	lex, err := normalizer.LoadLexicon(filepath.Join(*dataDir, "rules", "lexicon.json"))
	if err != nil {
		fatal("léxico", err)
	}
	zones, err := normalizer.LoadZones(filepath.Join(*dataDir, "config", "zones.json"))
	if err != nil {
		fatal("zonas", err)
	}
	norm := normalizer.New(cat, lex, normalizer.Options{
		ActiveZones:       zones,
		DefaultDepartment: os.Getenv("DEFAULT_DEPARTMENT"),
		DefaultProvince:   os.Getenv("DEFAULT_PROVINCE"),
	})

	cfg := api.Config{Normalizer: norm, RatePerSecond: *rate, MaxBatch: *maxBatch, Logger: logger}
	if snap, err := snapshot.Load(*snapPath); err != nil {
		logger.Warn("sin snapshot: /v1/geocode responde 503", "error", err)
	} else {
		districts, err := geo.LoadDistricts(filepath.Join(*dataDir, "geo", "districts.json"))
		if err != nil {
			fatal("límites", err)
		}
		cfg.Resolver = resolver.New(norm, snap, districts.Centroids())
		logger.Info("snapshot cargado", "version", snap.Version, "calles", len(snap.Streets))
	}
	if *keysPath != "" {
		data, err := os.ReadFile(*keysPath)
		if err != nil {
			fatal("api keys", err)
		}
		if err := json.Unmarshal(data, &cfg.KeyHashes); err != nil {
			fatal("api keys", err)
		}
		logger.Info("autenticación activa", "clientes", len(cfg.KeyHashes))
	} else {
		logger.Warn("sin API keys: autenticación desactivada (solo desarrollo)")
	}
	if *eventsPath != "" {
		cfg.Events, err = api.NewEventBuffer(*eventsPath, 10000)
		if err != nil {
			fatal("eventos", err)
		}
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           api.New(cfg).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
	}
	go func() {
		logger.Info("escuchando", "addr", *addr, "version", norm.VersionString())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal("servidor", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	cfg.Events.Close()
	logger.Info("detenido", "eventos_descartados", cfg.Events.Dropped())
}
