-- Esquema v0 del plano de control (docs/plan.md, Apéndice D).
-- PostgreSQL + PostGIS es la fuente de verdad: el resolver no lo consulta por
-- request; lee snapshots construidos a partir de aquí.

CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Catálogo nacional de ubigeos y límites (los polígonos solo donde existan).
CREATE TABLE ubigeos (
    code            char(6) PRIMARY KEY,
    department      text NOT NULL,
    province        text NOT NULL,
    district        text NOT NULL,
    normalized_name text NOT NULL,
    zone            text NOT NULL,
    polygon         geometry(MultiPolygon, 4326)
);
CREATE INDEX ubigeos_polygon_gix ON ubigeos USING gist (polygon);

CREATE TABLE ubigeo_aliases (
    ubigeo_code      char(6) NOT NULL REFERENCES ubigeos(code),
    alias            text NOT NULL,
    normalized_alias text NOT NULL,
    weak             boolean NOT NULL DEFAULT false,
    PRIMARY KEY (ubigeo_code, normalized_alias)
);

CREATE TABLE coverage_zones (
    name                text PRIMARY KEY,
    status              text NOT NULL CHECK (status IN ('active', 'shadow', 'off')),
    snapshot_version    text,
    golden_set_version  text
);

-- Calles canónicas por distrito. Una calle que cruza distritos es una fila por distrito.
CREATE TABLE streets (
    id              bigserial PRIMARY KEY,
    ubigeo_code     char(6) NOT NULL REFERENCES ubigeos(code),
    street_type     text NOT NULL DEFAULT '',
    canonical_name  text NOT NULL,
    normalized_name text NOT NULL,      -- match_key del nombre (txt.Key)
    phonetic_name   text NOT NULL,
    source          text NOT NULL,      -- osm | operador | historico
    source_ref      text,               -- p. ej. ids de ways de OSM
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (ubigeo_code, street_type, normalized_name)
);
CREATE INDEX streets_name_trgm ON streets USING gin (normalized_name gin_trgm_ops);

CREATE TABLE street_aliases (
    id               bigserial PRIMARY KEY,
    street_id        bigint NOT NULL REFERENCES streets(id),
    alias            text NOT NULL,
    normalized_alias text NOT NULL,
    source           text NOT NULL,
    confidence       real,
    status           text NOT NULL CHECK (status IN ('candidate', 'promoted', 'rejected')),
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE street_segments (
    id           bigserial PRIMARY KEY,
    street_id    bigint NOT NULL REFERENCES streets(id),
    geom         geometry(LineString, 4326) NOT NULL,
    start_number integer,
    end_number   integer,
    source_ref   text
);
CREATE INDEX street_segments_geom_gix ON street_segments USING gist (geom);

-- Direcciones canónicas y sus observaciones. Nunca se sobrescribe una coordenada:
-- la vigente se calcula a partir de las observaciones por calidad de fuente.
CREATE TABLE canonical_addresses (
    id                 bigserial PRIMARY KEY,
    address_hash       text NOT NULL,
    street_id          bigint REFERENCES streets(id),
    ubigeo_code        char(6) REFERENCES ubigeos(code),
    house_number       text,
    block              text,
    lot                text,
    urbanization       text,
    normalized_address text NOT NULL,
    normalizer_version text NOT NULL,
    UNIQUE (address_hash, normalizer_version)
);

CREATE TABLE observations (
    id                   bigserial PRIMARY KEY,
    canonical_address_id bigint NOT NULL REFERENCES canonical_addresses(id),
    location             geometry(Point, 4326) NOT NULL,
    method               text NOT NULL CHECK (method IN ('pin_operador', 'gps_entrega', 'osm_addr', 'historico', 'geocoder')),
    source_quality       text NOT NULL CHECK (source_quality IN ('oro', 'plata', 'bronce')),
    author               text,
    source_ref           text,           -- p. ej. id del evento de entrega (sin datos personales)
    created_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX observations_address_idx ON observations (canonical_address_id);
CREATE INDEX observations_location_gix ON observations USING gist (location);

-- Puntos ancla para interpolar números sobre una calle.
CREATE TABLE anchors (
    id             bigserial PRIMARY KEY,
    street_id      bigint NOT NULL REFERENCES streets(id),
    house_number   integer NOT NULL,
    location       geometry(Point, 4326) NOT NULL,
    observation_id bigint REFERENCES observations(id),
    source         text NOT NULL
);
CREATE INDEX anchors_street_idx ON anchors (street_id, house_number);

-- Revisión manual y auditoría.
CREATE TABLE review_tickets (
    id          bigserial PRIMARY KEY,
    raw_input   jsonb NOT NULL,
    parse       jsonb NOT NULL,
    candidates  jsonb,
    priority    integer NOT NULL DEFAULT 0,
    status      text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'assigned', 'resolved', 'unresolvable', 'escalated')),
    assigned_to text,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX review_tickets_queue_idx ON review_tickets (status, priority DESC);

CREATE TABLE decision_events (
    id         bigserial PRIMARY KEY,
    ticket_id  bigint REFERENCES review_tickets(id),
    actor      text NOT NULL,
    action     text NOT NULL,
    before     jsonb,
    after      jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Una fila por resolución (se escribe de forma asíncrona, nunca en el camino del request).
CREATE TABLE resolution_events (
    id                 bigserial PRIMARY KEY,
    request_id         text NOT NULL,
    external_id        text,
    decision           text NOT NULL,
    resolution_type    text,
    precision_level    text,
    confidence         real,
    processing_us      integer,
    dataset_version    text,
    normalizer_version text,
    created_at         timestamptz NOT NULL DEFAULT now()
);
