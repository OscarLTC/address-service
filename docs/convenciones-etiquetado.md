# Convenciones de etiquetado

Este documento define el **parseo esperado** de una dirección, para el dataset de oro y para la planilla de etiquetado. La pregunta que responde cada regla es qué debe extraer el normalizador **a partir del texto escrito**, no cuál es la dirección "verdadera". Corregir nombres o adivinar datos que el texto no trae es trabajo de la resolución (ADR 0004).

## Formato general

- MAYÚSCULAS, sin tildes, con la Ñ conservada. Sin puntos ni comas.
- Si un campo no aparece en el texto, va vacío. No se infiere.
- Si el texto llega corrupto desde el origen (caracteres `�`, `Ã±`), la fila se marca como **DUDOSO** con el motivo "texto corrupto en origen". Esas filas no entran en la métrica: miden la calidad del origen, no la del parser.

## Vía y número

| Campo | Regla |
|---|---|
| Tipo de vía | Canónico (AVENIDA, JIRON, CALLE, PASAJE…). Vacío si el texto no lo dice. |
| Nombre de vía | Tal como está escrito, sin el tipo ni el número. Títulos expandidos (GRAL → GENERAL). |
| Número | Dígitos y, si existe, una letra pegada (245A). `S/N` cuando el texto dice "S/N" o "SN". |
| Número repetido | "912 912": es el mismo número repetido, no un interior. |
| Varios números | El número de puerta es el que sigue al nombre de la vía. Cifras dentro de descripciones ("casa de 2 pisos", "paradero 90") no lo son. |
| Establecimiento antes de la vía | "Plaza Norte - Av. X": la vía es la que lleva tipo (AVENIDA X). El establecimiento no es la vía. |

## Interior

- Se escriben todas las unidades que trae el texto, **en el orden del texto**, con su marcador canónico: `TORRE 3 DPTO 504`, `DPTO 904 TORRE 1`, `TDA 26A`, `PISO 2`.
- Marcadores canónicos: DPTO (dpto, dep, dp, dpt, apt, apto, apartamento, departamento), INT, OF, PISO (piso, nivel), TDA, STAND, TORRE, CASA (solo con valor: "casa C", "casa 7"), SOTANO, LOCAL.
- **Un número suelto después de la puerta, sin marcador ("Calle Los Almendros 315 708"), se escribe tal cual: `708`.** No se agrega DPTO, porque el texto no lo dice.
- El piso con ordinal se escribe con dígito: "primer piso", "1er piso" y "2do nivel" pasan a `PISO 1` o `PISO 2`.
- Una unidad repetida se escribe una vez.
- "Casa" sin valor, o con una descripción ("casa de 2 pisos gris"), es referencia, no interior.

## Manzana, lote y urbanización

- Mz y Lt llevan solo el valor (`B`, `C2`, `80B`). Si la letra viene separada del lote ("Lote 02 C"), va pegada: `02C`.
- Un valor repetido ("Lote 12, 12") se escribe una vez.
- La urbanización lleva el marcador canónico más el nombre: URBANIZACION, ASENTAMIENTO HUMANO, PUEBLO JOVEN, ASOCIACION, ASOCIACION PRO VIVIENDA, COOPERATIVA, RESIDENCIAL, CONDOMINIO, SECTOR, ZONA, GRUPO.
- Etapa, sector y grupo van dentro de la urbanización, en el orden del texto y con el ordinal como dígito: `ASOCIACION LOS ALAMOS 3 ETAPA`.
- Si la etapa aparece **antes** del marcador ("Etapa 2 Urb. Las Lomas"), va al final de la urbanización: `URBANIZACION LAS LOMAS ETAPA 2`.
- **El distrito escrito al final del nombre de una urbanización es la ubicación, no parte del nombre**, cuando coincide con el distrito del pedido: "Urb. Los Rosales-Comas" queda como `URBANIZACION LOS ROSALES`. Si nombra **otro** distrito ("Urb. Los Robles Salamanca", en Ate), es parte del nombre.
- Un lugar sin marcador después de Mz/Lt ("Mz H Lote 4 La Esperanza II") se escribe en urbanización sin marcador: `LA ESPERANZA II`.

## Ubigeo

Por defecto es el del pedido. Si el campo dice LIMA o CALLAO (que suele significar la provincia) y el texto nombra un distrito concreto de esa provincia, gana el del texto.

## Estados

| Estado | Cuándo |
|---|---|
| CORRECTO | La propuesta coincide con estas reglas. |
| CORREGIDO | Se corrigió algún campo. |
| DUDOSO | No se puede decidir con el texto: hace falta conocer el lugar o el texto está corrupto. Se explica en el comentario. |
| NO ES DIRECCIÓN | Texto sin dirección utilizable (solo el distrito, coordenadas, un nombre de tienda). |
