# Address Intelligence Service (Lima)

Servicio para normalizar y resolver direcciones peruanas. Este repositorio es el **arranque desde cero**: hoy contiene la Fase 1 del plan (normalizador determinístico + endpoint `/v1/normalize`) y la guía para conseguir los datos que faltan.

- Plan completo: [`docs/plan.md`](docs/plan.md)
- Qué necesitas y en qué orden: [`docs/SETUP.md`](docs/SETUP.md)
- Decisiones de arquitectura: [`docs/adr/`](docs/adr/)

## Inicio rápido

Requiere Go 1.22 o superior. No hay dependencias externas.

```bash
make test     # 122 casos de normalización + idempotencia + catálogo + geometría
make bench    # latencia del normalizador
make run      # servidor en :8080
make catalog  # regenera data/catalog/ubigeos.json desde el Excel del INEI
make osm      # descarga de OpenStreetMap límites, puntos addr:* y calles de Lima y Callao (data/osm, no versionado)
make golden   # genera data/geo/districts.json y goldenset/golden_v1.csv (no sobrescribe el test congelado)
make eval     # evalúa el normalizador sobre el dataset de oro (falla si G1 en test < 95 %)
make db-up    # PostgreSQL + PostGIS local con el esquema v0 (migrations/)
make snapshot # construye el snapshot del resolver desde OSM (data/snapshot, no versionado)
make geoeval  # error del resolver en metros sobre la partición test
```

```bash
curl -s localhost:8080/v1/geocode   -d '{"external_id":"PED-1","address":"Av. Arequipa 2450","district":"Lince"}'
```

```bash
curl -s localhost:8080/v1/normalize \
  -d '{"address":"Clle. Los Sáuces #245, Ate, Lima, Lima"}'
```

Devuelve el texto normalizado, los componentes (vía, nombre, número, Mz/Lt, urbanización...), la ubicación detectada (con su ubigeo y fuente), `match_key`, `phonetic_key` y una lista de `flags` con todo lo que fue ambiguo o se aplicó con cautela.

La ubicación (distrito, provincia, departamento) puede venir en campos separados, concatenada en `address` o mezclada; también se acepta `ubigeo`.

## Estructura

```text
cmd/resolver/            servidor HTTP: /healthz, /v1/normalize y /v1/geocode (si hay snapshot)
cmd/snapshotbuild/       construye el snapshot en memoria del resolver (calles, anclas, áreas)
cmd/geoeval/             mide el error del resolver en metros
cmd/catalogbuild/        genera el catálogo desde el Excel de ubigeos del INEI
cmd/osmfetch/            descarga datos crudos de OpenStreetMap (API Overpass)
cmd/goldengen/           genera límites distritales y el dataset de oro desde OSM
cmd/goldeneval/          evalúa el normalizador contra el dataset de oro
cmd/sampleprofile/       perfila una muestra real (data/private) con el normalizador; su salida no se versiona
scripts/emsd/            consulta de la muestra de EMS-D, planilla de etiquetado e importador al dataset de oro
internal/txt/            utilidades de texto (tildes, claves, fonética simple)
internal/catalog/        catálogo de ubigeos y búsqueda por nombre
internal/normalizer/     pipeline de normalización y detección de ubicación
internal/geo/            polígonos, punto en polígono y límites distritales
internal/golden/         formato CSV del dataset de oro
internal/snapshot/       formato del snapshot del resolver
internal/resolver/       búsqueda de calle, interpolación de número y decisión por riesgo
migrations/              esquema PostgreSQL + PostGIS del plano de control
deploy/                  docker compose de la base local
data/catalog/            ubigeos.json (INEI 2022, 1891 distritos, generado) y ubigeos_seed.json (alias y zonas de Lima/Callao)
data/geo/districts.json  límites de los 50 distritos de Lima y Callao (OSM, ODbL, generado)
data/rules/lexicon.json  diccionarios de reglas
data/config/zones.json   zonas de cobertura activas
testdata/                casos de prueba del normalizador (formato independiente del lenguaje)
goldenset/               dataset de oro (ver goldenset/README.md)
docs/                    plan, guía de arranque y ADRs
```

## Cómo trabajar

1. Encuentras una dirección que se normaliza mal.
2. La agregas como caso en `testdata/normalize_cases.json` (hoy falla).
3. Ajustas `data/rules/lexicon.json` o el código hasta que pase.
4. Si cambió el comportamiento, subes `Version` en `internal/normalizer/normalizer.go`.

## Estado

- Hecho: normalización (limpieza, tokenización, abreviaturas por posición, parseo de vía/número/Mz/Lt/interior/urbanización/referencia), detección de departamento/provincia/distrito con precedencias y ambigüedades, cobertura por zona, `/v1/normalize`.
- Hecho: límites distritales de Lima y Callao desde OSM; dataset de oro de calles (`golden_v1.csv`, 2,463 filas) y de Mz/Lt con urbanizaciones y AA.HH. reales de OSM (`golden_mzlt_v1.csv`, 1,014 filas), con test congelado; evaluador. Con `normalizer/0.8.0` la métrica G1 da 99.2 % en test, con 0 ubigeos equivocados y 100 % de idempotencia. **No es la exactitud real**: el ruido es sintético (ver `goldenset/README.md`).
- Hecho: primera muestra real de EMS-D (600 direcciones, en `data/private/`, no versionada). Con los campos de ubicación, el ubigeo coincide con el de EMS-D en el 99.3 %. Hallazgos en `docs/muestra-emsd.md`.
- Hecho: 200 direcciones reales etiquetadas por un LLM y revisadas por Claude con `docs/convenciones-etiquetado.md` (sin revisión humana completa). Sobre ellas, G1 da **75.6 % en test** (45 filas, sin usar para ajustar) y 96.3 % en dev, con 0 ubigeos equivocados.
- Hecho (Fase 2, v0): esquema PostGIS, snapshot con 30,870 calles de OSM (las homónimas de un mismo distrito separadas) y 16 mil anclas, resolver en memoria (~33 µs por dirección) y `POST /v1/geocode`. Contra puntos de OSM en test: AUTO_ACCEPT cubre el 8 % con 4.5 % de error (más de 50 m); el 45 % va a revisión. OSM no alcanza como verdad (ADR 0005): la meta de 1-2 % se medirá con GPS de entrega validado.
- Falta (siguiente): extraer y validar GPS de entrega (ADR 0005), cargar calles y anclas en PostGIS, responder las 16 dudas del etiquetado, una segunda muestra real para medir sin sobreajuste, campos `number` y `reference` en `/v1/normalize`, ingesta de calles al modelo de datos (Fase 2), snapshot en RAM, `/v1/geocode`, admin con mapa.
- Catálogo: nacional (INEI 2022, 1891 distritos). Falta el distrito 1892, creado después; se agrega en `ubigeos_seed.json` y se corre `make catalog`. Solo `LIMA_METRO` está activa en `data/config/zones.json`.
- Nombres repetidos en el país (Miraflores, San Miguel, Surco...): si exactamente uno cae en una zona activa se elige ese, con el flag `DISTRICT_BY_ACTIVE_ZONE`. Activar más zonas puede volver ambiguos nombres que hoy se resuelven.
