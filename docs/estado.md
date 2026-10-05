# Estado del plan

Corte: 2026-10-04. Referencia: `docs/plan.md`.

## Resumen

El MVP está construido de punta a punta en su versión inicial: normalizador, resolver, API, cola de revisión con mapa y ciclo de verificación. **Lo que falta no es código: es la verdad de las coordenadas y las decisiones de la Fase 0.** Sin esas dos cosas no se puede pasar las compuertas G2 ni G5.

| Fase | Estado | Compuerta |
|---|---|---|
| F0 Línea base y decisiones | Parcial | **Bloqueada**: faltan decisiones de negocio |
| F1 Normalizador y dataset de oro | Hecha (v0.9.0) | G1 cumplida en sintético (99.2 %); en real, 75.6 % en test sobre 45 filas con etiquetas sin revisión humana completa |
| F2 Datos, snapshot y resolver | Hecha (v0) | **Bloqueada**: falta medir contra GPS de entrega (ADR 0005) |
| F3 Admin y verificación | Hecha (v0) | Pendiente de operadores reales |
| F4 Plataforma y observabilidad | Mayormente hecha | Falta despliegue en la nube elegida y la prueba de carga allí |
| F5 Sombra y piloto | No empezada | Requiere F0, F2 y tráfico real |

## Hecho

- **Normalizador:** 125 casos de prueba. Probado con datos reales de EMS-D: 600 direcciones perfiladas y 200 etiquetadas. Campos `number` y `reference`. Convenciones en `docs/convenciones-etiquetado.md`.
- **Datos:**
  - OSM: 50 distritos, 30,870 calles (homónimas separadas, ids estables), 15,794 anclas y 1,860 áreas.
  - PostGIS con el esquema del plano de control (migraciones 0001-0005).
- **Resolver:** match exacto, por alias, fonético y aproximado; desempate de homónimas; interpolación por paridad; decisión por riesgo; direcciones verificadas. Unos 33 µs por dirección.
- **API:**
  - `/v1/normalize`, `/v1/geocode` y `/v1/geocode/batch` (1,000 direcciones en ~22 ms).
  - API keys con límite por cliente, `/readyz` y `/metrics`.
  - Eventos sin datos personales y recarga del snapshot en caliente.
  - OpenAPI v0 e imagen Docker de 15.6 MB.
- **Admin:**
  - Importación de CSV con el resultado por fila; la cola se prioriza por frecuencia.
  - Mapa con calles candidatas; validaciones del pin; observaciones inmutables y auditoría.
  - Exportación de lo verificado.
  - Promoción de alias con confirmaciones independientes.
- **Ciclo cerrado:** una dirección en REVIEW, resuelta por un operador, pasa a AUTO_ACCEPT en el snapshot siguiente. Verificado de punta a punta.
- **Verdad de coordenadas:** la consulta del GPS de entrega, su validación (oro y plata) y la evaluación en metros están listas y probadas con datos inventados (ADR 0005).
- **Calidad:** CI con formato, `vet`, pruebas (incluida la integración con PostGIS), compuerta del dataset de oro y construcción de la imagen. Hook de pre-commit contra fugas de la muestra real.

## Bloqueos (no dependen del código)

1. **GPS de entrega de producción:** correr `scripts/emsd/gps_entregas.sql` y luego `cmd/gpsimport` y `cmd/geoeval -quality oro`. Sin esto, el error real del geocoder **no está medido**; la cifra actual (4.5 % en AUTO_ACCEPT contra puntos de OSM) es solo una referencia.
2. **Decisiones de la Fase 0:**
   - el error máximo aceptado por nivel de precisión;
   - quién opera la cola y con qué capacidad;
   - la nube y la región;
   - la comparación contra geocoders comerciales (requiere cuentas y presupuesto);
   - la confirmación del uso de ChatGPT con las direcciones de la muestra.
3. **Revisión humana del etiquetado:** las 16 dudas en `data/private/dudas_etiquetado_v1.xlsx`.
4. **Segunda muestra real**, de otro periodo, para medir el parseo sin el sobreajuste de la primera.
5. **Mapa base con licencia para producción:** el admin usa teselas de OSM, que solo sirven para desarrollo.

## Riesgos abiertos

- La cobertura automática es baja: AUTO_ACCEPT resuelve solo el 8 % de las direcciones del test sintético. Crecerá con las anclas verificadas (GPS y operadores), pero hay que medirlo con datos reales.
- El `score` no está calibrado: hace falta la verdad de GPS para convertirlo en una probabilidad.
- En la periferia, OSM tiene pocas calles y pocas anclas. Ahí habrá más revisión hasta que el admin complete los datos.
