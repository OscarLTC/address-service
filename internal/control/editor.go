package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"addrsvc/internal/geo"
	"addrsvc/internal/resolver"
	"addrsvc/internal/snapshot"
)

// Edición manual sobre el mapa (migrations/0006): cuadras numeradas, números de
// puerta y calles trazadas por operadores. Las cuadras y los números se usan de
// inmediato en las sugerencias del admin; todo entra al resolver con el snapshot
// siguiente (cmd/snapshotbuild -db).

// maxEditFromStreet es la mayor distancia entre una marca o un número y su calle.
const maxEditFromStreet = 60.0

// StreetEdits son las ediciones vigentes de una calle.
type StreetEdits struct {
	Anchors []snapshot.Anchor    // números puestos a mano y pines de operadores
	Marks   []snapshot.BlockMark // cuadras numeradas a mano
	// MarkIDs y AnchorIDs permiten quitar una edición: van en el mismo orden que
	// las marcas y que los números puestos a mano (los primeros de Anchors).
	MarkIDs   []int64
	AnchorIDs []int64
}

// Edits devuelve las ediciones manuales de cada calle junto con los pines de
// operadores que tienen número de puerta.
func (s *Store) Edits(ctx context.Context, streetIDs []int64) (map[int64]*StreetEdits, error) {
	out := map[int64]*StreetEdits{}
	if len(streetIDs) == 0 {
		return out, nil
	}
	get := func(id int64) *StreetEdits {
		if out[id] == nil {
			out[id] = &StreetEdits{}
		}
		return out[id]
	}
	rows, err := s.db.Query(ctx, `SELECT id, street_id, house_number, ST_X(location), ST_Y(location)
		FROM manual_anchors WHERE street_id = ANY($1) AND deleted_at IS NULL ORDER BY id`, streetIDs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, streetID int64
		var number int
		var lng, lat float64
		if err := rows.Scan(&id, &streetID, &number, &lng, &lat); err != nil {
			rows.Close()
			return nil, err
		}
		e := get(streetID)
		e.Anchors = append(e.Anchors, snapshot.Anchor{Number: number, Point: geo.Point{lng, lat}, Ref: fmt.Sprintf("manual:%d", id)})
		e.AnchorIDs = append(e.AnchorIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = s.db.Query(ctx, `SELECT id, street_id, hundred, ST_X(location), ST_Y(location)
		FROM block_marks WHERE street_id = ANY($1) AND deleted_at IS NULL ORDER BY id`, streetIDs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, streetID int64
		var hundred int
		var lng, lat float64
		if err := rows.Scan(&id, &streetID, &hundred, &lng, &lat); err != nil {
			rows.Close()
			return nil, err
		}
		e := get(streetID)
		e.Marks = append(e.Marks, snapshot.BlockMark{Hundred: hundred, Point: geo.Point{lng, lat}})
		e.MarkIDs = append(e.MarkIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	pins, err := s.StreetPins(ctx, streetIDs)
	if err != nil {
		return nil, err
	}
	for id, ps := range pins {
		e := get(id)
		manual := map[int]bool{}
		for _, a := range e.Anchors {
			manual[a.Number] = true
		}
		for _, p := range ps {
			if !manual[p.Number] { // el número puesto a mano en el editor manda
				e.Anchors = append(e.Anchors, p)
			}
		}
	}
	return out, nil
}

// nearStreet valida que el punto esté sobre la calle indicada.
func (s *Store) nearStreet(ctx context.Context, streetID int64, lng, lat float64) error {
	var d *float64
	if err := s.db.QueryRow(ctx, `SELECT min(ST_Distance(geom::geography, ST_SetSRID(ST_MakePoint($2,$3),4326)::geography))
		FROM street_segments WHERE street_id = $1`, streetID, lng, lat).Scan(&d); err != nil {
		return err
	}
	if d == nil {
		return fmt.Errorf("la calle %d no existe en la base", streetID)
	}
	if *d > maxEditFromStreet {
		return fmt.Errorf("el punto está a %.0f m de la calle: debe estar a %.0f m o menos", *d, maxEditFromStreet)
	}
	return nil
}

// AddBlockMark guarda la centena de la cuadra que pasa por el punto.
func (s *Store) AddBlockMark(ctx context.Context, streetID int64, hundred int, lng, lat float64, actor string) (int64, error) {
	if hundred < 0 || hundred > 999 {
		return 0, errors.New("la cuadra debe estar entre 0 y 999")
	}
	if err := s.nearStreet(ctx, streetID, lng, lat); err != nil {
		return 0, err
	}
	var id int64
	err := s.db.QueryRow(ctx, `INSERT INTO block_marks (street_id, hundred, location, created_by)
		VALUES ($1, $2, ST_SetSRID(ST_MakePoint($3,$4),4326), $5) RETURNING id`, streetID, hundred, lng, lat, actor).Scan(&id)
	return id, err
}

// AddManualAnchor guarda un número de puerta sobre una calle.
func (s *Store) AddManualAnchor(ctx context.Context, streetID int64, number int, lng, lat float64, actor string) (int64, error) {
	if number < 1 || number > 99999 {
		return 0, errors.New("el número de puerta debe estar entre 1 y 99999")
	}
	if err := s.nearStreet(ctx, streetID, lng, lat); err != nil {
		return 0, err
	}
	var id int64
	err := s.db.QueryRow(ctx, `INSERT INTO manual_anchors (street_id, house_number, location, created_by)
		VALUES ($1, $2, ST_SetSRID(ST_MakePoint($3,$4),4326), $5) RETURNING id`, streetID, number, lng, lat, actor).Scan(&id)
	return id, err
}

// AddManualStreet guarda una calle trazada a mano. Los puntos son [lng, lat].
func (s *Store) AddManualStreet(ctx context.Context, ubigeo, streetType, name string, line [][2]float64, actor string) (int64, error) {
	if len(line) < 2 {
		return 0, errors.New("la calle necesita al menos dos puntos")
	}
	if strings.TrimSpace(name) == "" {
		return 0, errors.New("la calle necesita un nombre")
	}
	pts := make([]string, len(line))
	for i, p := range line {
		pts[i] = strconv.FormatFloat(p[0], 'f', 7, 64) + " " + strconv.FormatFloat(p[1], 'f', 7, 64)
	}
	wkt := "LINESTRING(" + strings.Join(pts, ",") + ")"
	var inside bool
	if err := s.db.QueryRow(ctx, `SELECT coalesce(bool_or(ST_Intersects(polygon, ST_GeomFromText($2, 4326))), false)
		FROM ubigeos WHERE code = $1`, ubigeo, wkt).Scan(&inside); err != nil {
		return 0, err
	}
	if !inside {
		return 0, errors.New("la calle trazada no toca el distrito indicado")
	}
	var id int64
	err := s.db.QueryRow(ctx, `INSERT INTO manual_streets (ubigeo_code, street_type, name, geom, created_by)
		VALUES ($1, $2, $3, ST_GeomFromText($4, 4326), $5) RETURNING id`,
		ubigeo, strings.ToUpper(strings.TrimSpace(streetType)), strings.TrimSpace(name), wkt, actor).Scan(&id)
	return id, err
}

// ManualStreet es una calle trazada a mano, vigente.
type ManualStreet struct {
	ID        int64           `json:"id"`
	Type      string          `json:"street_type"`
	Name      string          `json:"name"`
	CreatedBy string          `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
	Geometry  json.RawMessage `json:"geometry"` // GeoJSON LineString
}

// ManualStreets devuelve las calles trazadas a mano en un distrito. Se ven en el
// mapa del editor apenas se guardan, antes de entrar al snapshot.
func (s *Store) ManualStreets(ctx context.Context, ubigeo string) ([]ManualStreet, error) {
	rows, err := s.db.Query(ctx, `SELECT id, street_type, name, created_by, created_at, ST_AsGeoJSON(geom, 7)::json
		FROM manual_streets WHERE ubigeo_code = $1 AND deleted_at IS NULL ORDER BY id`, ubigeo)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ManualStreet, error) {
		var m ManualStreet
		err := r.Scan(&m.ID, &m.Type, &m.Name, &m.CreatedBy, &m.CreatedAt, &m.Geometry)
		return m, err
	})
}

// RemoveEdit quita una edición manual (no la borra: queda con deleted_at).
func (s *Store) RemoveEdit(ctx context.Context, kind string, id int64) error {
	table := map[string]string{"marks": "block_marks", "anchors": "manual_anchors", "streets": "manual_streets"}[kind]
	if table == "" {
		return errors.New("tipo de edición desconocido")
	}
	tag, err := s.db.Exec(ctx, `UPDATE `+table+` SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return errors.New("no existe o ya se quitó")
	}
	return err
}

// editorRoutes registra las rutas del editor de calles.
func (s *Server) editorRoutes(mux *http.ServeMux) {
	mux.Handle("GET /admin/api/streets", s.auth(http.HandlerFunc(s.findStreets)))
	mux.Handle("GET /admin/api/districts/{ubigeo}", s.auth(http.HandlerFunc(s.districtOverview)))
	mux.Handle("GET /admin/api/districts/{ubigeo}/drawn", s.auth(http.HandlerFunc(s.drawnStreets)))
	mux.Handle("POST /admin/api/streets", s.auth(http.HandlerFunc(s.drawStreet)))
	mux.Handle("GET /admin/api/streets/{id}", s.auth(http.HandlerFunc(s.streetDetail)))
	mux.Handle("POST /admin/api/streets/{id}/marks", s.auth(http.HandlerFunc(s.addEdit)))
	mux.Handle("POST /admin/api/streets/{id}/anchors", s.auth(http.HandlerFunc(s.addEdit)))
	mux.Handle("DELETE /admin/api/edits/{kind}/{id}", s.auth(http.HandlerFunc(s.removeEdit)))
}

func (s *Server) findStreets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if len(q.Get("ubigeo")) != 6 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "falta el ubigeo del distrito (6 dígitos)"})
		return
	}
	writeJSON(w, http.StatusOK, s.resolver.FindStreets(q.Get("ubigeo"), q.Get("q"), 200))
}

// districtOverview devuelve todas las calles del distrito con lo que ya se sabe de
// cada una, para pintarlas en el mapa del editor.
func (s *Server) districtOverview(w http.ResponseWriter, r *http.Request) {
	ubigeo := r.PathValue("ubigeo")
	var ids []int64
	for _, st := range s.resolver.FindStreets(ubigeo, "", 1<<30) {
		ids = append(ids, int64(st.ID))
	}
	if len(ids) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "el snapshot no tiene calles de ese distrito"})
		return
	}
	edits, err := s.store.Edits(r.Context(), ids)
	if err != nil {
		s.logger.Error("ediciones", "distrito", ubigeo, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no se pudieron leer las ediciones"})
		return
	}
	streets := s.resolver.Overview(ubigeo, func(id int) ([]snapshot.Anchor, []snapshot.BlockMark) {
		if e := edits[int64(id)]; e != nil {
			return e.Anchors, e.Marks
		}
		return nil, nil
	})
	summary := map[string]int{}
	for _, st := range streets {
		summary[st.Coverage]++
	}
	writeJSON(w, http.StatusOK, map[string]any{"ubigeo": ubigeo, "summary": summary, "streets": streets})
}

// editedStreet es el detalle de una calle con los ids de sus ediciones.
type editedStreet struct {
	resolver.StreetDetail
	MarkPoints    []editPoint `json:"mark_points"`
	ManualAnchors []editPoint `json:"manual_anchors"`
}

type editPoint struct {
	ID    int64   `json:"id"`
	Value int     `json:"value"` // la centena de la marca o el número de puerta
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
}

func (s *Server) streetDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id inválido"})
		return
	}
	edits, err := s.store.Edits(r.Context(), []int64{id})
	if err != nil {
		s.logger.Error("ediciones", "calle", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no se pudieron leer las ediciones"})
		return
	}
	e := edits[id]
	if e == nil {
		e = &StreetEdits{}
	}
	d, ok := s.resolver.Detail(int(id), e.Anchors, e.Marks)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "la calle no está en el snapshot del admin"})
		return
	}
	out := editedStreet{StreetDetail: d, MarkPoints: []editPoint{}, ManualAnchors: []editPoint{}}
	for i, mid := range e.MarkIDs {
		out.MarkPoints = append(out.MarkPoints, editPoint{ID: mid, Value: e.Marks[i].Hundred, Lat: e.Marks[i].Point[1], Lng: e.Marks[i].Point[0]})
	}
	for i, aid := range e.AnchorIDs {
		out.ManualAnchors = append(out.ManualAnchors, editPoint{ID: aid, Value: e.Anchors[i].Number, Lat: e.Anchors[i].Point[1], Lng: e.Anchors[i].Point[0]})
	}
	writeJSON(w, http.StatusOK, out)
}

// addEdit guarda una cuadra numerada (…/marks) o un número de puerta (…/anchors).
func (s *Server) addEdit(w http.ResponseWriter, r *http.Request) {
	streetID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id inválido"})
		return
	}
	var body struct {
		Lat   *float64 `json:"lat"`
		Lng   *float64 `json:"lng"`
		Value *int     `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil || body.Lat == nil || body.Lng == nil || body.Value == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "se necesitan lat, lng y value"})
		return
	}
	op := operatorOf(r)
	var id int64
	kind := "anchors"
	if strings.HasSuffix(r.URL.Path, "/marks") {
		kind = "marks"
		id, err = s.store.AddBlockMark(r.Context(), streetID, *body.Value, *body.Lng, *body.Lat, op.Name)
	} else {
		id, err = s.store.AddManualAnchor(r.Context(), streetID, *body.Value, *body.Lng, *body.Lat, op.Name)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.logger.Info("edición manual", "tipo", kind, "calle", streetID, "valor", *body.Value, "operador", op.Name)
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) drawStreet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Ubigeo string       `json:"ubigeo"`
		Type   string       `json:"street_type"`
		Name   string       `json:"name"`
		Line   [][2]float64 `json:"line"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON inválido"})
		return
	}
	op := operatorOf(r)
	id, err := s.store.AddManualStreet(r.Context(), body.Ubigeo, body.Type, body.Name, body.Line, op.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.logger.Info("edición manual", "tipo", "streets", "id", id, "operador", op.Name)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "note": "la calle entra al resolver con el snapshot siguiente"})
}

func (s *Server) drawnStreets(w http.ResponseWriter, r *http.Request) {
	ms, err := s.store.ManualStreets(r.Context(), r.PathValue("ubigeo"))
	if err != nil {
		s.logger.Error("calles trazadas", "distrito", r.PathValue("ubigeo"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no se pudieron leer las calles trazadas"})
		return
	}
	writeJSON(w, http.StatusOK, ms)
}

func (s *Server) removeEdit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id inválido"})
		return
	}
	if err := s.store.RemoveEdit(r.Context(), r.PathValue("kind"), id); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.logger.Info("edición manual quitada", "tipo", r.PathValue("kind"), "id", id, "operador", operatorOf(r).Name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
