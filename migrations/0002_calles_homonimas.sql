-- Las calles homónimas de un mismo distrito son calles distintas (urbanizaciones
-- distintas con "Calle Los Geranios"): el snapshot las separa en componentes
-- conectados. La unicidad por (ubigeo, tipo, nombre) no aplica.
ALTER TABLE streets DROP CONSTRAINT IF EXISTS streets_ubigeo_code_street_type_normalized_name_key;
CREATE INDEX IF NOT EXISTS streets_lookup_idx ON streets (ubigeo_code, normalized_name);
