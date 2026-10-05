-- Alias propuestos por operadores al elegir una calle cuyo nombre difiere de lo
-- escrito. Se promueven con confirmaciones independientes (cmd/snapshotbuild -db).
ALTER TABLE street_aliases
    ADD COLUMN IF NOT EXISTS created_by text,
    ADD COLUMN IF NOT EXISTS ubigeo_code char(6);
CREATE INDEX IF NOT EXISTS street_aliases_lookup_idx ON street_aliases (ubigeo_code, normalized_alias);
