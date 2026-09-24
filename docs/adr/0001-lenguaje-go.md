# ADR 0001 - Lenguaje del núcleo: Go

**Estado:** propuesto (se confirma o cambia al cerrar la Fase 0)

**Contexto.** El resolver debe responder desde memoria con baja latencia, arrancar rápido y consumir poca memoria. El volumen inicial es pequeño (ráfagas de ~1,000 direcciones por hora), así que el lenguaje no es el cuello de botella: lo es el diseño (índices en RAM, sin red en el camino caliente).

**Alternativas.** Java/Spring (más cercano a la experiencia previa; más memoria y arranque más lento), TypeScript/Node (una sola lengua con el admin; hilo único para trabajo de CPU).

**Decisión.** Go, solo con biblioteca estándar en esta etapa.

**Consecuencias.** Las reglas viven en JSON (`data/rules`) y los casos de prueba en `testdata/normalize_cases.json`, ambos independientes del lenguaje. Si al cerrar la F0 se prefiere otro lenguaje, portar el normalizador es un trabajo acotado y se valida con los mismos casos.
