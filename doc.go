/*
Delta is a robust high-performance resource synchronization system for Go and Postgres.

## Resources

Resources are defined via an interface:

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

## Workers & Streams

Workers and Streams are defined via a struct that implement the `Worker` and `Streamer` interfaces:

```go

	type UserWorker struct {
	    // An embedded WorkerDefaults sets up default methods to fulfill the rest of
	    // the Worker interface:
	    delta.WorkerDefaults[User]
	    // An embedded StreamDefaults sets up default methods to fulfill the rest of
	    // the Stream interface:
	    delta.StreamDefaults[User]
	}

// Work does the heavy lifting of processing a resource.
// This method should be idempotent and safe to run concurrently.

	func (w *UserWorker) Work(ctx context.Context, job *delta.Job[User]) error {
	    fmt.Printf("Worked user: %+v\n", job.Resource.Email)
	    return nil
	}

// Stream returns a channel of resource results that should be processed according to a filter.

	func (w *UserWorker) Stream(ctx context.Context, filter *delta.Filter[User]) (<-chan *delta.Result[User], error) {
	    resp, _ := http.DefaultClient.Get("https://api.example.com/users", nil)
	    defer resp.Body.Close()

	    var users []User
	    if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
	        return err
	    }

	    results := make(chan *delta.Result[User])
	    go func() {
	        for _, user := range users {
	            if filter != nil && !filter.Match(&user) {
	                continue
	            }
	            select {
	            case <-ctx.Done():
	                break
	            default:
	                results <- &delta.Result[User]{Resource: &user}
	            }
	        }
	        close(results)
	    }()

	    return results
	}

```

## Registering workers

Resources are uniquely identified by their `kind` string. Workers are registered on
start up so that Delta knows how to assign resources to workers:

```go
workers := delta.NewWorkers()
// AddWorker panics if the worker is already registered or invalid:
delta.AddWorker(workers, &UserWorker{})
```

## Starting a client

A Delta [`Client`] provides an interface for resource synchronization and background job
processing. A client's created with a database pool, [driver], and config struct
containing a `Workers` bundle and other settings.
Here's a client `Client` working one queue (`"default"`) with up to 100 worker
goroutines at a time:

```go

	deltaClient, err := delta.NewClient(deltapgxv5.New(dbPool), &delta.Config{
	    Queues: map[string]delta.QueueConfig{
	        delta.QueueDefault: {MaxWorkers: 100},
	    },
	    Workers: workers,
	})

	if err != nil {
	    panic(err)
	}

// Run the client inline. All executed jobs will inherit from ctx:

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

## Inserting resources

[`Client.InsertTx`] is used in conjunction with an instance implementation
of `Resource` to insert a resource to synchronize on a transaction:

```go

	_, err = deltaClient.InsertTx(ctx, tx, User{
	    Email: "bob@hello.com",
	    Name:  "Bob",
	}, nil)

	if err != nil {
	    panic(err)
	}

```
*/
package delta
