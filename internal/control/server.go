package control

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"addrsvc/internal/normalizer"
	"addrsvc/internal/resolver"
)

//go:embed ui
var uiFiles embed.FS

// Operator es una persona autenticada en el admin.
type Operator struct {
	Name string `json:"name"`
	Role string `json:"role"` // operador | revisor | administrador
}

// Server expone la API y la pantalla del admin.
type Server struct {
	store    *Store
	resolver *resolver.Resolver
	tokens   map[string]Operator // sha256(token) -> operador
	logger   *slog.Logger
}

// NewServer crea el servidor del admin.
func NewServer(store *Store, res *resolver.Resolver, tokenHashes map[string]Operator, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{store: store, resolver: res, tokens: tokenHashes, logger: logger}
}

type ctxOperator struct{}

// Handler devuelve las rutas del admin.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	ui, _ := fs.Sub(uiFiles, "ui")
	mux.Handle("GET /admin/", http.StripPrefix("/admin/", http.FileServer(http.FS(ui))))
	mux.Handle("GET /admin/api/me", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, operatorOf(r))
	})))
	mux.Handle("POST /admin/api/import", s.auth(http.HandlerFunc(s.importCSV)))
	mux.Handle("GET /admin/api/tickets", s.auth(http.HandlerFunc(s.listTickets)))
	mux.Handle("GET /admin/api/tickets/{id}", s.auth(http.HandlerFunc(s.getTicket)))
	mux.Handle("POST /admin/api/tickets/{id}/resolve", s.auth(http.HandlerFunc(s.resolve)))
	return mux
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sum := sha256.Sum256([]byte(r.Header.Get("X-Admin-Token")))
		op, ok := s.tokens[hex.EncodeToString(sum[:])]
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token de operador inválido"})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxOperator{}, op)))
	})
}

func operatorOf(r *http.Request) Operator {
	op, _ := r.Context().Value(ctxOperator{}).(Operator)
	return op
}

// importCSV resuelve un CSV de direcciones y manda a la cola las que quedan en
// revisión. Columnas reconocidas: external_id, address (o direccion), number (o
// numero), reference, district (o distrito), province, department, ubigeo.
func (s *Server) importCSV(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	cr := csv.NewReader(r.Body)
	cr.FieldsPerRecord = -1
	head, err := cr.Read()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "CSV inválido"})
		return
	}
	col := map[string]int{}
	for i, h := range head {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	pick := func(rec []string, names ...string) string {
		for _, n := range names {
			if i, ok := col[n]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
		}
		return ""
	}
	if _, ok := col["address"]; !ok {
		if _, ok := col["direccion"]; !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "falta la columna address (o direccion)"})
			return
		}
	}
	op := operatorOf(r)
	stats := map[string]int{}
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "CSV inválido: " + err.Error()})
			return
		}
		req := normalizer.Request{
			Address: pick(rec, "address", "direccion"), Number: pick(rec, "number", "numero"),
			Reference: pick(rec, "reference", "referencia"), District: pick(rec, "district", "distrito"),
			Province: pick(rec, "province", "provincia"), Department: pick(rec, "department", "departamento"),
			Ubigeo: pick(rec, "ubigeo"),
		}
		stats["total"]++
		res := s.resolver.Geocode(req)
		stats[res.Decision]++
		if res.Decision != resolver.Review {
			continue
		}
		key := res.Normalized.MatchKey + "|" + res.Ubigeo
		if res.Ubigeo == "" {
			key += strings.ToUpper(req.District)
		}
		_, created, err := s.store.UpsertTicket(r.Context(), NewTicket{
			AddressKey: key, Ubigeo: res.Ubigeo,
			Raw:        map[string]any{"external_id": pick(rec, "external_id"), "address": req.Address, "number": req.Number, "reference": req.Reference, "district": req.District, "province": req.Province, "ubigeo": req.Ubigeo},
			Parse:      res.Normalized,
			Candidates: map[string]any{"reason": reasonOf(res), "candidates": res.Candidates, "suggested": res.Location, "precision": res.PrecisionLevel, "flags": res.Flags},
			Source:     "import:" + op.Name,
		})
		if err != nil {
			s.logger.Error("ticket", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no se pudo crear el ticket"})
			return
		}
		if created {
			stats["tickets_nuevos"]++
		} else {
			stats["tickets_repetidos"]++
		}
	}
	s.logger.Info("importación", "operador", op.Name, "total", stats["total"], "revision", stats[resolver.Review])
	writeJSON(w, http.StatusOK, stats)
}

func reasonOf(r resolver.Result) string {
	if r.Reason != "" {
		return r.Reason
	}
	for _, f := range r.Flags {
		switch f {
		case "AMBIGUOUS_STREET", "LONG_STREET_WITHOUT_ANCHOR", "DISTRICT_CONFLICT", "STREET_NOT_FOUND", "AMBIGUOUS_LIMA", "DUPLICATED_TEXT":
			return f
		}
	}
	return "REVIEW"
}

func (s *Server) listTickets(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "open"
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	ts, err := s.store.ListTickets(r.Context(), status, limit)
	if err != nil {
		s.logger.Error("cola", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no se pudo leer la cola"})
		return
	}
	writeJSON(w, http.StatusOK, ts)
}

func (s *Server) getTicket(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id inválido"})
		return
	}
	t, err := s.store.GetTicket(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no existe"})
		return
	}
	if err != nil {
		s.logger.Error("ticket", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no se pudo leer el ticket"})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) resolve(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id inválido"})
		return
	}
	var res Resolution
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&res); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON inválido"})
		return
	}
	op := operatorOf(r)
	warnings, err := s.store.Resolve(r.Context(), id, op.Name, res)
	var werr *ErrWarnings
	switch {
	case errors.As(err, &werr):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "el pin tiene alertas: confirma para guardar", "warnings": werr.Warnings})
	case errors.Is(err, ErrClosed):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, pgx.ErrNoRows):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no existe"})
	case err != nil:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		s.logger.Info("ticket resuelto", "ticket", id, "operador", op.Name, "accion", res.Action)
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "warnings": warnings})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// HashToken devuelve el sha256 en hex de un token de operador.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
