# Arranque desde cero: qué necesitas y en qué orden

No hay histórico de direcciones ni de coordenadas. Eso cambia tres cosas del plan:

1. **No existe una línea base de error que medir.** Se define el objetivo con la operación y se mide contra una muestra verificada.
2. **Las calles salen de fuentes públicas** (OpenStreetMap) y no de un histórico.
3. **El admin con mapa pasa a ser la fuente principal de verdad**, no un complemento: los datos verificados los producen los operadores. Conviene empezarlo antes.

La dependencia más importante es una muestra de direcciones reales y sucias. Sin ella se puede construir todo, pero no se puede medir la exactitud real (ver sección 4).

## 1. Herramientas

| Herramienta | Para qué | Cuándo |
|---|---|---|
| Go 1.22+ y Git | Núcleo del servicio | Ya |
| Un editor con soporte de Go (VS Code + extensión de Go) | Desarrollo | Ya |
| QGIS | Ver polígonos, calles y puntos; verificar coordenadas a mano | Semana 1 |
| `osmium-tool` (o la API Overpass) | Recortar Lima del extracto de OpenStreetMap | Semana 1-2 |
| Docker | PostgreSQL + PostGIS locales | Fase 2 |
| PostgreSQL 16 + PostGIS 3 | Fuente de verdad y consultas espaciales | Fase 2 |
| Node 20+ | Admin (React + TypeScript + MapLibre GL) | Fase 3 |
| Cuenta en una nube | Despliegue | Fase 4 |

## 2. Datos: fuentes y cuidados

Las fuentes son sugerencias de dónde buscar; confirma la disponibilidad, la versión y la licencia de cada una antes de usarla.

| Dato | Fuente sugerida | Para qué | Cuidado |
|---|---|---|---|
| Ubigeos oficiales (departamento, provincia, distrito) | INEI (clasificador de ubigeos) | Hecho: `docs/ref/UBIGEO 2022_1891 distritos.xlsx` → `make catalog` → `data/catalog/ubigeos.json` | Hay distritos creados después de 2022: agregarlos en `ubigeos_seed.json` y regenerar |
| Límites distritales (polígonos) | INEI/IGN o la plataforma de datos abiertos del Estado; alternativa: límites administrativos en OpenStreetMap | Validar que un pin cae en el distrito correcto | Actualidad y licencia |
| Calles de Lima | OpenStreetMap (extracto de Perú de Geofabrik, o Overpass para Lima) | Sembrar `streets` con geometría | Licencia ODbL (atribución y condiciones sobre bases derivadas); cobertura desigual en la periferia |
| Puntos de dirección | Nodos de OpenStreetMap con `addr:street` y `addr:housenumber` | Puntos ancla iniciales con coordenada | Cobertura parcial |
| Direcciones reales y sucias | La operación propia | Medir exactitud real; alimentar el dataset de oro | Requiere autorización y quitar datos personales (Ley 29733): solo texto de dirección y distrito, sin nombres ni teléfonos |
| Geocoders comerciales | 2 o 3 proveedores | Comparar (línea base externa) | Respetar sus términos: muchos prohíben guardar o reutilizar sus resultados |

## 3. Dataset de oro desde cero

Tres pistas complementarias; la plantilla es `goldenset/template.csv`.

- **A. Sintético (se puede hacer ya).** A partir de calles y números de OSM se genera la dirección canónica y se le aplican transformaciones de ruido: abreviaturas (`Clle`, `Av.`), tildes, `#`/`Nº`, el distrito concatenado, errores de tipeo, Mz/Lt. Sirve como regresión del normalizador. **No mide el error real**, porque los errores inventados no son los errores humanos reales.
- **B. Puntos de dirección de OSM (semana 1-2).** Nodos con `addr:*` en Lima dan direcciones reales con coordenada. Sirven de puntos ancla y de verdad de referencia parcial en las zonas con cobertura.
- **C. Direcciones reales (la que importa).** Entre 100 y 300 direcciones de la operación, anonimizadas, con parseo esperado y coordenada verificada por alguien independiente de quien la puso.

Reglas: partición por calle (no por fila), un test congelado que no se toca para ajustar reglas, estratificar por distrito y tipo de dirección (calle con número, Mz/Lt, urbanización) e incluir casos difíciles, no solo los que salen bien.

## 4. Línea base sin histórico

Sin coordenadas históricas no hay "error actual". Lo que sí se puede hacer:

1. Acordar con la operación el costo del error y el error máximo aceptado por nivel de precisión (valores de partida en `docs/plan.md`).
2. Verificar de forma independiente la muestra C. Con dos personas revisando sobre imagen satelital se obtiene una referencia razonable.
3. Pasar la misma muestra por 2 o 3 geocoders comerciales y medir error y costo por 1,000 consultas. Esa es la línea base contra la que compite el servicio.

Si no se consigue ninguna muestra real, el proyecto puede avanzar con A y B, pero hay que dejar por escrito que la exactitud real queda **sin medir** hasta que exista una muestra.

## 5. Las primeras dos semanas

**Semana 1**
- Día 1: `git init`, `make test`, leer `docs/plan.md` y los ADRs. Decidir el nombre del proyecto.
- Día 2: instalar QGIS. Bajar el catálogo de ubigeos del INEI y compararlo con el seed (distritos, códigos, nombres). Corregir el seed o reemplazarlo.
- Día 3-4: bajar el extracto de OSM de Perú, recortar Lima y abrirlo en QGIS. Contar calles con nombre por distrito y ver dónde hay huecos. Contar nodos con `addr:housenumber`.
- Día 5: llenar las primeras 100 filas del dataset de oro (mezcla de tipos de dirección, sin coordenadas todavía). Empezar a pedir la muestra real y la autorización de uso.

**Semana 2**
- Cada dirección que se normalice mal se convierte en un caso nuevo de `testdata/normalize_cases.json` y luego se corrige.
- Ampliar `data/rules/lexicon.json` con las abreviaturas y marcadores que aparezcan.
- Cargar los polígonos distritales y reemplazar la semilla de ubigeos.
- Escribir el resumen de la Fase 0: decisiones (lenguaje, región), origen y licencia de los datos, quién opera la cola de revisión, meta de error. Eso cierra la compuerta G0.

## 6. Qué no hacer todavía

- No construir el admin antes de tener el modelo de datos (Fase 2) definido.
- No integrar un geocoder externo en el camino de respuesta.
- No optimizar el rendimiento del normalizador: hoy está muy por debajo del presupuesto de latencia.
- No aceptar el catálogo semilla como oficial.
