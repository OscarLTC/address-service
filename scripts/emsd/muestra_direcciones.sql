-- Muestra de direcciones reales de EMS-D (bd_backoffice) para la pista C del
-- dataset de oro: cómo llegan las direcciones de los clientes.
--
-- Solo saca campos de dirección. NO saca nombre, documento, teléfono ni correo del
-- remitente o del destinatario, ni números de pedido o tracking. Además enmascara
-- en el texto secuencias de 7 o más dígitos (teléfonos, DNI) y correos.
--
-- Ejecutar contra producción, con la salida a data/private/ (no se versiona):
--
--   psql "$BACKOFFICE_PRD" -v ON_ERROR_STOP=1 --csv -f scripts/emsd/muestra_direcciones.sql \
--     > data/private/emsd_muestra.csv
--
-- Parámetros (se cambian aquí abajo): ventana de días, tope por cuenta y tope total.

WITH params AS (
    SELECT 90  AS dias,          -- antigüedad máxima de los pedidos
           40  AS por_cuenta,    -- tope de direcciones por cuenta (estratificación)
           600 AS total          -- tope de la muestra
),
pedidos AS (
    -- La dirección del cliente: destino en logística directa, origen en inversa.
    SELECT p."ID_PEDIDO",
           p."ID_CUENTA",
           p."ID_CANAL_VENTA",
           p."TIPO_LOGISTICA"::text AS tipo_logistica,
           CASE WHEN p."TIPO_LOGISTICA"::text = 'LOGISTICA_INVERSA' THEN 'ORIGEN' ELSE 'DESTINO' END AS lado,
           p."FECHA_CREACION",
           CASE WHEN p."TIPO_LOGISTICA"::text = 'LOGISTICA_INVERSA' THEN p."DIRECCION_ORIGEN"         ELSE p."DIRECCION_DESTINO"         END AS direccion,
           CASE WHEN p."TIPO_LOGISTICA"::text = 'LOGISTICA_INVERSA' THEN p."NUMERO_DIRECCION_ORIGEN"  ELSE p."NUMERO_DIRECCION_DESTINO"  END AS numero,
           CASE WHEN p."TIPO_LOGISTICA"::text = 'LOGISTICA_INVERSA' THEN p."REFERENCIA_ORIGEN"        ELSE p."REFERENCIA_DESTINO"        END AS referencia,
           CASE WHEN p."TIPO_LOGISTICA"::text = 'LOGISTICA_INVERSA' THEN p."GEO_DISTRITO_ORIGEN"      ELSE p."GEO_DISTRITO_DESTINO"      END AS distrito,
           CASE WHEN p."TIPO_LOGISTICA"::text = 'LOGISTICA_INVERSA' THEN p."GEO_PROVINCIA_ORIGEN"     ELSE p."GEO_PROVINCIA_DESTINO"     END AS provincia,
           CASE WHEN p."TIPO_LOGISTICA"::text = 'LOGISTICA_INVERSA' THEN p."GEO_DEPARTAMENTO_ORIGEN"  ELSE p."GEO_DEPARTAMENTO_DESTINO"  END AS departamento,
           CASE WHEN p."TIPO_LOGISTICA"::text = 'LOGISTICA_INVERSA' THEN p."UBIGEO_ORIGEN"            ELSE p."UBIGEO_DESTINO"            END AS ubigeo,
           CASE WHEN p."TIPO_LOGISTICA"::text = 'LOGISTICA_INVERSA' THEN p."LATITUD_ORIGEN"           ELSE p."LATITUD_DESTINO"           END AS lat,
           CASE WHEN p."TIPO_LOGISTICA"::text = 'LOGISTICA_INVERSA' THEN p."LONGITUD_ORIGEN"          ELSE p."LONGITUD_DESTINO"          END AS lng
    FROM "PEDIDO" p, params
    WHERE p."ESTADO" = 'A'
      AND p."FECHA_CREACION" >= now() - make_interval(days => params.dias)
),
limpios AS (
    -- Enmascara teléfonos/DNI (7+ dígitos seguidos) y correos dentro del texto libre.
    SELECT pd.*,
           regexp_replace(regexp_replace(coalesce(pd.direccion, ''),  '[^[:space:]@]+@[^[:space:]@]+', '<CORREO>', 'g'), '[0-9]{7,}', '<NUM>', 'g') AS direccion_m,
           regexp_replace(regexp_replace(coalesce(pd.referencia, ''), '[^[:space:]@]+@[^[:space:]@]+', '<CORREO>', 'g'), '[0-9]{7,}', '<NUM>', 'g') AS referencia_m
    FROM pedidos pd
    WHERE btrim(coalesce(pd.direccion, '')) <> ''
),
unicos AS (
    -- Una fila por dirección distinta dentro de cada cuenta (los clientes repiten).
    SELECT DISTINCT ON (l."ID_CUENTA", lower(btrim(l.direccion_m)), coalesce(l.numero, ''), coalesce(l.ubigeo, ''))
           l.*
    FROM limpios l
    ORDER BY l."ID_CUENTA", lower(btrim(l.direccion_m)), coalesce(l.numero, ''), coalesce(l.ubigeo, ''), md5(l."ID_PEDIDO")
),
estratificados AS (
    -- Orden pseudoaleatorio pero reproducible (md5 del id), con tope por cuenta.
    SELECT u.*,
           row_number() OVER (PARTITION BY u."ID_CUENTA" ORDER BY md5(u."ID_PEDIDO")) AS rn_cuenta
    FROM unicos u
)
SELECT left(md5(e."ID_PEDIDO"), 12)          AS id_muestra,
       e."ID_CUENTA"                         AS cuenta,
       e."ID_CANAL_VENTA"                    AS canal_venta,
       e.tipo_logistica,
       e.lado,
       to_char(e."FECHA_CREACION", 'YYYY-MM') AS mes,
       e.direccion_m                         AS direccion,
       e.numero,
       e.referencia_m                        AS referencia,
       e.distrito,
       e.provincia,
       e.departamento,
       e.ubigeo,
       round(e.lat::numeric, 6)              AS lat,
       round(e.lng::numeric, 6)              AS lng
FROM estratificados e, params
WHERE e.rn_cuenta <= params.por_cuenta
ORDER BY md5(e."ID_PEDIDO")
LIMIT (SELECT total FROM params);
