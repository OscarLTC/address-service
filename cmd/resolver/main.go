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

// watchSnapshot recarga el snapshot cuando cambia el archivo (o con SIGHUP) y lo
// intercambia de forma atómica. Si la carga falla, sigue el snapshot anterior: para
// volver a una versión previa basta con restaurar el archivo anterior.
func watchSnapshot(path string, every time.Duration, load func() (*resolver.Resolver, error), server *api.Server, logger *slog.Logger) {
	modTime := func() time.Time {
		if fi, err := os.Stat(path); err == nil {
			return fi.ModTime()
		}
		return time.Time{}
	}
	last := modTime()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	var tick <-chan time.Time
	if every > 0 {
		t := time.NewTicker(every)
		defer t.Stop()
		tick = t.C
	}
	for {
		forced := false
		select {
		case <-tick:
		case <-hup:
			forced = true
		}
		mt := modTime()
		if !forced && (mt.IsZero() || mt.Equal(last)) {
			continue
		}
		res, err := load()
		if err != nil {
			logger.Error("recarga del snapshot fallida: se mantiene la versión anterior", "error", err)
			continue
		}
		last = mt
		prev := ""
		if old := server.Resolver(); old != nil {
			prev = old.Version()
		}
		server.SetResolver(res)
		logger.Info("snapshot recargado", "anterior", prev, "nuevo", res.Version())
	}
}

func main() {
	addr := flag.String("addr", ":8080", "dirección de escucha")
	dataDir := flag.String("data", "data", "directorio de datos (catálogo, reglas, zonas, geo)")
	snapPath := flag.String("snapshot", "data/snapshot/lima.snap", "snapshot del resolver (sin él, /v1/geocode responde 503)")
	keysPath := flag.String("api-keys", os.Getenv("API_KEYS_FILE"), "JSON {sha256(api key): cliente}; vacío = sin autenticación (solo desarrollo)")
	rate := flag.Float64("rate", 0, "solicitudes por segundo por cliente (0 = sin límite)")
	maxBatch := flag.Int("max-batch", 5000, "máximo de direcciones por lote")
	eventsPath := flag.String("events", "", "archivo NDJSON de eventos de resolución (vacío = no se registran)")
	hashKey := flag.String("hash-key", "", "imprime el hash de una API key y termina")
	reloadEvery := flag.Duration("reload-every", 30*time.Second, "cada cuánto revisar si cambió el snapshot (0 = solo con SIGHUP)")
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
	districts, err := geo.LoadDistricts(filepath.Join(*dataDir, "geo", "districts.json"))
	if err != nil {
		fatal("límites", err)
	}
	centroids := districts.Centroids()
	load := func() (*resolver.Resolver, error) {
		snap, err := snapshot.Load(*snapPath)
		if err != nil {
			return nil, err
		}
		logger.Info("snapshot cargado", "version", snap.Version, "calles", len(snap.Streets), "verificadas", len(snap.Verified))
		return resolver.New(norm, snap, centroids), nil
	}
	if res, err := load(); err != nil {
		logger.Warn("sin snapshot: /v1/geocode responde 503", "error", err)
	} else {
		cfg.Resolver = res
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

	server := api.New(cfg)
	go watchSnapshot(*snapPath, *reloadEvery, load, server, logger)
	srv := &http.Server{
		Addr:              *addr,
		Handler:           server.Handler(),
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
