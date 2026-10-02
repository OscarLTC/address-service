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

## Partición y test congelado

- La partición es **por calle**: todas las direcciones de una calle quedan en `dev` o todas en `test`. Así no se premia memorizar calles.
- `test` queda **congelado**. `goldengen` no sobrescribe un dataset existente salvo con `-force`. Los datos de OSM cambian, así que una nueva generación se publica como una versión nueva (`golden_v2.csv`) y no reemplaza la anterior.
- `goldeneval` muestra ejemplos de fallos solo de `dev`. De `test` solo da cifras agregadas: las reglas se ajustan mirando `dev`.

## Límites (léelos antes de citar una cifra)

- **Esto no mide la exactitud real.** El ruido es inventado y los errores humanos reales son otros. Solo la muestra de direcciones reales de la operación (pista C de `docs/SETUP.md`) mide el error verdadero.
- **Faltan los casos más difíciles de Lima.** Casi todas las direcciones de OSM son calles formales con número. No hay Mz/Lt, AA.HH., urbanizaciones ni referencias.
- **La cobertura de OSM es desigual.** Los distritos de la periferia sur (Pucusana, Santa María del Mar, Punta Negra) tienen muy pocos puntos.
- **Las coordenadas no tienen verificación independiente** (`verification_method = osm_unverified`). No cuentan como coordenadas "oro" mientras nadie las verifique en el admin.
- **Licencia.** Los datos derivados de OSM son ODbL: hay que conservar la atribución "© OpenStreetMap contributors".

## Evaluar

```bash
make eval   # termina con error si la métrica G1 en test baja de 95 %
```

La métrica G1 cuenta una fila como correcta solo si el tipo de vía, el nombre, el número y el ubigeo son correctos a la vez. Aparte, el reporte separa el **ubigeo equivocado**, que es el error peligroso, de la **abstención**: no devolver ubigeo ante una ambigüedad real es el comportamiento correcto.
