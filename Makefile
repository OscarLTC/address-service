.PHONY: test run bench fmt vet catalog osm golden eval db-up snapshot geoeval dbload control snapshot-db

test:
	go test ./...

run:
	go run ./cmd/resolver -data data

bench:
	go test -bench=. -benchmem -run='^$$' ./internal/normalizer

fmt:
	gofmt -w .

vet:
	go vet ./...

catalog:
	go run ./cmd/catalogbuild -xlsx "docs/ref/UBIGEO 2022_1891 distritos.xlsx"

osm:
	go run ./cmd/osmfetch -out data/osm

golden:
	go run ./cmd/goldengen -osm data/osm -out goldenset/golden_v1.csv -mzlt-out goldenset/golden_mzlt_v2.csv

eval:
	go run ./cmd/goldeneval -golden goldenset/golden_v1.csv,goldenset/golden_mzlt_v2.csv -min-core 95

db-up:
	docker compose -f deploy/docker-compose.yml up -d

snapshot:
	go run ./cmd/snapshotbuild -out data/snapshot/lima.snap

geoeval:
	go run ./cmd/geoeval -snapshot data/snapshot/lima.snap

dbload:
	go run ./cmd/dbload -snapshot data/snapshot/lima.snap

# Snapshot con las direcciones verificadas por operadores y GPS de entrega.
snapshot-db:
	go run ./cmd/snapshotbuild -out data/snapshot/lima.snap -db postgres://addrsvc:addrsvc@localhost:55432/addrsvc

control:
	go run ./cmd/control -operators data/private/operators.json
