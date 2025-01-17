.PHONY: install generate verify build test upgrade

install: go.mod
	go mod download

build:
	go build ./...

test:
	go test ./...

generate: generate/sqlc

generate/sqlc:
	@echo "Generating SQLC..."
	sqlc generate -f ./internal/db/sqlc/sqlc.yaml

verify: verify/sqlc

verify/sqlc:
	@echo "Verifying SQLC..."
	sqlc diff -f ./internal/db/sqlc/sqlc.yaml

upgrade:
	go get -u ./... && go mod tidy
