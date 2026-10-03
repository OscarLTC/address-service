# Dataset de oro

El formato de las columnas está en `template.csv`. Cada fila trae una dirección de entrada y el parseo esperado: tipo de vía, nombre, número, Mz/Lt, urbanización y ubigeo.

## `golden_v1.csv`

Se generó con `make golden` a partir de los puntos de OpenStreetMap que tienen `addr:street` y `addr:housenumber`, en Lima Metropolitana y el Callao. Toma hasta 20 direcciones base por distrito y como máximo 2 por calle. Tiene dos pistas:

| Fuente (`source`) | Qué es | Para qué sirve |
|---|---|---|
| `osm_addr` | La dirección tal como está en OSM, con el distrito en un campo aparte | Verdad de referencia parcial; su coordenada sirve como punto ancla |
| `synthetic` | Las mismas direcciones con ruido realista: abreviaturas, mayúsculas, tildes, `#`/`N°`/`Nro.`, alias de distrito, ubicación concatenada o en campos, interior | Regresión del normalizador |

En la columna `notes` de cada fila sintética se listan las transformaciones aplicadas. `goldeneval` reporta la exactitud por cada una.

El parseo esperado sale de las etiquetas de OSM y el ubigeo esperado sale del polígono distrital que contiene el punto (`data/geo/districts.json`). **Ninguno de los dos sale del normalizador**, para que la evaluación no sea circular.

## `golden_mzlt_v2.csv` (y `golden_mzlt_v1.csv`, histórico)

Esta es la pista de manzana y lote (`source = synthetic_mzlt`). Son direcciones sin número de puerta, armadas con Mz/Lt y el nombre real de una urbanización, AA.HH., asociación, cooperativa, condominio o sector tomado de OpenStreetMap: hasta 15 por distrito y 2 variantes por área. Se combinan en cuatro órdenes (`Mz Lt Urb`, `Urb Mz Lt`, `Vía Mz Lt Urb`, `Urb Vía Mz Lt`) y se escriben con las formas que se usan en Lima (`Mza.`, `MzB`, `Lote`, `AA.HH.`, `A.H.`, `P.J.`, `Asoc.`…). Algunas filas llevan una referencia entre paréntesis.

- La urbanización esperada es el marcador canónico más el nombre. Los ordinales pasan a dígito solo antes de `Etapa`, `Sector`, `Zona` o `Grupo`, así que "Primera Etapa" se vuelve "1 ETAPA" pero "Quinta Heren" se queda como está.
- Se descartan los nombres que cambiarían el parseo de verdad: los que coinciden con un distrito del país, los que contienen una unidad, una referencia, una vía o un marcador abreviado, y los que llevan paréntesis.
- La coordenada es el centro del área (`osm_area_center`), es decir, precisión de zona y no de puerta.
- La v2 excluye además los nombres que contienen su propio distrito sin un conector delante ("Unidad La Perla" en La Perla). Según `docs/convenciones-etiquetado.md`, ese distrito es la ubicación, así que el nombre no sirve de verdad. La v1 se conserva sin cambios como histórico; el evaluador usa la v2.

## Partición y test congelado

- La partición es **por calle**, y en Mz/Lt **por área**: todas las direcciones de una calle o de una urbanización quedan en `dev` o todas en `test`. Así no se premia memorizar nombres.
- `test` queda **congelado**. `goldengen` no sobrescribe un dataset existente salvo con `-force`. Los datos de OSM cambian, así que una nueva generación se publica como una versión nueva (`golden_v2.csv`) y no reemplaza la anterior.
- `goldeneval` muestra ejemplos de fallos solo de `dev`. De `test` solo da cifras agregadas: las reglas se ajustan mirando `dev`.

## Límites (léelos antes de citar una cifra)

- **Esto no mide la exactitud real.** El ruido es inventado y los errores humanos reales son otros. Solo la muestra de direcciones reales de la operación (pista C de `docs/SETUP.md`) mide el error verdadero.
- **Las direcciones con Mz/Lt también son sintéticas.** Los nombres de las áreas son reales, pero las combinaciones de manzana y lote son inventadas, y faltan formas reales como la ubicación dentro de la referencia ("Ref. ..., SJL") o la manzana sin lote.
- **La cobertura de OSM es desigual.** Los distritos de la periferia sur (Pucusana, Santa María del Mar, Punta Negra) tienen muy pocos puntos.
- **Las coordenadas no tienen verificación independiente** (`verification_method = osm_unverified`). No cuentan como coordenadas "oro" mientras nadie las verifique en el admin.
- **Licencia.** Los datos derivados de OSM son ODbL: hay que conservar la atribución "© OpenStreetMap contributors".

## Evaluar

```bash
make eval   # evalúa ambos archivos; termina con error si la métrica G1 en test baja de 95 %
```

La métrica G1 cuenta una fila como correcta solo si **todos** los campos esperados son correctos a la vez: tipo de vía, nombre, número, Mz, Lt, urbanización y ubigeo. Aparte, el reporte separa el **ubigeo equivocado**, que es el error peligroso, de la **abstención**: no devolver ubigeo ante una ambigüedad real es el comportamiento correcto.
