-- GPS de entrega de EMS-D (bd_backoffice) como verdad de coordenadas (ADR 0005).
--
-- Una fila por pedido de logística directa en Lima y Callao con "Entrega Exitosa
-- Destino" (EMC) registrada desde la app: dirección del destino, punto de entrega y
-- punto de "Llegada a Punto" (LPE) para validar.
--
-- Solo saca campos de dirección y el punto GPS. NO saca nombre, documento, teléfono
-- ni correo, ni número de pedido o tracking (el pedido va seudonimizado). Enmascara
-- en el texto secuencias de 7 o más dígitos y correos.
--
--   psql "$BACKOFFICE_PRD" -v ON_ERROR_STOP=1 --csv -f scripts/emsd/gps_entregas.sql \
--     > data/private/emsd_gps.csv

WITH params AS (
    SELECT 90 AS dias            -- antigüedad máxima de las entregas
),
ev AS (
    SELECT s."ID_ENTIDAD"              AS id_pedido,
           tv."CODIGO_TIPO_EVENTO"     AS cod,
           s."LATITUD"                 AS lat,
           s."LONGITUD"                AS lng,
           s."FECHA_EVENTO"            AS fecha,
           row_number() OVER (PARTITION BY s."ID_ENTIDAD", tv."CODIGO_TIPO_EVENTO" ORDER BY s."FECHA_EVENTO" DESC) AS rn
    FROM "SEGUIMIENTO_ENTIDAD" s
    JOIN "TIPO_ENTIDAD" te ON te."ID_TIPO_ENTIDAD" = s."ID_TIPO_ENTIDAD" AND te."NOMBRE" = 'Pedido'
    JOIN "TIPO_EVENTO"  tv ON tv."ID_TIPO_EVENTO"  = s."ID_TIPO_EVENTO"
    CROSS JOIN params
    WHERE tv."CODIGO_TIPO_EVENTO" IN ('EMC', 'LPE')
      AND coalesce(s."LATITUD", 0) <> 0 AND coalesce(s."LONGITUD", 0) <> 0
      AND s."FECHA_EVENTO" >= now() - make_interval(days => params.dias)
),
entrega AS (SELECT * FROM ev WHERE cod = 'EMC' AND rn = 1),
llegada AS (SELECT * FROM ev WHERE cod = 'LPE' AND rn = 1)
SELECT left(md5(p."ID_PEDIDO"), 12)                                   AS id_muestra,
       p."ID_CUENTA"                                                  AS cuenta,
       regexp_replace(regexp_replace(coalesce(p."DIRECCION_DESTINO", ''),  '[^[:space:]@]+@[^[:space:]@]+', '<CORREO>', 'g'), '[0-9]{7,}', '<NUM>', 'g') AS direccion,
       p."NUMERO_DIRECCION_DESTINO"                                   AS numero,
       regexp_replace(regexp_replace(coalesce(p."REFERENCIA_DESTINO", ''), '[^[:space:]@]+@[^[:space:]@]+', '<CORREO>', 'g'), '[0-9]{7,}', '<NUM>', 'g') AS referencia,
       p."GEO_DISTRITO_DESTINO"                                       AS distrito,
       p."GEO_PROVINCIA_DESTINO"                                      AS provincia,
       p."GEO_DEPARTAMENTO_DESTINO"                                   AS departamento,
       p."UBIGEO_DESTINO"                                             AS ubigeo,
       to_char(e.fecha, 'YYYY-MM-DD')                                 AS fecha_entrega,
       round(e.lat::numeric, 6)                                       AS lat_entrega,
       round(e.lng::numeric, 6)                                       AS lng_entrega,
       round(l.lat::numeric, 6)                                       AS lat_llegada,
       round(l.lng::numeric, 6)                                       AS lng_llegada
FROM entrega e
JOIN "PEDIDO" p ON p."ID_PEDIDO" = e.id_pedido
LEFT JOIN llegada l ON l.id_pedido = e.id_pedido
WHERE p."ESTADO" = 'A'
  AND p."TIPO_LOGISTICA"::text = 'LOGISTICA_DIRECTA'
  AND left(p."UBIGEO_DESTINO", 4) IN ('1501', '0701')
  AND btrim(coalesce(p."DIRECCION_DESTINO", '')) <> ''
ORDER BY md5(p."ID_PEDIDO");
