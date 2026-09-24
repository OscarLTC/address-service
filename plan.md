# Address Intelligence Service - Lima

## Plan de desarrollo: fases del MVP, releases y arquitectura de referencia

**Versión:** 2.0 (reescritura del plan original)
**Alcance inicial:** Lima Metropolitana
**Cómo leer este documento:** las secciones 1 a 4 son el plan de ejecución (qué se hace, en qué orden y con qué criterios de avance). Los apéndices A a I son la referencia técnica que sustenta ese plan.

---

## 1. Resumen ejecutivo

### Qué se construye

Un servicio que recibe direcciones peruanas con formatos inconsistentes, las **normaliza**, las **resuelve** a una calle canónica y devuelve una **coordenada con su nivel de precisión y de confianza**. Cuando no está seguro, no adivina: envía el caso a **revisión manual en un mapa** (panel admin), y cada corrección humana mejora los datos del servicio.

Hoy la coordenada y el alcance de cada dirección se resuelven con un proceso manual. El servicio automatiza lo que se puede automatizar con riesgo medido y convierte el trabajo manual restante en datos verificados.

### Principios

1. **Normalizar no es resolver.** La normalización limpia y estandariza el texto de forma determinística. La corrección de un nombre dudoso pertenece a la resolución y siempre lleva un nivel de confianza.
2. **Riesgo medido, no cero.** El objetivo es un error acotado y una regla clara de abstención: es mejor `REVIEW_REQUIRED` que una coordenada equivocada con confianza alta.
3. **Dos planos.** El resolver (plano de datos) responde solo desde memoria. El admin, los jobs y la construcción de índices (plano de control) pueden ser lentos sin afectar la latencia.
4. **Sin LLM en el request.** Todo el camino de respuesta es determinístico.
5. **Precisión explícita.** Cada respuesta indica qué tan fina es la coordenada (`precision_level`), separado de qué tan seguro está el match (`confidence`).
6. **Los datos verificados son el activo.** Las observaciones inmutables, los aliases confirmados y el dataset de oro son lo que da valor al servicio con el tiempo.
7. **Empezar simple.** Sin Kubernetes, sin Kafka, sin Elasticsearch, sin microservicios por componente hasta que una métrica lo justifique.

### Línea de tiempo (estimación orientativa para una persona; se recalibra al cerrar la Fase 0)

| Fase | Semanas | Foco | Compuerta de salida |
|---|---|---|---|
| F0 | 1-2 | Línea base, decisiones y riesgos previos | G0: se puede construir y usar los datos |
| F1 | 2-4 | Normalizador v0 y dataset de oro | G1: parseo correcto sobre el objetivo |
| F2 | 4-8 | Datos de calles, snapshot y resolver | G2: latencia y exactitud medidas |
| F3 | 7-10 | Admin mínimo y bucle de verificación | G3: un operador cierra casos de punta a punta |
| F4 | 9-11 | Plataforma, seguridad y observabilidad | G4: SLO cumplidos bajo ráfagas |
| F5 | 11-15 | Modo sombra y piloto asistido | G5: criterios de lanzamiento del MVP |

Las fases se solapan a propósito: el admin (F3) se construye mientras termina el resolver (F2), porque produce los datos verificados que necesita la evaluación.

---

## 2. Alcance y criterios de éxito del MVP

### Dentro del MVP

- Cobertura activa: Lima Metropolitana (43 distritos). El **catálogo nacional de ubigeos** se carga desde la F1: cualquier dirección de otra zona se detecta y se responde con `OUT_OF_SCOPE` y el ubigeo detectado, sin coordenada dudosa. Callao pasa a zona activa en la R1.1.
- **Entrada flexible de ubicación:** departamento, provincia y distrito como campos separados, concatenados dentro de `address`, o mezclados; `ubigeo` explícito cuando el cliente lo tenga (Apéndice B, N5, y Apéndice E).
- `POST /v1/normalize`, `POST /v1/geocode`, `POST /v1/geocode/batch` (síncrono, hasta 1,000-5,000 registros según benchmark).
- Resolver con snapshot en memoria, motor de decisión por riesgo y `precision_level`.
- Panel admin: cola de revisión, mapa, observaciones, auditoría, promoción de aliases, carga y exportación CSV/Excel.
- Métricas, dashboard, API keys y límites por cliente.
- Modo sombra y piloto asistido contra el proceso manual actual.

### Fuera del MVP (van a releases posteriores)

Callao, jobs y webhooks, `/feedback` automático, autocomplete, SFTP, SDKs, reverse geocode, eventos (Kafka/RabbitMQ), multi-cliente con facturación, otras ciudades.

### Criterios de éxito (valores de partida; se confirman en la Fase 0)

| Métrica | Valor de partida |
|---|---|
| Error máximo en `AUTO_ACCEPT` | 1-2 % |
| Definición de error | > 50 m en `ADDRESS_POINT` y `SEGMENT_INTERPOLATED`; > 150 m en `STREET`; > 300 m en `ZONE` |
| Latencia interna (`processing_ms`) | p50 < 5 ms, p95 < 20 ms, p99 < 50 ms |
| Batch de 1,000 | < 300 ms de procesamiento interno cuando la mayoría resuelve en RAM |
| Cobertura automática | La define la operación en F0 (qué % debe dejar de pasar por revisión manual) |
| Estabilidad | Curva riesgo-cobertura estable durante 2-3 semanas en modo sombra |

Estos números son hipótesis de diseño. Ninguno se considera cumplido sin benchmark y sin evaluación sobre el test congelado.

---

## 3. Fases de desarrollo del MVP

### F0 - Línea base, decisiones y riesgos previos (semanas 1-2)

**Objetivo:** confirmar que el proyecto se puede construir y que los datos se pueden usar, antes de escribir el servicio.

**Trabajo**

- [ ] Definir el costo del error (entrega fallida, reintento, revisión manual) y el error máximo aceptado por nivel de precisión.
- [ ] Auditar el origen de las coordenadas históricas (pin manual, geocoder, GPS de entrega, desconocido) y clasificarlas en oro / plata / bronce.
- [ ] Verificar de forma independiente unas 300 direcciones para medir el **error actual** (línea base).
- [ ] Comparar las mismas 300-500 direcciones contra 2 o 3 geocoders comerciales: error y costo por 1,000 consultas.
- [ ] Confirmar la propiedad y el uso permitido del histórico; revisar la Ley 29733 (datos personales), la licencia ODbL de OSM, los términos del mapa base y los de los geocoders externos.
- [ ] Perfilar los datos: tipos de vía, tokens más frecuentes y desconocidos, % con distrito, % con Mz/Lt, duplicados, problemas de codificación.
- [ ] Decidir lenguaje del resolver (Go o Java/Spring), nube y región (medir RTT desde los clientes) y pico esperado de requests por segundo.
- [ ] Definir quién opera la cola de revisión y con qué capacidad.
- [ ] Crear el repositorio, la especificación OpenAPI v0, el modelo de datos v0 y un registro de decisiones (ADR).

**Entregables:** línea base de error, informe build vs buy, meta de error por escrito, ADRs, OpenAPI v0.

**Compuerta G0 (go / no-go):** línea base medida; build vs buy justificado; uso de los datos autorizado; meta de error escrita; responsable de la cola definido. Si falla, aplicar la sección 5 ("Cuándo detenerse o replantear").

---

#### Variante: arranque sin datos históricos

Si no existe un histórico de direcciones y coordenadas (el caso de este proyecto al iniciar), la F0 y las fases siguientes cambian así. La guía práctica está en `docs/SETUP.md` del repositorio.

- **F0:** no hay auditoría de coordenadas históricas ni "error actual" que medir. En su lugar: (1) fuentes públicas (catálogo de ubigeos del INEI, límites distritales, OpenStreetMap); (2) una **muestra real de direcciones sucias** (mínimo 100 a 300, con autorización y sin datos personales); (3) verificación independiente de esa muestra; (4) línea base = geocoders comerciales medidos contra esa verdad verificada.
- **Sin muestra real** el proyecto puede avanzar con datos sintéticos y puntos de dirección de OSM, pero la exactitud real queda **sin medir**. Debe quedar escrito en G0.
- **F1:** el dataset de oro se arma con tres pistas: sintética (regresión del normalizador), puntos `addr:*` de OSM y muestra real. Solo la muestra real mide el error verdadero.
- **F2:** la semilla de calles sale únicamente de OSM (más sus puntos de dirección como puntos ancla); no hay "candidatas del histórico".
- **F3 se adelanta:** el admin con mapa pasa a ser la **fuente principal de verdad**. Conviene arrancar su versión mínima en paralelo a F2 (hacia la semana 5) y no al final.
- **Riesgo adicional:** la cobertura de OSM es desigual en la periferia; las zonas con pocas calles darán `REVIEW_REQUIRED` con más frecuencia hasta que el admin las complete.

**Compuerta G0 (sin histórico):** además de lo anterior, hay una decisión escrita sobre la muestra real (conseguida, o exactitud declarada como no medida).

---

### F1 - Normalizador v0 y dataset de oro (semanas 2-4)

**Objetivo:** un normalizador determinístico, versionado y medido, expuesto como `/v1/normalize`.

**Trabajo**

- [ ] **Catálogo nacional de ubigeos** (departamento, provincia, distrito) con polígonos y aliases (Cercado, Surco, SJL, SJM, VES, VMT, SMP, etc.). Solo Lima Metropolitana queda como zona activa; el resto de direcciones detectadas responde `OUT_OF_SCOPE`.
- [ ] **Detección de ubicación (N5):** parseo de departamento, provincia y distrito desde campos separados y desde el sufijo del texto concatenado, validación por jerarquía, precedencias y ambigüedades (Apéndice B). Tabla `coverage_zones` con el estado de cada zona.
- [ ] Diccionarios v0: tipos de vía, abreviaturas dependientes de la posición, Mz/Lt/urb/AA.HH./PJ/coop/asoc, santos y títulos, ordinales, interior/piso/tienda.
- [ ] Pipeline N0-N7 como librería pura; reglas en YAML con ID, clase de riesgo (segura / guardada / insegura) y ejemplos (Apéndice B).
- [ ] Claves `display`, `match_key`, `phonetic_key` y `token_set_key`; parseo con varias hipótesis (n-best) cuando hay ambigüedad.
- [ ] Pruebas de tabla por regla y prueba de idempotencia en CI.
- [ ] **Dataset de oro v1:** ~1,000 direcciones estratificadas por distrito, tipo de dirección y nivel de suciedad; con el parseo esperado. Partición por calle (no por fila) y un **test congelado**. Las coordenadas verificadas se completan en F3 con el admin.
- [ ] Exponer `/v1/normalize` (sin geolocalización).
- [ ] Reporte de calidad: cobertura de parseo, tasa de tokens desconocidos, % de direcciones con flags y dispersión espacial por `match_key`.

**Entregables:** librería del normalizador v0, diccionarios y reglas versionados, dataset de oro v1, endpoint `/normalize`, reporte de calidad.

**Compuerta G1:** parseo correcto sobre el objetivo en el test congelado (punto de partida: 95 % de direcciones con vía, número y distrito bien identificados cuando existen en el texto); idempotencia al 100 %; detección correcta de departamento, provincia y distrito sobre el objetivo, tanto con campos separados como concatenados; ninguna regla con dispersión espacial anómala sin justificar.

---

### F2 - Datos de calles, snapshot y resolver (semanas 4-8)

**Objetivo:** resolver direcciones contra un índice en memoria, con motor de decisión por riesgo.

**Trabajo**

- [ ] Esquema PostgreSQL + PostGIS y migraciones (Apéndice D).
- [ ] Ingesta de calles de Lima desde OSM, más calles y aliases candidatos del histórico si existe; todo normalizado con el mismo normalizador. Geometría por tramo y **puntos ancla** desde coordenadas oro.
- [ ] **Snapshot builder v0:** snapshot completo, formato versionado, manifiesto y checksum, publicado en almacenamiento de objetos. Se genera **por zona de cobertura** (el ubigeo es la llave de partición) y cada resolver carga solo las zonas activas.
- [ ] **Resolver v0:** carga del snapshot, cache LRU con `singleflight`, match exacto, fuzzy acotado por distrito (índice invertido por tokens + similitud), interpolación de número sobre el tramo, `precision_level`.
- [ ] **Motor de decisión:** `AUTO_ACCEPT`, `ACCEPT_FLAGGED`, `REVIEW`, `REJECT`, con confianza penalizada por ambigüedad, falta de distrito, falta de número, conflicto de señales y margen estrecho entre candidatos.
- [ ] `POST /v1/geocode` y `POST /v1/geocode/batch` (deduplicación, agrupación por distrito, pool de workers, respuesta NDJSON).
- [ ] Buffer asíncrono de eventos hacia `resolution_events` (nunca bloquea la respuesta).
- [ ] Benchmarks de latencia y evaluación contra el dataset de oro; primera **curva riesgo-cobertura**.

**Entregables:** esquema de datos, pipeline de ingesta, snapshot builder, resolver v0, endpoints `/geocode` y `/batch`, informe de benchmark, curva riesgo-cobertura v0.

**Compuerta G2:** latencia dentro de los objetivos en benchmark; exactitud medida por nivel de precisión y **mejor o igual que la línea base de F0**; umbrales iniciales de decisión fijados a partir de la curva.

---

### F3 - Admin mínimo y bucle de verificación (semanas 7-10)

**Objetivo:** que un operador resuelva los casos `REVIEW` sobre un mapa y que cada decisión quede como dato verificado.

**Trabajo**

- [ ] Autenticación del admin y roles (operador, revisor, administrador).
- [ ] Cola de revisión **priorizada por frecuencia y costo del error**, no por antigüedad.
- [ ] Pantalla de revisión con texto original, parseo, flags, candidatos con score, coordenada histórica y mapa (polígono del distrito, calle candidata, pines) con MapLibre GL.
- [ ] Acciones: confirmar candidato, colocar o mover pin, corregir o crear calle, marcar irresoluble (con motivo), escalar.
- [ ] Modelo de **observaciones inmutables** (método, autor, fecha, calidad de fuente) y eventos de auditoría (antes / después). La coordenada vigente se calcula a partir de las observaciones.
- [ ] Alertas automáticas al guardar: pin fuera del distrito, pin lejos de la calle elegida, número que no encaja con los vecinos.
- [ ] Doble verificación al crear una calle nueva o promover un alias con impacto amplio; auditoría por muestreo de operadores.
- [ ] Reglas de promoción de alias (mínimo de confirmaciones independientes, baja dispersión espacial, sin fusionar calles canónicas distintas) y entrega mediante **delta** al resolver.
- [ ] Guía de criterio de pin para operadores (qué punto es "el correcto").
- [ ] Carga y exportación CSV/Excel.
- [ ] Usar el admin para **verificar las coordenadas del dataset de oro** y completar sus etiquetas.

**Entregables:** admin funcional, guía de operadores, dataset de oro con coordenadas verificadas, canal de deltas al resolver.

**Compuerta G3:** un operador cierra casos de punta a punta; un alias aprobado llega al resolver en segundos; el acuerdo entre operadores en una muestra doble ciega es aceptable (umbral fijado en F0).

---

### F4 - Plataforma, seguridad y observabilidad (semanas 9-11)

**Objetivo:** dejar el servicio operable, medible y seguro.

**Trabajo**

- [ ] CI/CD con la **evaluación del dataset de oro como compuerta**: un cambio de regla o de datos no pasa si baja la exactitud o sube la dispersión espacial.
- [ ] Contenedores y despliegue en servicio administrado, mínimo 2 instancias del resolver, sin escalar a cero; readiness solo después de cargar el snapshot.
- [ ] OpenTelemetry, métricas p50/p95/p99, logs estructurados sin datos personales innecesarios, política de retención, dashboard (Apéndice I).
- [ ] API keys, rate limiting por cliente, TLS, WAF, idempotencia, timeouts y circuit breaker para proveedores externos.
- [ ] Prueba de carga con ráfagas de 1,000+ registros y prueba de pérdida de una instancia.
- [ ] Runbook: rollback de snapshot, degradación controlada, respuesta ante incidentes.

**Entregables:** pipeline CI/CD, entorno productivo, dashboard, runbook, informe de carga.

**Compuerta G4:** SLO cumplidos bajo ráfagas; rollback de snapshot probado; seguridad mínima verificada.

---

### F5 - Modo sombra y piloto asistido (semanas 11-15)

**Objetivo:** demostrar con tráfico real que el riesgo está dentro del objetivo antes de automatizar.

**Trabajo**

- [ ] **Sombra:** el servicio procesa el tráfico real sin efecto operativo y se compara con el proceso manual actual. Duración mínima de 2-3 semanas.
- [ ] **Muestreo aleatorio de los auto-aceptados** para medir el error real (no solo el de los casos revisados).
- [ ] Calibración de la confianza: verificar que "0.97" corresponda a ~97 % de aciertos, por tipo de match y por distrito.
- [ ] **Piloto asistido:** solo `AUTO_ACCEPT` pasa sin intervención; el resto va a revisión. Activación distrito por distrito.
- [ ] Medir el tamaño de la cola, el tiempo por caso y el % resuelto por aliases ya existentes.
- [ ] Documentar el error máximo aceptado por escrito, para que subirlo sea una decisión consciente y no una consecuencia de la presión por cobertura.

**Entregables:** informe de sombra, curva riesgo-cobertura estable, umbrales calibrados, plan de activación por distrito.

**Compuerta G5 (lanzamiento del MVP):** error en `AUTO_ACCEPT` dentro del objetivo en la muestra aleatoria; cobertura automática dentro de la meta; la operación sostiene la cola de revisión; rollback probado.

---

## 4. Releases posteriores

Las duraciones son orientativas y parten del cierre del MVP. Cada release repite el ciclo: sombra o canary antes de activar, y una compuerta de salida basada en métricas.

| Release | Foco | Contenido clave | Compuerta de salida |
|---|---|---|---|
| **R1.1** (≈ 6-8 sem) | Cobertura y bucle de datos | activar la zona Callao (7 distritos); jobs asíncronos + webhooks; `POST /v1/feedback` (GPS de entrega confirma o corrige); autocomplete interno; promoción automática de aliases con reglas; recalibración por distrito; ampliar el dataset de oro | El % de revisión baja de forma sostenida; el error de `AUTO_ACCEPT` sigue dentro del objetivo |
| **R1.2** (≈ 8-10 sem) | Integraciones | SFTP y CSV automatizado; SDKs (Java, .NET, Node/TypeScript) con normalización local opcional; reverse geocode; eventos (Kafka/RabbitMQ/SQS) bajo demanda; portal de API keys y consumo; documentación pública v1 | Al menos 2 integraciones en producción sin soporte manual por caso |
| **R2.0** (≈ 10-14 sem) | Servicio multi-cliente | Capa de datos por cliente (aliases y observaciones propias) sobre la capa global; cuotas y planes; facturación; SLA por plan; auditoría de cumplimiento (Ley 29733); página de estado; soporte | Aislamiento entre clientes verificado; primer cliente externo operando |
| **R2.x** (continuo) | Expansión geográfica | Proceso repetible de activación de zona (el catálogo nacional ya existe desde la F1): calles, diccionarios locales, dataset de oro, operadores, sombra y snapshot propio. Zonas priorizadas por volumen | Cada zona cumple su propia compuerta G5 antes de activarse |
| **R3.0** | Address Intelligence completo | `/validate`, `/match`, autocomplete público, etiquetador de tokens con ML (CRF) solo si el parseo por reglas se estanca, conectividad privada y despliegue dedicado o on-premise | Métricas de precisión y costo por 1,000 resoluciones mejores que el proveedor externo de referencia |

**Regla de entrada a cada release:** no se abre un release nuevo mientras el anterior no cumpla su compuerta y el error de `AUTO_ACCEPT` siga dentro del objetivo escrito.

---

## 5. Riesgos y decisiones previas

### Riesgos principales

| Riesgo | Efecto | Mitigación |
|---|---|---|
| La coordenada histórica es mala o de origen desconocido | Se mide contra un error y se "aprende" el error | Auditar origen en F0; dataset de oro verificado de forma independiente |
| Sesgo de supervivencia en el histórico | Solo hay direcciones fáciles | Estratificar el dataset de oro; incluir casos que se corrigieron a mano |
| Cobertura desigual por distrito | Un promedio global oculta distritos con mal desempeño | Métricas por distrito; activación distrito por distrito |
| Techo de precisión en zonas sin numeración | Meta imposible de "coordenada de puerta" | Definir la meta por `precision_level`; aceptar `ZONE` como resultado válido |
| Uso no autorizado de datos | Riesgo legal y comercial | Confirmar propiedad y uso en F0; capa por cliente desde el diseño |
| Datos personales (Ley 29733) | Sanciones, fuga de información | Minimizar logs, retención definida, acceso restringido |
| Términos de geocoders y licencia OSM | No poder almacenar o reutilizar resultados | Revisar términos antes de "aprender" de un proveedor externo |
| Ruido introducido por operadores | Datos contaminados | Guía de pin, alertas en la UI, doble verificación, auditoría por muestreo |
| Cola de revisión sin dueño | Casos acumulados o aceptados sin mirar | Responsable y capacidad definidos en F0 |
| Presión por bajar el umbral | El error sube sin decisión consciente | Error máximo escrito; cambios de umbral solo con curva riesgo-cobertura |
| Dependencia de una sola persona | Conocimiento concentrado | ADRs y documentación de reglas desde el inicio |
| Sobrealcance (Kafka, SDKs, portal) | Se retrasa el núcleo | Alcance congelado hasta cerrar G5 |

### Cuándo detenerse o replantear

- La línea base muestra que las coordenadas actuales ya son suficientemente buenas y el problema real está en otro lado.
- Un proveedor comercial alcanza el objetivo de precisión a un costo menor que construir y mantener.
- No hay forma de obtener verdad de referencia confiable.
- El uso de los datos históricos no está autorizado.

---

# Apéndices (referencia técnica)

## Apéndice A - Arquitectura: dos planos

```text
                    CLIENTES / INTEGRACIONES
              REST - Batch - Webhooks - Archivos
                              |
                       API Gateway / WAF
                     (auth, rate limit, TLS)
                              |
   +--------------------------+--------------------------+
   |  PLANO DE DATOS (camino rápido, solo lectura)       |
   |                                                     |
   |   Resolver A        Resolver B       Event buffer   |
   |   snapshot en RAM   snapshot en RAM  (asíncrono)    |
   +--------^-----------------------------------|--------+
            | snapshots / deltas                 | eventos
   +--------|-----------------------------------v--------+
   |  PLANO DE CONTROL (escritura, humano en el bucle)   |
   |                                                     |
   |  Admin UI    Admin API    Workers    Index builder  |
   |  (React +    (cola de     (jobs,     (snapshots     |
   |  MapLibre)   revisión)    webhooks)  versionados)   |
   |                                                     |
   |  PostgreSQL + PostGIS          Object storage       |
   |  (fuente de verdad)            (snapshots, cargas)  |
   +-----------------------------------------------------+
```

**Dos ejecutables del mismo repositorio** (`resolver` y `control`), más un worker. Es un monolito modular; la separación por plano existe para aislar la latencia, no por moda de microservicios.

### Por qué el resolver es rápido

- **Sin red en el camino caliente.** El snapshot inmutable en RAM contiene distritos y polígonos, calles con geometría, aliases, puntos ancla y las direcciones ya resueltas. No hay llamadas a PostgreSQL, Redis ni HTTP durante una consulta.
- **Fuzzy acotado por distrito.** Al detectar primero el distrito, los candidatos son las calles de un solo distrito. Un índice invertido por tokens más scoring de similitud en memoria lo resuelve en pocos milisegundos. `pg_trgm` se usa solo en el buscador del admin y la minería offline.
- **Actualización sin reinicios.** El builder publica un snapshot versionado y los resolvers lo intercambian de forma atómica. Un alias aprobado llega mediante un delta en segundos y se integra al siguiente snapshot completo. Rollback = volver a la versión anterior.
- **Nada bloquea la respuesta.** Logs y tickets de revisión van a un buffer asíncrono.
- **El geocoder externo no está en el camino rápido.** `mode=fast` responde de inmediato (con `REVIEW_REQUIRED` si no hay certeza). `mode=extended` permite el fallback con su propio SLA, o se resuelve después por webhook.
- **Batch eficiente.** Deduplicación, agrupación por distrito, pool de workers y respuesta en streaming (NDJSON).
- **Instancias calientes.** Mínimo 2, sin escalar a cero, `ready` solo tras cargar el snapshot.

### Presupuesto de latencia (objetivos de diseño a validar con benchmark)

| Paso | Objetivo |
|---|---|
| Normalizar y detectar distrito | < 0.5 ms |
| Cache o match exacto | < 0.05 ms |
| Fuzzy acotado al distrito | < 2 ms |
| Interpolar número sobre el tramo | < 0.5 ms |
| Serializar | < 0.2 ms |
| **Total de procesamiento** | **p50 de 1 a 3 ms, p99 < 20 ms** |

La latencia de extremo a extremo la dominará la red. Medir el RTT desde donde están los clientes hasta la región elegida antes de optimizar el código. Si los clientes están en Lima, un SDK con normalización local o un despliegue cercano puede ganar más que cualquier ajuste del resolver. Reportar siempre `processing_ms` separado de la latencia de red.

### Qué se cachea y dónde

| Información | RAM (snapshot / LRU) | Redis | PostgreSQL |
|---|:---:|:---:|:---:|
| Abreviaturas, ubigeos y aliases | Sí | No | Sí |
| Calles y geometría por distrito | Sí | No | Sí |
| Dirección normalizada → resultado | LRU | No (fase inicial) | Sí (observaciones) |
| Resultado del geocoder externo | LRU | Opcional | Sí |
| Historial y auditoría | No | No | Sí |

**Redis queda fuera del camino caliente.** Solo se incorpora para idempotencia, rate limit distribuido y estado de jobs, y no antes de que una métrica lo justifique. En la fase inicial los jobs pueden usar una cola en PostgreSQL (`SKIP LOCKED`).

---

## Apéndice B - Especificación del normalizador

### Tres capas

1. **Limpieza:** Unicode, mayúsculas, tildes (solo en la clave de comparación; se conserva la Ñ para mostrar), espacios, puntuación, `Nº / # / Nro`. 100 % determinística.
2. **Estandarización:** expansión de abreviaturas según su posición y extracción de componentes (tipo de vía, nombre, número, Mz/Lt, urbanización, distrito, referencia).
3. **Corrección fonética u ortográfica** (`Saucez` → `Sauces`): esto es **resolución**, no normalización. El normalizador emite una clave fonética aparte y el matching la usa con menor confianza.

### Pipeline

| Etapa | Qué hace |
|---|---|
| N0 Ingesta | Arregla codificación (`Ã±`), caracteres de control, vacíos y longitudes absurdas |
| N1 Texto base | NFKC, mayúsculas, espacios, puntuación, ordinales (`1RA`, `PRIMERA` → `1`) |
| N2 Tokenización | Separa pegados: `Av.Javier`, `MzA`, `Lt5`, `Calle5`, `245A` |
| N3 Léxico | Expande abreviaturas según la posición: tipo de vía, Mz/Lt, urbanización/AA.HH./PJ/coop/asoc, santos, títulos, cuadra, km |
| N4 Parseo | Asigna roles: vía, número, interior, Mz/Lt, urbanización, referencia, distrito |
| N5 Ubicación | Detecta departamento, provincia y distrito (ver "Detección de ubicación") y marca conflictos |
| N6 Claves | `display`, `match_key`, `phonetic_key`, `token_set_key` |
| N7 Salida | Original intacto, componentes, flags, ambigüedades y `normalizer_version` |

### Reglas de Lima con más riesgo

- **Abreviaturas según su posición.** `C.`, `Av.`, `Jr.` cuentan como tipo de vía solo al inicio. `Mz` y `Lt` cuentan solo si les sigue un token corto. Los alias de distrito (`SMP`, `SJL`, `VES`) se aplican al campo distrito o a la cola del texto, no en cualquier parte.
- **Números que son nombre de calle.** `Calle 5 Mz A Lt 3`, `Jr. 2 de Mayo 340`: si el número sigue directo al tipo de vía y hay otro número o un Mz/Lt después, pertenece al nombre de la vía.
- **Direcciones sin número.** Mz/Lt más urbanización, AA.HH. o asociación es la llave real; el parser lo soporta como caso normal.
- **Santos y títulos:** `Sta.`, `Sto.`, `Sn`, `Gral.`, `Mcal.`, `Almte.`, `Cdte.`.
- **Distrito.** 43 distritos de Lima Metropolitana y 7 del Callao. Ambigüedades reales: `Lima` (provincia o Cercado), `Callao` (distrito o provincia), `Lurigancho` frente a `Chosica`, `Surco`. Si el campo distrito y el texto se contradicen, se marca el conflicto; no se elige uno en silencio.
- **Referencias** ("frente al grifo", "altura del paradero") se mueven a `reference`, no se descartan.
- **Interior y piso:** `Int`, `Dpto`, `Of`, `Piso`, `Tda`, `Stand` se separan del número de puerta.

### Detección de ubicación (N5): departamento, provincia y distrito

La ubicación puede llegar de tres formas equivalentes, y el servicio produce la misma salida en las tres:

```text
1. Campos separados:  address="Av. Los Sauces 245"  district="Ate"  province="Lima"  department="Lima"
2. Todo concatenado:  address="Av. Los Sauces 245, Ate, Lima, Lima"
3. Mixto:             address="Av. Los Sauces 245 - Ate"  province="Lima"
```

**Cómo se procesa el texto concatenado**

- Se busca **de derecha a izquierda contra el catálogo**, sin depender de comas: los clientes usan comas, guiones, barras o solo espacios. Se prueban los últimos 1 a 3 tramos del texto contra distritos, provincias y departamentos.
- La **jerarquía valida**: una combinación distrito-provincia-departamento solo es válida si el catálogo la contiene. Eso descarta la mayoría de los falsos positivos.
- Lo que se reconoce como ubicación se retira del texto de la dirección y se guarda en los componentes; el original se conserva.

**Orden de precedencia**

1. `ubigeo` explícito enviado por el cliente.
2. Campos estructurados que sean coherentes entre sí en el catálogo.
3. Sufijo detectado en el texto.
4. Polígono que contiene la coordenada, si el cliente la envía (sirve de desempate y de validación).
5. `default_area` configurado para el cliente (con flag `AREA_FROM_DEFAULT`).

**Ambigüedades y conflictos (se marcan, no se ocultan)**

| Situación | Tratamiento |
|---|---|
| Campo y texto se contradicen ("Ate" vs "La Molina") | Flag `DISTRICT_CONFLICT`; pasa a revisión si afecta la resolución |
| "Lima, Lima" (distrito y provincia, o provincia y departamento) | Se resuelve con la jerarquía y con la presencia de otro distrito antes en el texto; si no alcanza, `AMBIGUOUS_LIMA`, sin asumir |
| Nombre repetido entre provincias (por ejemplo La Victoria en Lima y en Chiclayo) sin provincia ni departamento | Se usa `default_area` del cliente con flag; si no existe, revisión |
| Ubicación fuera de una zona activa | `OUT_OF_SCOPE` con el ubigeo detectado |

### Principios que reducen riesgo

1. Se conserva siempre el texto original.
2. Ante una ambigüedad se emiten varias hipótesis con puntaje; el resolver desempata contra el índice de calles.
3. Las reglas son datos versionados (YAML), con ID, ejemplos y clase de riesgo. Las **seguras** siempre se aplican; las **guardadas** dependen del contexto y dejan flag; las **inseguras** no van al normalizador, van a la capa de matching.
4. Un cambio de regla solo se aprueba si no baja el dataset de oro ni sube la dispersión espacial.
5. El `address_hash` depende de la normalización, por eso se versiona y se puede reindexar.
6. Existe una sola implementación del normalizador. El admin llama al servicio para previsualizar; nunca se reimplementa en el frontend.

### Ejemplo de salida

```json
{
  "raw": "Clle. Los Sáuces #245, Ate",
  "normalized": "CALLE LOS SAUCES 245",
  "components": {
    "street_type": "CALLE",
    "street_name": "LOS SAUCES",
    "number": "245",
    "district": "ATE"
  },
  "match_key": "CALLE|LOS SAUCES|245",
  "phonetic_key": "CALE|LOS SAUSES|245",
  "flags": ["ABBREVIATION_EXPANDED", "DISTRICT_FROM_TEXT"],
  "normalizer_version": "1.0.0"
}
```

---

## Apéndice C - Datos, entrenamiento y reducción de riesgo

Al inicio "entrenar" no es machine learning: es **curar datos**.

### Dataset de oro

- ~1,000 direcciones estratificadas por distrito (más peso a los de mayor volumen), tipo de dirección y nivel de suciedad.
- Etiquetas: parseo esperado, calle canónica y coordenada verificada de forma independiente.
- Partición por calle, no por fila; **test congelado** que no se toca para ajustar reglas; renovación periódica con muestras nuevas.
- Con ~300 muestras sin fallos se puede afirmar un error ≤ 1 % con 95 % de confianza (regla de tres). Para distritos pequeños se audita de forma continua.

### Clasificación de coordenadas históricas

| Nivel | Criterio |
|---|---|
| Oro | Verificadas de forma independiente (GPS de entrega confirmada o revisión manual) |
| Plata | Consistentes con otras del mismo grupo |
| Bronce | Sin verificar |

**Circularidad:** si se valida contra coordenadas malas, se "aprende" el error. Por eso el dataset de oro se verifica aparte del histórico.

### Aprender del histórico

- **Diccionarios por frecuencia:** los primeros ~200 tokens suelen cubrir gran parte del volumen.
- **Aliases por co-localización:** cadenas distintas, del mismo distrito, con coordenadas a pocos metros son candidatas a alias.
- **Lista de "no unir":** nombres parecidos en lugares lejanos (`LOS OLIVOS` / `LOS OLIVARES`) son calles distintas; impiden que el fuzzy las fusione.
- **Dispersión espacial como prueba:** por cada `match_key`, si la dispersión es alta, o la regla une direcciones distintas o las coordenadas son malas.
- **Confusiones fonéticas reales:** medir qué pares (Z/S, V/B, LL/Y, H) aparecen en los datos antes de incluirlos en la clave fonética.

### Calibración de la confianza

Transformar el score en una probabilidad real de estar dentro del umbral de metros, por tipo de match (exacto, alias, fuzzy) y por distrito. Sin calibración, `confidence` es un número decorativo.

### Decisión de salida por riesgo

Se comparan tres señales independientes (coordenada resuelta, coordenada histórica, polígono del distrito) y el **margen** entre el mejor y el segundo candidato.

| Decisión interna | Condición típica | `status` en la API |
|---|---|---|
| `AUTO_ACCEPT` | Match exacto o alias confirmado, distrito coherente, coordenada cercana a la histórica, margen amplio | `RESOLVED` |
| `ACCEPT_FLAGGED` | Fuzzy con buen margen, o falta el número | `RESOLVED` (con flags y `precision_level` menor) |
| `REVIEW` | Conflicto entre señales, dos candidatos con score cercano, distrito contradictorio | `REVIEW_REQUIRED` (crea ticket) |
| `REJECT` | Dirección inutilizable o coordenada fuera de Lima | `UNRESOLVED` (con motivo) |

### Niveles de precisión

`ADDRESS_POINT` > `SEGMENT_INTERPOLATED` > `STREET` > `ZONE` (urbanización, manzana) > `DISTRICT`. Devolver "centroide de distrito con confianza alta" es más honesto que una coordenada aparentemente exacta.

### Bucle de producción

Las fuentes de mejora son el GPS de entrega, las correcciones manuales y los rechazos. Un alias solo se **promueve** con N confirmaciones independientes, baja dispersión espacial y sin fusionar dos calles canónicas. Antes de activar una versión nueva se corre en modo sombra contra la anterior; todo queda versionado para hacer rollback.

### Machine learning: solo si hace falta

Empezar con reglas y medir dónde fallan. Si el parseo se estanca, entrenar un etiquetador de tokens (por ejemplo CRF) sobre el dataset de oro. Un LLM puede ayudar **offline** como sugeridor de aliases y etiquetas que un humano revisa; nunca en el request.

---

## Apéndice D - Modelo de datos conceptual

```text
Department -> Province -> District/UBIGEO -> Zone -> Street -> Street Segment -> Address
```

| Entidad | Campos principales |
|---|---|
| `ubigeos` | Catálogo nacional: `id`, `code`, `department`, `province`, `district`, `normalized_name`, `polygon` |
| `ubigeo_aliases` | `ubigeo_id`, `alias`, `normalized_alias` |
| `coverage_zones` | `id`, `name`, `ubigeo_codes`, `status` (activa / sombra / apagada), `snapshot_version`, `golden_set_version` |
| `client_config` | `client_id`, `default_area`, `allowed_zones`, `mode` por defecto (se convierte en `tenants` completa en R2.0) |
| `streets` | `id`, `ubigeo_id`, `street_type`, `canonical_name`, `normalized_name`, `geom`, `source` |
| `street_aliases` | `street_id`, `alias`, `normalized_alias`, `source`, `confidence`, `status` (candidato / promovido) |
| `street_segments` | `street_id`, `geom` (LineString, SRID 4326), `start_number`, `end_number`, metadatos de tramo |
| `anchors` | `street_id`, `number`, `lat`, `lng`, `observation_id` (puntos verificados para interpolar) |
| `canonical_addresses` | `id`, `address_hash`, `street_id`, `house_number`, `normalized_address`, `normalizer_version` |
| `observations` | `canonical_address_id`, `lat`, `lng`, `method` (pin_operador / gps_entrega / historico / geocoder), `author`, `created_at`, `source_quality` |
| `review_tickets` | `id`, `raw_input`, `parse`, `candidates`, `priority`, `status`, `assigned_to` |
| `decision_events` | `ticket_id`, `user`, `action`, `before`, `after`, `created_at` (auditoría) |
| `resolution_events` | `request_id`, `external_id`, `decision`, `resolution_type`, `confidence`, `processing_ms`, `dataset_version`, `normalizer_version` |
| `tenants` (R2.0) | `id`, `name`; capa de aliases y observaciones propia de cada cliente sobre la capa global |

**Observaciones, no verdad única:** nunca se sobrescribe una coordenada. La vigente se calcula por prioridad de calidad: verificada por operador > GPS de entrega confirmada > histórico sin verificar. Esto permite auditar, revertir el error de un operador y detectar contradicciones.

**Jerarquía que se llena por demanda:** los distritos se cargan una vez; las calles se siembran con OSM y el histórico y se corrigen en el admin; los números no se registran todos a mano, se registran **puntos ancla** y el resto se calcula por interpolación (con menor `precision_level`).

---

## Apéndice E - Contrato de la API

### Endpoints

| Endpoint | Uso | Release |
|---|---|---|
| `POST /v1/normalize` | Normalización pura, sin geolocalización | MVP |
| `POST /v1/geocode` | Una dirección | MVP |
| `POST /v1/geocode/batch` | Lote síncrono, respuesta NDJSON | MVP |
| `POST /v1/jobs` (+ webhook) | Lotes grandes asíncronos | R1.1 |
| `POST /v1/feedback` | Confirmación o corrección con GPS de entrega | R1.1 |
| `GET /v1/autocomplete` | Sugerencias en formularios | R1.1 (interno) / R3.0 (público) |
| `POST /v1/reverse` | Coordenada → calle y distrito | R1.2 |
| `POST /v1/validate`, `/v1/match` | Validación y comparación de direcciones | R3.0 |

### Reglas del contrato

- `Idempotency-Key` en batches y jobs; `external_id` siempre devuelto para correlacionar con pedidos o guías.
- `mode=fast` (solo interno, SLA estricto) o `mode=extended` (permite fallback externo, SLA propio). No se mezclan en el mismo SLA.
- HTTP/2, gzip, keep-alive; gRPC no al inicio.

### Petición

```http
POST /v1/geocode
```

```json
{
  "external_id": "PED-001",
  "address": "Clle Los Saucez 245",
  "district": "Ate",
  "province": "Lima",
  "country": "PE",
  "mode": "fast"
}
```

Todos los campos de ubicación son opcionales y pueden ir combinados: `department`, `province`, `district` y `ubigeo`. También se aceptan concatenados dentro de `address`. La detección y las precedencias están en el Apéndice B (N5).

Estados y flags relacionados con la ubicación:

| Valor | Significado |
|---|---|
| `OUT_OF_SCOPE` (status) | La ubicación detectada está fuera de las zonas activas; se devuelve el `ubigeo` detectado |
| `DISTRICT_CONFLICT` (flag) | El campo y el texto indican distritos distintos |
| `AMBIGUOUS_LIMA` (flag) | No se pudo distinguir distrito, provincia y departamento con nombre repetido |
| `AREA_FROM_DEFAULT` (flag) | La ubicación se completó con el `default_area` del cliente |

### Respuesta

```json
{
  "external_id": "PED-001",
  "status": "RESOLVED",
  "decision": "ACCEPT_FLAGGED",
  "canonical_address": "CALLE LOS SAUCES 245",
  "ubigeo": "150103",
  "area_source": "FIELD",
  "street_id": "ST-729183",
  "location": { "lat": -12.03, "lng": -76.91 },
  "precision_level": "SEGMENT_INTERPOLATED",
  "confidence": 0.97,
  "resolution_type": "FUZZY_MATCH",
  "flags": ["PHONETIC_CORRECTION"],
  "source": "INTERNAL",
  "normalizer_version": "1.0.0",
  "dataset_version": "2026-09-01.3",
  "processing_ms": 3
}
```

Cuando `status` es `REVIEW_REQUIRED`, la respuesta incluye `review_id` y, opcionalmente, `candidates`.

---

## Apéndice F - Stack tecnológico

| Componente | Recomendación | Nota |
|---|---|---|
| Resolver | Go, o Java/Spring | El rendimiento sale del diseño en RAM más que del lenguaje. Go ofrece poco consumo de memoria y arranque rápido; Java/Spring reduce el riesgo si quien construye ya lo domina. Se decide en F0 |
| Reglas de normalización | YAML versionado + librería pura | Una sola implementación |
| Admin | React + TypeScript + MapLibre GL | Mapa base con licencia que permita guardar las coordenadas |
| Datos | PostgreSQL + PostGIS | `pg_trgm` solo en el admin y la minería offline |
| Snapshots | Almacenamiento de objetos + manifiesto versionado | Checksum y rollback |
| Jobs | Cola en PostgreSQL al inicio | Redis o SQS cuando haga falta |
| Redis | Fuera del camino caliente | Idempotencia, rate limit distribuido, estado de jobs |
| API | REST/JSON sobre HTTP/2 | NDJSON para batch |
| Observabilidad | OpenTelemetry + métricas p50/p95/p99 + logs estructurados | |
| Despliegue | Contenedores en servicio administrado, sin Kubernetes | Elegir según la cuenta y los costos existentes |

**Evitar al inicio:** Kubernetes por defecto, Kafka sin un caso real de eventos, Elasticsearch/OpenSearch antes de agotar el índice en RAM + PostgreSQL, microservicios por componente.

---

## Apéndice G - Integraciones como servicio

| Modalidad | Para qué | Release |
|---|---|---|
| REST `/normalize` y `/geocode` | Sistemas en línea (ERP, OMS, WMS, TMS, apps) | MVP |
| Batch síncrono | Imposiciones horarias de 1,000+ pedidos | MVP |
| Carga CSV/Excel desde el admin | Reemplaza el proceso manual actual | MVP |
| Jobs + webhooks | Lotes grandes sin bloquear al cliente | R1.1 |
| `/feedback` con GPS de entrega | Cierra el bucle de datos | R1.1 |
| Autocomplete y validación en formularios | Evita mala data en el origen (< 30 ms desde un índice de prefijos en RAM) | R1.1 |
| SFTP | Sistemas legacy | R1.2 |
| Reverse geocode | Punto → calle y distrito, en memoria | R1.2 |
| Eventos (Kafka, RabbitMQ, SQS) | Empresas orientadas a eventos; solo si un cliente lo pide | R1.2 |
| SDKs con normalización local opcional | Java, .NET, Node/TypeScript; reducen la latencia de red | R1.2 |
| Conectividad privada / on-premise | Enterprise, por seguridad o mantenimiento | R3.0 |

Flujo por eventos, cuando exista: `OrderCreated` → Address Service → `AddressResolved` → WMS / TMS / planificador de rutas / tracking. La dirección se resuelve una sola vez y el resultado se reutiliza.

---

## Apéndice H - Seguridad, privacidad y operación

- API keys en el MVP; OAuth2 Client Credentials más adelante. TLS obligatorio, WAF, rate limiting por cliente.
- Una dirección asociada a una persona es dato personal (Ley 29733): logs mínimos, retención definida, acceso restringido y anonimización donde se pueda. No enviar direcciones reales a terceros (incluidos LLM) sin revisar sus condiciones de uso de datos.
- Idempotencia en batches y jobs; timeouts y circuit breaker para proveedores externos; retries controlados.
- Auditoría de todas las correcciones manuales (quién, cuándo, antes y después).
- Estabilidad de resultados: definir si un pedido ya despachado conserva su resultado congelado o se reprocesa cuando cambia la versión del dataset.
- Operación: runbook de rollback de snapshot, degradación controlada, respuesta ante incidentes.

---

## Apéndice I - Métricas

### Por cada resolución se registra

`request_id`, `external_id`, `processing_ms`, `cache_level`, `resolution_type`, `decision`, `confidence`, `precision_level`, `source`, `ubigeo_resolved`, `street_resolved`, `external_fallback_used`, `dataset_version`, `normalizer_version`.

### Normalizador

Cobertura de parseo, idempotencia, tasa de tokens desconocidos, % de direcciones con flags.

### Resolución

Exactitud a 50/100/300 m por nivel de precisión, cobertura automática, error en `AUTO_ACCEPT` (por muestreo aleatorio), curva riesgo-cobertura. Mirar percentiles altos (p95, p99) y el error **por distrito**; un promedio esconde los casos graves.

### Datos y operación

Dispersión espacial por clave, aliases promovidos y rechazados, tamaño de la cola de revisión y su tendencia, tiempo por caso, acuerdo entre operadores en muestras doble ciegas.

### Dashboard

p50/p95/p99, throughput, cache hit ratio, % exacto / fuzzy / externo, tasa de no resueltos, precisión por ubigeo, top aliases desconocidos y costo por 1,000 resoluciones.

---

## Conclusión

El objetivo no es replicar Google Maps: es resolver bien el problema de las **direcciones peruanas inconsistentes**, con baja latencia, un contrato de integración simple y un error medido y acotado. El camino es normalizar de forma determinística, resolver contra un índice en memoria, abstenerse cuando hay duda, y convertir cada corrección humana en un dato verificado que hace al servicio más preciso y barato con el tiempo.

El benchmark y la evaluación sobre el dataset de oro, con direcciones reales de la operación, son el criterio para decidir cuándo pasar a la siguiente fase y cuándo introducir cualquier componente adicional.
