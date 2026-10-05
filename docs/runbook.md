# Runbook

Procedimientos de operación del servicio. Plano de datos: `cmd/resolver`. Plano de control: PostgreSQL + PostGIS, `cmd/control` y los comandos de datos.

## Publicar un snapshot nuevo

1. Construir el snapshot desde OSM y las direcciones verificadas:
   ```bash
   go run ./cmd/snapshotbuild -out data/snapshot/lima.snap -db "$DATABASE_URL"
   ```
2. Revisar el resultado antes de publicar. Comparar contra la versión anterior:
   ```bash
   go run ./cmd/geoeval -snapshot data/snapshot/lima.snap
   ```
   No publicar si sube el error en AUTO_ACCEPT o si baja mucho el número de calles o anclas.
3. Cargar la misma versión en la base, para que el admin y la base coincidan:
   ```bash
   go run ./cmd/dbload -snapshot data/snapshot/lima.snap
   ```
4. Copiar el archivo a la ruta que vigilan los resolvers. Cada uno lo recarga solo en menos de 30 s (`-reload-every`) o al recibir SIGHUP. El intercambio es atómico y no corta solicitudes.
5. Confirmar en cada instancia: `GET /readyz` devuelve el `dataset_version` nuevo.

**Guardar siempre la versión anterior** (por ejemplo `lima.snap.<versión>`): es el rollback.

## Rollback de snapshot

1. Copiar la versión anterior sobre la ruta vigilada. El resolver la recarga sola.
2. Confirmar `GET /readyz` en cada instancia.
3. Volver a cargar esa misma versión en la base (`cmd/dbload`). El admin no arranca si la base y su snapshot difieren.

Los ids de calle son estables entre versiones, así que las direcciones verificadas siguen apuntando a su calle después de un rollback.

## Degradación controlada

| Síntoma | Qué pasa | Qué hacer |
|---|---|---|
| El resolver arranca sin snapshot | `/readyz` responde 503 y `/v1/geocode` también; `/v1/normalize` sigue funcionando | No enviarle tráfico (readiness). Restaurar el archivo del snapshot |
| Falla la recarga de un snapshot | Se mantiene la versión anterior y queda un log `recarga del snapshot fallida` | Revisar el archivo (formato, versión) y volver a publicar |
| La base de datos no responde | El resolver no se entera (no la usa por request). El admin y la importación de CSV fallan | Restaurar la base; el servicio de resolución sigue |
| Buffer de eventos lleno | Se descartan eventos, nunca se bloquea una respuesta; se cuenta en el log de apagado | Revisar el disco o el destino de los eventos |
| Picos de un cliente | Responde 429 con `Retry-After` según su límite (`-rate`) | Ajustar el límite por cliente o escalar instancias |

## Señales a vigilar (`/metrics`)

- `addrsvc_request_duration_ms` por ruta: p95 de `geocode` por debajo de 20 ms y un lote de 1,000 por debajo de 300 ms.
- `addrsvc_requests_total{code="5xx"}`: debe ser 0.
- `addrsvc_decisions_total`: la proporción de `REVIEW` y `AUTO_ACCEPT` por versión de snapshot. Un salto brusco tras publicar es motivo de rollback.

## Incidente: una coordenada aceptada estaba mal

1. Identificar la versión (`dataset_version` en la respuesta o en el evento) y el `external_id`.
2. Si la dirección ya está verificada, corregirla en el admin: el pin nuevo es otra observación y la anterior queda en la auditoría.
3. Si el error viene de una regla o de un ancla de OSM, agregar el caso a `testdata/` o al dataset de oro, corregir, y medir con `goldeneval` y `geoeval` antes de publicar.
4. Si afecta a muchas direcciones, hacer rollback del snapshot mientras se corrige.

## Datos personales (Ley 29733)

- Los eventos de resolución y los logs no guardan el texto de la dirección.
- Las muestras reales viven solo en `data/private/` (no versionado). El hook de pre-commit (`scripts/install_hooks.sh`) bloquea fragmentos de la muestra en los commits.
- Las consultas a producción (`scripts/emsd/*.sql`) no extraen nombres, documentos, teléfonos ni correos, y enmascaran secuencias largas de dígitos en el texto libre.
