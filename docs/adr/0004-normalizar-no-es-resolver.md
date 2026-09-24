# ADR 0004 - Normalizar no es resolver

**Decisión.** El normalizador limpia y estandariza de forma determinística. No corrige nombres dudosos (`Saucez` no se convierte en `Sauces`). La grafía distinta se relaciona mediante `phonetic_key`, que el matching usa con menor confianza.

**Motivo.** Corregir un nombre puede cambiar una calle real por otra. Esa decisión necesita un puntaje de confianza y un umbral, que pertenecen a la resolución.

**Consecuencia.** Todo lo ambiguo se marca con un flag (`GUARDED_C_AS_CALLE`, `AMBIGUOUS_LIMA`, `WEAK_DISTRICT_ALIAS`, `DISTRICT_CONFLICT`, `DISTRICT_BY_ACTIVE_ZONE`...) en lugar de resolverse en silencio.
