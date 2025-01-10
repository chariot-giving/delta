# delta

Delta is a robust high-performance resource synchronization system for Go and Postgres.

## Overview

In robotics and automation, a control loop is a non-terminating loop that regulates the state of a system.

In Delta, controllers are control loops that watch the state of your database resources,
then make or request changes where needed.
Each controller tries to move the current database state closer to the desired state.

### Controller Pattern

A controller tracks at least one Delta resource type.
These objects have a spec field that represents the desired state.
The controller(s) for that resource are responsible for making the current state come closer to that desired state.

### Desired versus current state

Your database could be changing at any point as work happens and control loops automatically fix failures.
This means that, potentially, your database never reaches a stable state.

As long as the controllers for your database are running and able to make useful changes,
it doesn't matter if the overall state is stable or not.

## Resources

Resources are defined via an struct that implements the `Object` interface:

```go
type User struct {
    Email string
    Name  string
}

func (e *User) ID() string {
    // The Email uniquely identifies the user on the external system.
    return e.Email
}

func (e *User) Kind() string {
    return "user"
}
```

## Controller

Controllers are defined via a struct that implements the `Worker` and `Informer` interfaces:

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
// This method should be idempotent and safe to run concurrently.
func (c *UserController) Work(ctx context.Context, resource *delta.Resource[User]) error {
    fmt.Printf("Worked user: %+v\n", resource.Object.Email)
    return nil
}

// Inform pushes resources into a channel for processing.
// The nice thing about this is that the Delta library defines the channel semantics
// to enforce backpressure and rate limiting as well as QoS guarantees on durably enqueueing work.
func (c *UserController) Inform(ctx context.Context, queue chan User) {
    resp, _ := http.DefaultClient.Get("https://api.example.com/users", nil)
    defer resp.Body.Close()

    var users []User
    if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
        return err
    }

    go func() {
        for _, user := range users {
            if !c.Match(&user) {
                continue
            }
            select {
            case <-ctx.Done():
                break
            case queue <- user:
            }
        }
    }()

    return nil
}
```

### Registering controllers

Resources are uniquely identified by their `kind` string. Controllers are registered on
start up so that Delta knows how to assign resources to controllers:

```go
controllers := delta.NewControllers()
// AddWorker panics if the controller is already registered or invalid:
delta.AddController(controllers, &UserController{})
```

## Informing resources

[`Client.InformTx`] is used in conjunction with an instance implementation
of `Resource` to inform a Delta Controller about a resource object:

```go
_, err = deltaClient.InformTx(ctx, tx, User{
    Email: "bob@hello.com",
    Name:  "Bob",
}, nil)

if err != nil {
    panic(err)
}
```

## Starting a client

A Delta [`Client`] provides an interface for resource synchronization and background job
processing. A client's created with a database pool, [driver], and config struct
containing a `Controllers` bundle and other settings.
Here's a client `Client` working one namespace (`"default"`) with up to 100 controller
goroutines at a time:

```go
deltaClient, err := delta.NewClient(deltapgxv5.New(dbPool), &delta.Config{
    Namespaces: map[string]delta.NamespaceConfig{
        delta.NamespaceDefault: {MaxWorkers: 100},
    },
    Controllers: controllers,
})
if err != nil {
    panic(err)
}

// Run the client inline. All executed processes will inherit from ctx:
if err := deltaClient.Start(ctx); err != nil {
    panic(err)
}
```

## Stopping

The client should also be stopped on program shutdown:

```go
// Stop fetching new work and wait for active jobs to finish.
if err := deltaClient.Stop(ctx); err != nil {
    panic(err)
}
```

There are some complexities around ensuring clients stop cleanly, but also in a
timely manner.

## Control Plane Components

The control plane's components make global decisions about how Delta manages resources
(for example, scheduling and executing jobs) as well as detecting and responding to events.

### postgres

Persisted state is stored in a Postgres database.
