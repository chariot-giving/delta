# Getting Started

Learn how to install Delta packages for Go, run migrations to get Delta's database schema in
place, and create an initial controller and client to start managing resources.

## Prerequisites

Delta requires an existing PostgreSQL database, and is most commonly used with pgx.

## Installation

To install Delta, run the following in the directory of a Go project (where a go.mod file is present):

```bash
go get github.com/chariot-giving/delta
```

## Running Migrations

Delta persists resource state to a Postgres database, and needs a small set of tables created to manage resources and orchestrate controllers.
It relies on [goose](https://github.com/pressly/goose) to run migrations.

```bash
GOOSE_DRIVER=postgres
GOOSE_DBSTRING=postgres://chariot:password@localhost:5432/chariot?search_path=delta
GOOSE_MIGRATION_DIR=./internal/db/migrations
goose status
goose up
```

> Note that multiple Delta clients can share the same database and schema even if those
> clients are configured to manage different resources with different controllers.

## Resource Objects and Controllers

Each kind of managed resource in Delta requires two types: an `Object` struct
and a `Controller[Object]` struct. The `Object` struct has two purposes:

1. It defines the structured arguments for your worker. These arguments are serialized to JSON before the resource is stored in the database.
2. It defines a `Kind()` string method that will be used to uniquely identify the kind of resource in the database.

The `Controller[Object]` struct is a worker that will be used to process the `Object` struct
as well as an informer that will be used to inform Delta of external `Object` resources.
