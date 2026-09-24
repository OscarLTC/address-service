# ADR 0002 - Dos planos: datos y control

**Decisión.** El servicio se divide en un plano de datos (resolver: solo lectura, snapshot en RAM, sin llamadas a base de datos por consulta) y un plano de control (admin, workers, constructor de índices, PostgreSQL/PostGIS). Son dos ejecutables del mismo repositorio.

**Motivo.** Aislar la latencia: el trabajo lento o pesado (revisión manual, ingestas, jobs) nunca compite por recursos con las consultas. No es una decisión de microservicios: hay un solo repositorio y un solo modelo de dominio.

**Consecuencia.** Los cambios de datos llegan al resolver como snapshots versionados (y deltas pequeños para aliases aprobados), con rollback a la versión anterior.
