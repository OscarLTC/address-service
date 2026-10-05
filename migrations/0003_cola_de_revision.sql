-- Cola de revisión priorizada por frecuencia: la misma dirección (match_key +
-- ubigeo) no crea tickets nuevos mientras haya uno abierto; suma ocurrencias.
ALTER TABLE review_tickets
    ADD COLUMN IF NOT EXISTS ubigeo_code char(6),
    ADD COLUMN IF NOT EXISTS address_key text,
    ADD COLUMN IF NOT EXISTS occurrences integer NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS source text,
    ADD COLUMN IF NOT EXISTS resolved_at timestamptz,
    ADD COLUMN IF NOT EXISTS resolution jsonb;
CREATE UNIQUE INDEX IF NOT EXISTS review_tickets_open_key
    ON review_tickets (address_key) WHERE status IN ('open', 'assigned', 'escalated');
