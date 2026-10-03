# Muestra real de EMS-D: cómo llegan las direcciones

Este documento resume la primera muestra real (pista C), tomada el 2026-10-01 con `scripts/emsd/muestra_direcciones.sql` sobre `bd_backoffice` de producción. Solo contiene cifras agregadas. La muestra está en `data/private/`, no se versiona y no se copian direcciones de ella al repositorio, tampoco como casos de prueba: los casos de `testdata/` que imitan sus patrones usan calles y números inventados.

## La muestra

- 600 direcciones distintas de 38 cuentas, de los últimos 90 días, con un máximo de 40 por cuenta.
- El 80 % es de Lima Metropolitana y el 4 % del Callao. El resto viene de otras regiones (Arequipa, La Libertad, Piura, Junín…).
- Casi todas son logística directa (594). Solo 6 son de recojo (inversa).

## Lo que trae cada pedido

| Dato | Presencia |
|---|---:|
| Ubigeo de 6 dígitos | 100 % |
| Distrito, provincia y departamento en texto | 100 % |
| Referencia | 46 % |
| Número de puerta en un campo aparte | 3 % (en UAT era 36 %) |
| Coordenadas | 0 % |

No hay histórico de coordenadas, así que se confirma la variante del plan que arranca sin histórico.

## Hallazgos sobre el texto

- **El distrito suele repetirse al final del texto sin coma** ("... 118 2 piso San Martin de Porres"). Es mucho más frecuente que una urbanización cuyo nombre termina en un distrito.
- **Los nombres de urbanizaciones coinciden con distritos de otras regiones** (una urbanización de Ate que se llama como un distrito de Arequipa, por ejemplo). Sin campos de ubicación, eso produce ubigeos equivocados desde el texto (2.5 % de la muestra en modo solo texto).
- **Aparecen dos números seguidos sin marcador** ("610 204"): el primero es la puerta y el segundo el interior (7 % de la muestra).
- **Hay basura al final**: ", Perú", el ubigeo de 6 dígitos o el código postal de 5.
- **El 31 % no trae tipo de vía** y la calle llega solo con su nombre.
- **El 22 % deja texto libre después del número**: edificio, torre, "casa", indicaciones. Ese texto se mueve a la referencia.
- **Hay textos duplicados**, como la dirección repetida dos veces en el mismo campo, y prefijos que no son dirección ("COMPRA Y RECOGE - ...", "Delivery").

## Hallazgos sobre los datos de EMS-D

- **El campo distrito llega truncado a 20 caracteres** ("SAN JUAN DE MIRAFLOR", "PUEBLO LIBRE (MAGDAL", "VEINTISEIS DE OCTUBR"). El normalizador ahora lo resuelve por prefijo dentro de la provincia, con el flag `DISTRICT_FIELD_PARTIAL`.
- **El ubigeo `200116` no existe en el catálogo del INEI 2022.** El nombre que lo acompaña corresponde a `200115` (Veintiséis de Octubre, Piura). Hay que revisar la tabla `UBIGEO` de EMS-D.
- **"NAZCA" frente a "NASCA"**: el INEI escribe "NASCA". Falta un alias de provincia (fuera de la zona activa).
- **En el 1.7 % de los pedidos, el distrito escrito en el texto contradice el del campo.** El normalizador lo marca con `DISTRICT_CONFLICT` y respeta el campo.

## Ubicación con `normalizer/0.6.0`

| Modo | Ubigeo igual al de EMS-D | Abstención | Distinto |
|---|---:|---:|---:|
| Con campos de ubicación (como llama EMS-D) | 99.3 % | 0.5 % | 0.2 % (es el caso `200116`) |
| Solo texto | 20.2 % | 77.3 % | 2.5 % |

En modo solo texto, la mayoría de las direcciones no trae el distrito escrito, por eso la abstención es alta. Ese modo no es el de EMS-D, pero muestra que un cliente que solo mande texto necesita `default_area` o revisión.

## Etiquetado de 200 direcciones (2026-10-03)

Se etiquetaron 200 direcciones de Lima y Callao con `scripts/emsd/planilla_etiquetado.py`. **La revisión la hizo un LLM (ChatGPT), no una persona**, así que estas etiquetas son "plata": sirven para encontrar fallos y medir el avance, no para declarar la exactitud real. Quedan 184 evaluables: 97 correctas y 87 corregidas. Otras 14 son dudosas y 2 no son direcciones. La revisión siguió la instrucción de la planilla, que tenía dos huecos: no contemplaba `S/N` (se restauró al importar) y llevó a suponer "DPTO" para interiores sin marcador.

| Normalizador | G1 dev (138) | G1 test (46) | Ubigeo equivocado |
|---|---:|---:|---:|
| 0.6.0 | 52.9 % | 54.3 % | 2 en dev |
| 0.7.0 | 68.1 % | 58.7 % | 0 |

Los cambios de 0.7.0 salieron de los fallos de **dev**. La partición test se usa solo para medir: que suba menos que dev indica cuánto se ajustó mirando dev.

Lo que más pesa en los fallos que quedan:
- **El interior sin marcador** ("315 708"): la etiqueta dice "DPTO 708" y el normalizador "708". Es una convención que hay que decidir.
- **El nombre de la vía que arrastra texto libre**: edificios, locales, indicaciones.
- **Ambigüedades reales** que necesitan un criterio humano, como dos números seguidos separados por coma o guion.

## Pendiente

- Etiquetar a mano el parseo esperado de un subconjunto (100 a 300) para medir la exactitud real del parseo. Hoy solo se compara la ubicación.
- Agregar `number` y `reference` como campos de entrada de `/v1/normalize`, porque EMS-D los tiene separados.
- Atender el piso escrito antes del marcador ("2 piso"), los prefijos que no son dirección y los textos duplicados.
- Revisar la discrepancia `200116` con el equipo de EMS-D.
