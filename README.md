# Address Intelligence Service (Lima)

Servicio para normalizar y resolver direcciones peruanas. Este repositorio es el **arranque desde cero**: hoy contiene la Fase 1 del plan (normalizador determinístico + endpoint `/v1/normalize`) y la guía para conseguir los datos que faltan.

- Plan completo: [`docs/plan.md`](docs/plan.md)
- Qué necesitas y en qué orden: [`docs/SETUP.md`](docs/SETUP.md)
- Decisiones de arquitectura: [`docs/adr/`](docs/adr/)

## Inicio rápido

Requiere Go 1.22 o superior. No hay dependencias externas.

```bash
make test     # 44 casos de normalización + idempotencia + catálogo
make bench    # latencia del normalizador
make run      # servidor en :8080
make catalog  # regenera data/catalog/ubigeos.json desde el Excel del INEI
```

```bash
curl -s localhost:8080/v1/normalize \
  -d '{"address":"Clle. Los Sáuces #245, Ate, Lima, Lima"}'
```

Devuelve el texto normalizado, los componentes (vía, nombre, número, Mz/Lt, urbanización...), la ubicación detectada (con su ubigeo y fuente), `match_key`, `phonetic_key` y una lista de `flags` con todo lo que fue ambiguo o se aplicó con cautela.

La ubicación (distrito, provincia, departamento) puede venir en campos separados, concatenada en `address` o mezclada; también se acepta `ubigeo`.

## Estructura

```text
cmd/resolver/            servidor HTTP (hoy: /healthz y /v1/normalize)
cmd/catalogbuild/        genera el catálogo desde el Excel de ubigeos del INEI
internal/txt/            utilidades de texto (tildes, claves, fonética simple)
internal/catalog/        catálogo de ubigeos y búsqueda por nombre
internal/normalizer/     pipeline de normalización y detección de ubicación
data/catalog/            ubigeos.json (INEI 2022, 1891 distritos, generado) y ubigeos_seed.json (alias y zonas de Lima/Callao)
data/rules/lexicon.json  diccionarios de reglas
data/config/zones.json   zonas de cobertura activas
testdata/                casos de prueba del normalizador (formato independiente del lenguaje)
goldenset/               plantilla del dataset de oro
docs/                    plan, guía de arranque y ADRs
```

## Cómo trabajar

1. Encuentras una dirección que se normaliza mal.
2. La agregas como caso en `testdata/normalize_cases.json` (hoy falla).
3. Ajustas `data/rules/lexicon.json` o el código hasta que pase.
4. Si cambió el comportamiento, subes `Version` en `internal/normalizer/normalizer.go`.

## Estado

- Hecho: normalización (limpieza, tokenización, abreviaturas por posición, parseo de vía/número/Mz/Lt/interior/urbanización/referencia), detección de departamento/provincia/distrito con precedencias y ambigüedades, cobertura por zona, `/v1/normalize`.
- Falta (siguiente): polígonos distritales, ingesta de calles desde OSM, dataset de oro, snapshot en RAM, `/v1/geocode`, admin con mapa.
- Catálogo: nacional (INEI 2022, 1891 distritos). Falta el distrito 1892, creado después; se agrega en `ubigeos_seed.json` y se corre `make catalog`. Solo `LIMA_METRO` está activa en `data/config/zones.json`.
- Nombres repetidos en el país (Miraflores, San Miguel, Surco...): si exactamente uno cae en una zona activa se elige ese, con el flag `DISTRICT_BY_ACTIVE_ZONE`. Activar más zonas puede volver ambiguos nombres que hoy se resuelven.
