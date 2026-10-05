# ADR 0006 - Driver de PostgreSQL solo en el plano de control

**Estado:** aceptado

**Contexto.** El ADR 0001 fijó Go con solo la biblioteca estándar "en esta etapa". El plano de control (admin, cola de revisión, carga de datos) necesita leer y escribir en PostgreSQL, y la biblioteca estándar no trae un driver.

**Decisión.** El plano de control usa `github.com/jackc/pgx/v5`. El plano de datos (`cmd/resolver` y los paquetes que importa) sigue sin dependencias externas y nunca habla con la base de datos por request (ADR 0002). Una prueba (`cmd/resolver/deps_test.go`) falla si el resolver llega a importar `pgx`.

**Consecuencias.** `go.mod` deja de estar vacío de dependencias. El binario del resolver y su imagen no cambian. Si el plano de control crece, se evalúa una capa de acceso a datos propia antes de sumar un ORM.
