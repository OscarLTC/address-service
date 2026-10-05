-- Índices para recargar calles sin recorrer tablas completas.
CREATE INDEX IF NOT EXISTS street_segments_street_idx ON street_segments (street_id);
CREATE INDEX IF NOT EXISTS canonical_addresses_street_idx ON canonical_addresses (street_id);
