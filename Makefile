.PHONY: test run bench fmt vet catalog

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
