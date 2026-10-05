// Comando control: plano de control. Sirve el admin (cola de revisión con mapa) y su
// API sobre PostgreSQL + PostGIS. Usa el mismo snapshot que el resolver para
// resolver los CSV importados.
//
//	go run ./cmd/control -db postgres://addrsvc:addrsvc@localhost:55432/addrsvc -operators data/private/operators.json
//	go run ./cmd/control -hash-token <token>    # hash para el archivo de operadores
//
// El archivo de operadores es {"sha256(token)": {"name": "...", "role": "operador"}}.
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

	"addrsvc/internal/catalog"
	"addrsvc/internal/control"
	"addrsvc/internal/geo"
	"addrsvc/internal/normalizer"
	"addrsvc/internal/resolver"
	"addrsvc/internal/snapshot"
)

func main() {
	addr := flag.String("addr", ":8090", "dirección de escucha")
	dsn := flag.String("db", envOr("DATABASE_URL", "postgres://addrsvc:addrsvc@localhost:55432/addrsvc"), "conexión a PostgreSQL")
	dataDir := flag.String("data", "data", "directorio de datos")
	snapPath := flag.String("snapshot", "data/snapshot/lima.snap", "snapshot del resolver")
	opsPath := flag.String("operators", envOr("OPERATORS_FILE", "data/private/operators.json"), "archivo de operadores (hash de token -> nombre y rol)")
	hashToken := flag.String("hash-token", "", "imprime el hash de un token de operador y termina")
	flag.Parse()
	if *hashToken != "" {
		fmt.Println(control.HashToken(*hashToken))
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	fatal := func(msg string, err error) {
		logger.Error(msg, "error", err)
		os.Exit(1)
	}
	var ops map[string]control.Operator
	data, err := os.ReadFile(*opsPath)
	if err != nil {
		fatal("operadores", err)
	}
	if err := json.Unmarshal(data, &ops); err != nil || len(ops) == 0 {
		fatal("operadores", fmt.Errorf("archivo vacío o inválido: %v", err))
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
	districts, err := geo.LoadDistricts(filepath.Join(*dataDir, "geo", "districts.json"))
	if err != nil {
		fatal("límites", err)
	}
	snap, err := snapshot.Load(*snapPath)
	if err != nil {
		fatal("snapshot", err)
	}
	res := resolver.New(normalizer.New(cat, lex, normalizer.Options{ActiveZones: zones}), snap, districts.Centroids())

	ctx := context.Background()
	store, err := control.NewStore(ctx, *dsn)
	if err != nil {
		fatal("base de datos", err)
	}
	defer store.Close()
	if v, err := store.SnapshotVersion(ctx); err != nil || v != snap.Version {
		fatal("snapshot", fmt.Errorf("la base tiene el snapshot %q y el admin cargó %q: corre go run ./cmd/dbload con el mismo snapshot", v, snap.Version))
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           control.NewServer(store, res, ops, logger).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
	}
	go func() {
		logger.Info("admin escuchando", "addr", *addr, "url", "http://localhost"+*addr+"/admin/", "operadores", len(ops))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal("servidor", err)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	shutdown, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
