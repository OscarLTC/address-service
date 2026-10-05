# Imagen del resolver (plano de datos). El snapshot no va en la imagen: se monta o
# se descarga al arrancar, para cambiar de versión sin reconstruir.
#
#   docker build -t addrsvc-resolver .
#   docker run -p 8080:8080 -v $PWD/data/snapshot:/app/data/snapshot:ro addrsvc-resolver
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/resolver ./cmd/resolver

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/resolver /app/resolver
COPY data/catalog /app/data/catalog
COPY data/rules /app/data/rules
COPY data/config /app/data/config
COPY data/geo /app/data/geo
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/app/resolver", "-addr", ":8080", "-data", "/app/data", "-snapshot", "/app/data/snapshot/lima.snap"]
