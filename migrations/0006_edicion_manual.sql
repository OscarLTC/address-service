-- Edición manual sobre el mapa: lo que un operador sabe de una calle y los datos
-- abiertos no traen. Nada se borra: quitar algo es marcar deleted_at.
--
-- No hay llave foránea a streets: cmd/dbload reemplaza las calles de OSM en cada
-- carga y los ids son estables entre versiones (docs/runbook.md).

-- Cuadra numerada a mano: la cuadra de la calle que pasa por el punto lleva esa
-- centena ("cuadra 12" = números del 1200 al 1299).
CREATE TABLE IF NOT EXISTS block_marks (
    id          bigserial PRIMARY KEY,
    street_id   bigint NOT NULL,
    hundred     integer NOT NULL CHECK (hundred BETWEEN 0 AND 999),
    location    geometry(Point, 4326) NOT NULL,
    created_by  text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz
);
CREATE INDEX IF NOT EXISTS block_marks_street_idx ON block_marks (street_id) WHERE deleted_at IS NULL;

-- Número de puerta puesto a mano sobre una calle.
CREATE TABLE IF NOT EXISTS manual_anchors (
    id            bigserial PRIMARY KEY,
    street_id     bigint NOT NULL,
    house_number  integer NOT NULL CHECK (house_number BETWEEN 1 AND 99999),
    location      geometry(Point, 4326) NOT NULL,
    created_by    text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    deleted_at    timestamptz
);
CREATE INDEX IF NOT EXISTS manual_anchors_street_idx ON manual_anchors (street_id) WHERE deleted_at IS NULL;

-- Calle trazada a mano (no está en OSM o está incompleta). Entra al snapshot
-- siguiente; si toca a una calle del mismo nombre, la extiende.
CREATE TABLE IF NOT EXISTS manual_streets (
    id           bigserial PRIMARY KEY,
    ubigeo_code  char(6) NOT NULL,
    street_type  text NOT NULL DEFAULT '',
    name         text NOT NULL,
    geom         geometry(LineString, 4326) NOT NULL,
    created_by   text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz
);
CREATE INDEX IF NOT EXISTS manual_streets_ubigeo_idx ON manual_streets (ubigeo_code) WHERE deleted_at IS NULL;
