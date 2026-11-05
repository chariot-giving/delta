# Contributing to Delta

We welcome contributions to Delta!

## Running tests

Database-backed tests run against Postgres and are skipped unless
`DELTA_TEST_DATABASE_URL` is set. Each test creates (and drops) its own schema,
so any scratch database works:

```sh
docker run -d --name delta-test-db -e POSTGRES_PASSWORD=postgres -p 5499:5432 postgres:16
DELTA_TEST_DATABASE_URL="postgres://postgres:postgres@localhost:5499/postgres?sslmode=disable" go test -race ./...
```
