# ADR 0005 - De dónde sale la verdad de las coordenadas

**Estado:** propuesto

**Contexto.** Para medir el error del resolver en metros hace falta saber dónde queda de verdad cada dirección. EMS-D no guarda coordenadas de las direcciones de los pedidos: en la muestra de producción hubo 0 de 600. Las únicas coordenadas disponibles hoy son las de OpenStreetMap, y su ruido ya está en el orden del umbral que hay que medir. Cuando OSM tiene la misma calle y el mismo número dos veces a menos de 2 km, la separación mediana es de 34 m, el 39 % supera los 50 m y el 14 % supera los 150 m. Gran parte de eso es la diferencia entre el centro del edificio y la puerta, pero también hay errores de numeración.

**Decisión.**

1. **Las coordenadas de OSM son "bronce".** Sirven como anclas de interpolación y como referencia de desarrollo (`cmd/geoeval`), pero no para declarar que se cumple la meta de error del MVP (1-2 % en `AUTO_ACCEPT`).
2. **La fuente principal de verdad será el GPS de entrega de la app de EMS-D.** En `bd_backoffice`, la tabla `SEGUIMIENTO_ENTIDAD` registra eventos de la app con latitud y longitud ("Llegada a Punto", "Entrega Exitosa"), y esos eventos se propagan al pedido, que es el que tiene la dirección.
3. **Un punto GPS solo es "oro" si pasa estas validaciones:**
   - el punto de "Entrega Exitosa" está a menos de 50 m del de "Llegada a Punto" del mismo pedido;
   - cae dentro del distrito del ubigeo del pedido;
   - la misma dirección normalizada tiene al menos dos entregas en días distintos que coinciden (a 30 m o menos).
   Las demás observaciones quedan como "plata" (Apéndice C).
4. **Las observaciones nunca se sobrescriben** (tabla `observations`): la coordenada vigente se calcula según la calidad de la fuente.

**Consecuencias.**

- Hasta tener una muestra de entregas validadas, el error de `AUTO_ACCEPT` se reporta como referencia contra OSM y no como cumplimiento de la compuerta G2.
- Extraer el GPS de entrega requiere la misma autorización de uso de datos que la muestra de direcciones. Solo se sacan el punto, la fecha y el pedido (seudonimizado), nunca datos del destinatario.
- El admin (Fase 3) sigue siendo necesario para los casos sin entregas, como las direcciones nuevas o las entregas fallidas.
