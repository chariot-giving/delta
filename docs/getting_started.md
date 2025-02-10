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

We rely on Goose to run database migrations:

```bash
go install github.com/pressly/goose/v3/cmd/goose@latest
```

Finally, install the River CLI tool:

```bash
go install github.com/riverqueue/river/cmd/river@latest
```

## Running Migrations

Run a Postgres database locally with Docker:

```bash
docker run --name delta-db -e POSTGRES_PASSWORD=password -e POSTGRES_USER=delta -e POSTGRES_DB=delta -p 5431:5432 -d postgres:16
```

Connect to the database and create the Delta schema:

```bash
$ pgcli "postgres://delta:password@localhost:5431/delta"
> CREATE SCHEMA delta;
```

Delta persists resource state to a Postgres database, and needs a small set of tables created to manage resources and orchestrate controllers.
It relies on [goose](https://github.com/pressly/goose) to run migrations.

```bash
export GOOSE_DRIVER=postgres
export GOOSE_DBSTRING=postgres://delta:password@localhost:5431/delta?search_path=delta
export GOOSE_MIGRATION_DIR=./internal/db/migrations
goose up
goose status
```

> Note that multiple Delta clients can share the same database and schema even if those
> clients are configured to manage different resources with different controllers.

Next, we need to initialize the River schemas in the same Delta database & schema.

```bash
river migrate-up --database-url $GOOSE_DBSTRING
```

## Resource Objects and Controllers

Each kind of managed resource in Delta requires two types: an `Object` struct
and a `Controller[Object]` struct. The `Object` struct has two purposes:

1. It defines the structured object for your controller. These arguments are serialized to JSON before the resource is stored in the database.
2. It defines two methods: a `Kind()` and `ID()` string methods that will be used to uniquely identify the resource in the database.

Here's an example of a `User` struct that implements the `Object` interface:

```go
type User struct {
    Email string
    Name  string
    UpdatedAt int64
}

func (u *User) ID() string {
    // The Email uniquely identifies the user on the external system.
    return e.Email
}

func (u *User) Kind() string {
    return "user"
}
```

The `Controller[Object]` struct is a worker that will be used to process the `Object` struct
as well as an informer that will be used to inform Delta of external `Object` resources.

Here is an example of a simple controller for managing a `User` resource:

```go
type UserController struct {
    // An embedded WorkerDefaults sets up default methods to fulfill the rest of
    // the Worker interface:
    delta.WorkerDefaults[User]
    // An embedded StreamDefaults sets up default methods to fulfill the rest of
    // the Stream interface:
    delta.InformerDefaults[User]
}

// Work does the heavy lifting of processing a resource.
func (c *UserController) Work(ctx context.Context, resource *delta.Resource[User]) error {
    fmt.Printf("Worked user: %+v\n", resource.Object.Email)
    return nil
}

// Inform pushes resources into a channel for processing.
func (c *UserController) Inform(ctx context.Context, opts *delta.InformOptions) (<-chan User, error) {
    userChan := make(chan User)
    resp, _ := http.DefaultClient.Get("https://api.example.com/users", nil)
    defer resp.Body.Close()

    var users []User
    if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
        return err
    }

    go func() {
        defer close(userChan)
        for _, user := range users {
            if !c.Match(&user) {
                continue
            }
            select {
            case <-ctx.Done():
                return ctx.Err()
            case userChan <- user:
            }
        }
    }()

    return userChan, nil
}
```

## Creating a Client

To create a Delta client, you need to create a `delta.Client` struct and pass in a `delta.Config` struct.

```go
// Configure Controllers
controllers := delta.NewControllers()
delta.AddController(controllers, &UserController{})

// Create a Delta Client
deltaClient, err := delta.NewClient(dbPool, &delta.Config{
    Namespaces: map[string]delta.NamespaceConfig{
        delta.NamespaceDefault: {MaxWorkers: 10},
    },
    Controllers: controllers,
})
if err != nil {
    panic(err)
}
```

## Running the Client

```go
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

// Run the client inline. All executed processes will inherit from ctx:
if err := deltaClient.Start(ctx); err != nil {
    panic(err)
}

<-quit // Wait for a signal to stop the client

if err := deltaClient.Stop(ctx); err != nil {
    panic(err)
}
```
