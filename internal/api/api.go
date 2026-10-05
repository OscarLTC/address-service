// Package api expone el plano de datos por HTTP: /v1/normalize, /v1/geocode,
// /v1/geocode/batch, más /healthz, /readyz y /metrics.
//
// Nada en el camino de una respuesta toca la red ni la base de datos: los eventos de
// resolución se encolan en un buffer asíncrono que nunca bloquea (si se llena, se
// descartan y se cuentan). Los logs no incluyen el texto de la dirección.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"addrsvc/internal/normalizer"
	"addrsvc/internal/resolver"
)

// Item es una dirección a resolver.
type Item struct {
	ExternalID string `json:"external_id,omitempty"`
	normalizer.Request
}

// GeocodeResponse es la respuesta de una dirección.
type GeocodeResponse struct {
	ExternalID string `json:"external_id,omitempty"`
	resolver.Result
	DatasetVersion string `json:"dataset_version"`
	ProcessingUS   int64  `json:"processing_us"`
}

type normalizeResponse struct {
	normalizer.Result
	ProcessingUS int64 `json:"processing_us"`
}

type batchRequest struct {
	Items []Item `json:"items"`
}

// Config configura el servidor.
type Config struct {
	Normalizer *normalizer.Normalizer
	Resolver   *resolver.Resolver // nil: /v1/geocode no está disponible
	// KeyHashes mapea sha256(hex) de cada API key al nombre del cliente. Vacío: sin
	// autenticación (solo para desarrollo local).
	KeyHashes map[string]string
	// RatePerSecond es el límite de solicitudes por cliente (0: sin límite).
	RatePerSecond float64
	MaxBatch      int
	Events        *EventBuffer // puede ser nil
	Logger        *slog.Logger
}

// Server es el handler HTTP del plano de datos.
type Server struct {
	cfg     Config
	metrics *Metrics
	limiter *limiter
	idem    *idemCache
}

// New crea el servidor.
func New(cfg Config) *Server {
	if cfg.MaxBatch == 0 {
		cfg.MaxBatch = 5000
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Server{cfg: cfg, metrics: NewMetrics(), limiter: newLimiter(cfg.RatePerSecond), idem: newIdemCache(256)}
}

// Handler devuelve el mux con todas las rutas.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": s.cfg.Normalizer.VersionString()})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if s.cfg.Resolver == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "sin snapshot"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "dataset_version": s.cfg.Resolver.Version()})
	})
	mux.HandleFunc("GET /metrics", s.metrics.ServeHTTP)
	mux.Handle("POST /v1/normalize", s.protect("normalize", http.HandlerFunc(s.normalize)))
	mux.Handle("POST /v1/geocode", s.protect("geocode", http.HandlerFunc(s.geocode)))
	mux.Handle("POST /v1/geocode/batch", s.protect("batch", http.HandlerFunc(s.batch)))
	return mux
}

type ctxClient struct{}

// clientOf devuelve el cliente autenticado del request.
func clientOf(r *http.Request) string {
	c, _ := r.Context().Value(ctxClient{}).(string)
	return c
}

// protect aplica API key, límite por cliente, métricas y log estructurado.
func (s *Server) protect(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		client := "anonimo"
		if len(s.cfg.KeyHashes) > 0 {
			sum := sha256.Sum256([]byte(r.Header.Get("X-API-Key")))
			c, ok := s.cfg.KeyHashes[hex.EncodeToString(sum[:])]
			if !ok {
				s.finish(w, r, route, client, http.StatusUnauthorized, start, map[string]string{"error": "API key inválida o ausente"})
				return
			}
			client = c
		}
		if !s.limiter.allow(client) {
			w.Header().Set("Retry-After", "1")
			s.finish(w, r, route, client, http.StatusTooManyRequests, start, map[string]string{"error": "límite de solicitudes excedido"})
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), ctxClient{}, client)))
		s.metrics.request(route, rec.status, time.Since(start))
		s.cfg.Logger.Info("request", "route", route, "client", client, "status", rec.status,
			"duration_ms", float64(time.Since(start).Microseconds())/1000)
	})
}

func (s *Server) finish(w http.ResponseWriter, _ *http.Request, route, client string, status int, start time.Time, body any) {
	writeJSON(w, status, body)
	s.metrics.request(route, status, time.Since(start))
	s.cfg.Logger.Info("request", "route", route, "client", client, "status", status)
}

func (s *Server) normalize(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req normalizer.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON inválido"})
		return
	}
	start := time.Now()
	res := s.cfg.Normalizer.Normalize(req)
	writeJSON(w, http.StatusOK, normalizeResponse{Result: res, ProcessingUS: time.Since(start).Microseconds()})
}

func (s *Server) geocode(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Resolver == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sin snapshot cargado"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var it Item
	if err := json.NewDecoder(r.Body).Decode(&it); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON inválido"})
		return
	}
	resp := s.resolve(it, clientOf(r))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) resolve(it Item, client string) GeocodeResponse {
	start := time.Now()
	res := s.cfg.Resolver.Geocode(it.Request)
	us := time.Since(start).Microseconds()
	s.metrics.decision(res.Decision, res.PrecisionLevel)
	s.cfg.Events.Push(Event{
		Time: start.UTC(), Client: client, ExternalID: it.ExternalID, Decision: res.Decision,
		ResolutionType: res.ResolutionType, PrecisionLevel: res.PrecisionLevel, Score: res.Score,
		ProcessingUS: us, DatasetVersion: s.cfg.Resolver.Version(), NormalizerVersion: res.Normalized.Version,
	})
	return GeocodeResponse{ExternalID: it.ExternalID, Result: res, DatasetVersion: s.cfg.Resolver.Version(), ProcessingUS: us}
}

// batch resuelve un lote y responde NDJSON en el orden de entrada. Las direcciones
// repetidas se resuelven una vez. Con Idempotency-Key, repetir la misma solicitud
// devuelve la misma respuesta sin recalcular.
func (s *Server) batch(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Resolver == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sin snapshot cargado"})
		return
	}
	client := clientOf(r)
	idemKey := r.Header.Get("Idempotency-Key")
	if idemKey != "" {
		if body, ok := s.idem.get(client + "|" + idemKey); ok {
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.Header().Set("Idempotent-Replay", "true")
			_, _ = w.Write(body)
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	var req batchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON inválido"})
		return
	}
	if len(req.Items) == 0 || len(req.Items) > s.cfg.MaxBatch {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "el lote debe tener entre 1 y " + strconv.Itoa(s.cfg.MaxBatch) + " direcciones"})
		return
	}

	// Deduplicación: misma dirección y ubicación se resuelven una vez.
	keyOf := func(it Item) string {
		q := it.Request
		return strings.Join([]string{q.Address, q.Number, q.District, q.Province, q.Department, q.Ubigeo}, "\x00")
	}
	unique := map[string]int{}
	var uniq []Item
	pos := make([]int, len(req.Items))
	for i, it := range req.Items {
		k := keyOf(it)
		j, ok := unique[k]
		if !ok {
			j = len(uniq)
			unique[k] = j
			uniq = append(uniq, it)
		}
		pos[i] = j
	}
	results := make([]GeocodeResponse, len(uniq))
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < runtime.GOMAXPROCS(0); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range next {
				results[j] = s.resolve(uniq[j], client)
			}
		}()
	}
	for j := range uniq {
		next <- j
	}
	close(next)
	wg.Wait()

	var out strings.Builder
	enc := json.NewEncoder(&out)
	for i, it := range req.Items {
		resp := results[pos[i]]
		resp.ExternalID = it.ExternalID
		_ = enc.Encode(resp)
	}
	body := []byte(out.String())
	if idemKey != "" {
		s.idem.put(client+"|"+idemKey, body)
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Batch-Unique", strconv.Itoa(len(uniq)))
	_, _ = w.Write(body)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// HashKey devuelve el sha256 en hex de una API key, como se guarda en la configuración.
func HashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
