// Package control implementa el plano de control: la cola de revisión manual sobre
// PostgreSQL + PostGIS. Cada decisión de un operador queda como una observación
// inmutable (pin_operador) y un evento de auditoría con el antes y el después.
package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Umbrales de las alertas al guardar un pin (docs/plan.md, F3).
const pinFarFromStreet = 100.0 // metros

// Store accede a la base del plano de control.
type Store struct {
	db *pgxpool.Pool
}

// NewStore abre un pool de conexiones.
func NewStore(ctx context.Context, dsn string) (*Store, error) {
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// SnapshotVersion devuelve la versión de snapshot cargada en la base (cmd/dbload).
func (s *Store) SnapshotVersion(ctx context.Context) (string, error) {
	var v *string
	err := s.db.QueryRow(ctx, `SELECT max(snapshot_version) FROM coverage_zones`).Scan(&v)
	if v == nil {
		return "", err
	}
	return *v, err
}

// Close cierra el pool.
func (s *Store) Close() { s.db.Close() }

// NewTicket es una dirección que el resolver mandó a revisión.
type NewTicket struct {
	AddressKey string
	Ubigeo     string
	Raw        any // entrada original
	Parse      any // resultado del normalizador
	Candidates any // calles candidatas del resolver
	Source     string
}

// UpsertTicket crea el ticket o, si ya hay uno abierto para la misma dirección, suma
// una ocurrencia y sube su prioridad. Devuelve el id y si fue nuevo.
func (s *Store) UpsertTicket(ctx context.Context, t NewTicket) (int64, bool, error) {
	raw, _ := json.Marshal(t.Raw)
	parse, _ := json.Marshal(t.Parse)
	cands, _ := json.Marshal(t.Candidates)
	var ubigeo any
	if t.Ubigeo != "" {
		ubigeo = t.Ubigeo
	}
	var id int64
	var inserted bool
	err := s.db.QueryRow(ctx, `
		INSERT INTO review_tickets (raw_input, parse, candidates, priority, ubigeo_code, address_key, source)
		VALUES ($1, $2, $3, 1, $4, $5, $6)
		ON CONFLICT (address_key) WHERE status IN ('open','assigned','escalated')
		DO UPDATE SET occurrences = review_tickets.occurrences + 1, priority = review_tickets.priority + 1
		RETURNING id, (xmax = 0)`, raw, parse, cands, ubigeo, t.AddressKey, t.Source).Scan(&id, &inserted)
	return id, inserted, err
}

// TicketSummary es una fila de la cola.
type TicketSummary struct {
	ID          int64     `json:"id"`
	Status      string    `json:"status"`
	Priority    int       `json:"priority"`
	Occurrences int       `json:"occurrences"`
	Address     string    `json:"address"`
	Normalized  string    `json:"normalized"`
	Ubigeo      string    `json:"ubigeo"`
	District    string    `json:"district"`
	Reason      string    `json:"reason"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListTickets devuelve la cola ordenada por prioridad (frecuencia) y antigüedad.
func (s *Store) ListTickets(ctx context.Context, status string, limit int) ([]TicketSummary, error) {
	rows, err := s.db.Query(ctx, `
		SELECT t.id, t.status, t.priority, t.occurrences, coalesce(t.raw_input->>'address',''),
		       coalesce(t.parse->>'normalized',''), coalesce(t.ubigeo_code,''), coalesce(u.district,''),
		       coalesce(t.candidates->>'reason',''), t.created_at
		FROM review_tickets t LEFT JOIN ubigeos u ON u.code = t.ubigeo_code
		WHERE t.status = $1
		ORDER BY t.priority DESC, t.created_at
		LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (TicketSummary, error) {
		var t TicketSummary
		err := r.Scan(&t.ID, &t.Status, &t.Priority, &t.Occurrences, &t.Address, &t.Normalized, &t.Ubigeo, &t.District, &t.Reason, &t.CreatedAt)
		return t, err
	})
}

// TicketDetail es un ticket con la geometría que necesita el mapa.
type TicketDetail struct {
	ID          int64           `json:"id"`
	Status      string          `json:"status"`
	Priority    int             `json:"priority"`
	Occurrences int             `json:"occurrences"`
	Ubigeo      string          `json:"ubigeo"`
	District    string          `json:"district"`
	Raw         json.RawMessage `json:"raw_input"`
	Parse       json.RawMessage `json:"parse"`
	Candidates  json.RawMessage `json:"candidates"`
	Resolution  json.RawMessage `json:"resolution,omitempty"`
	// DistrictGeoJSON es el polígono del distrito; StreetsGeoJSON, las calles
	// candidatas como FeatureCollection (propiedad street_id).
	DistrictGeoJSON json.RawMessage `json:"district_geojson,omitempty"`
	StreetsGeoJSON  json.RawMessage `json:"streets_geojson,omitempty"`
}

// GetTicket devuelve un ticket con su geometría.
func (s *Store) GetTicket(ctx context.Context, id int64) (*TicketDetail, error) {
	var t TicketDetail
	err := s.db.QueryRow(ctx, `
		SELECT t.id, t.status, t.priority, t.occurrences, coalesce(t.ubigeo_code,''), coalesce(u.district,''),
		       t.raw_input, t.parse, coalesce(t.candidates,'null'), t.resolution, ST_AsGeoJSON(u.polygon, 6)::json
		FROM review_tickets t LEFT JOIN ubigeos u ON u.code = t.ubigeo_code
		WHERE t.id = $1`, id).Scan(&t.ID, &t.Status, &t.Priority, &t.Occurrences, &t.Ubigeo, &t.District,
		&t.Raw, &t.Parse, &t.Candidates, &t.Resolution, &t.DistrictGeoJSON)
	if err != nil {
		return nil, err
	}
	var cands struct {
		Candidates []struct {
			StreetID int64 `json:"street_id"`
		} `json:"candidates"`
	}
	_ = json.Unmarshal(t.Candidates, &cands)
	ids := []int64{}
	for _, c := range cands.Candidates {
		ids = append(ids, c.StreetID)
	}
	err = s.db.QueryRow(ctx, `
		SELECT json_build_object('type','FeatureCollection','features', coalesce(json_agg(json_build_object(
			'type','Feature','properties', json_build_object('street_id', st.id, 'name', trim(st.street_type||' '||st.canonical_name)),
			'geometry', ST_AsGeoJSON(sg.geom, 6)::json)), '[]'::json))
		FROM streets st JOIN street_segments sg ON sg.street_id = st.id
		WHERE st.id = ANY($1)`, ids).Scan(&t.StreetsGeoJSON)
	return &t, err
}

// Resolution es lo que decide un operador sobre un ticket.
type Resolution struct {
	Action   string   `json:"action"` // pin | unresolvable | escalate
	Lat      *float64 `json:"lat,omitempty"`
	Lng      *float64 `json:"lng,omitempty"`
	StreetID *int64   `json:"street_id,omitempty"`
	Note     string   `json:"note,omitempty"`
	// Confirm acepta guardar a pesar de las alertas.
	Confirm bool `json:"confirm,omitempty"`
}

// ErrWarnings indica que el pin tiene alertas y no se confirmó.
type ErrWarnings struct{ Warnings []string }

func (e *ErrWarnings) Error() string { return fmt.Sprintf("alertas sin confirmar: %v", e.Warnings) }

// ErrClosed indica que el ticket ya no está abierto.
var ErrClosed = errors.New("el ticket ya está cerrado")

// Resolve aplica la decisión de un operador en una transacción: valida, guarda la
// observación (si hay pin), la auditoría y el nuevo estado del ticket.
func (s *Store) Resolve(ctx context.Context, id int64, actor string, r Resolution) ([]string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var status, ubigeo, key string
	var before, parse json.RawMessage
	err = tx.QueryRow(ctx, `
		SELECT status, coalesce(ubigeo_code,''), coalesce(address_key,''), to_jsonb(t), parse
		FROM review_tickets t WHERE id = $1 FOR UPDATE`, id).Scan(&status, &ubigeo, &key, &before, &parse)
	if err != nil {
		return nil, err
	}
	if status == "resolved" || status == "unresolvable" {
		return nil, ErrClosed
	}

	var warnings []string
	newStatus := ""
	switch r.Action {
	case "pin":
		if r.Lat == nil || r.Lng == nil {
			return nil, errors.New("el pin necesita lat y lng")
		}
		if ubigeo != "" {
			var inside bool
			if err := tx.QueryRow(ctx, `SELECT coalesce(ST_Contains(polygon, ST_SetSRID(ST_MakePoint($2,$3),4326)), true)
				FROM ubigeos WHERE code = $1`, ubigeo, *r.Lng, *r.Lat).Scan(&inside); err != nil {
				return nil, err
			}
			if !inside {
				warnings = append(warnings, "PIN_OUTSIDE_DISTRICT")
			}
		}
		if r.StreetID != nil {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM streets WHERE id = $1)`, *r.StreetID).Scan(&exists); err != nil {
				return nil, err
			}
			if !exists {
				return nil, fmt.Errorf("la calle %d no existe en la base: ¿el snapshot del admin coincide con el cargado por dbload?", *r.StreetID)
			}
			var d float64
			if err := tx.QueryRow(ctx, `SELECT coalesce(min(ST_Distance(geom::geography, ST_SetSRID(ST_MakePoint($2,$3),4326)::geography)), 1e9)
				FROM street_segments WHERE street_id = $1`, *r.StreetID, *r.Lng, *r.Lat).Scan(&d); err != nil {
				return nil, err
			}
			if d > pinFarFromStreet {
				warnings = append(warnings, fmt.Sprintf("PIN_FAR_FROM_STREET (%.0f m)", d))
			}
		}
		if len(warnings) > 0 && !r.Confirm {
			return warnings, &ErrWarnings{warnings}
		}
		var p struct {
			Normalized string `json:"normalized"`
			Version    string `json:"normalizer_version"`
			Components struct {
				Number       string `json:"number"`
				Block        string `json:"block"`
				Lot          string `json:"lot"`
				Urbanization string `json:"urbanization"`
			} `json:"components"`
		}
		_ = json.Unmarshal(parse, &p)
		sum := sha256.Sum256([]byte(key))
		var ubi any
		if ubigeo != "" {
			ubi = ubigeo
		}
		var addrID int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO canonical_addresses (address_hash, street_id, ubigeo_code, house_number, block, lot, urbanization, normalized_address, normalizer_version)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (address_hash, normalizer_version) DO UPDATE SET street_id = coalesce(EXCLUDED.street_id, canonical_addresses.street_id)
			RETURNING id`, hex.EncodeToString(sum[:]), r.StreetID, ubi, nullable(p.Components.Number), nullable(p.Components.Block),
			nullable(p.Components.Lot), nullable(p.Components.Urbanization), p.Normalized, p.Version).Scan(&addrID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO observations (canonical_address_id, location, method, source_quality, author, source_ref)
			VALUES ($1, ST_SetSRID(ST_MakePoint($2,$3),4326), 'pin_operador', 'oro', $4, $5)`,
			addrID, *r.Lng, *r.Lat, actor, fmt.Sprintf("ticket:%d", id)); err != nil {
			return nil, err
		}
		newStatus = "resolved"
	case "unresolvable":
		if r.Note == "" {
			return nil, errors.New("marcar como irresoluble requiere un motivo")
		}
		newStatus = "unresolvable"
	case "escalate":
		newStatus = "escalated"
	default:
		return nil, fmt.Errorf("acción desconocida %q", r.Action)
	}

	resolution, _ := json.Marshal(map[string]any{"action": r.Action, "lat": r.Lat, "lng": r.Lng, "street_id": r.StreetID,
		"note": r.Note, "warnings": warnings, "actor": actor})
	if _, err := tx.Exec(ctx, `UPDATE review_tickets SET status = $2, resolution = $3, assigned_to = $4,
		resolved_at = CASE WHEN $2 IN ('resolved','unresolvable') THEN now() END WHERE id = $1`,
		id, newStatus, resolution, actor); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO decision_events (ticket_id, actor, action, before, after)
		VALUES ($1, $2, $3, $4, (SELECT to_jsonb(t) FROM review_tickets t WHERE id = $1))`,
		id, actor, r.Action, before); err != nil {
		return nil, err
	}
	return warnings, tx.Commit(ctx)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
