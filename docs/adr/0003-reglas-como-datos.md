# ADR 0003 - Reglas y casos de prueba como datos

**Decisión.** Los diccionarios (tipos de vía, abreviaturas, marcadores, títulos) están en `data/rules/lexicon.json` y el catálogo de ubigeos en `data/catalog`. Los casos de prueba del normalizador están en `testdata/normalize_cases.json`.

**Reglas de trabajo.**
- Cada bug de normalización se convierte primero en un caso de prueba y después se corrige.
- Un cambio de reglas se aprueba solo si toda la suite pasa y, más adelante, si la evaluación sobre el dataset de oro no empeora.
- Subir `normalizer.Version` cuando cambie el comportamiento: el `address_hash` depende de la normalización.
