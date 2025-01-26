package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chariot-giving/delta"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	http.HandleFunc("/health", healthz)

	db, err := pgxpool.New(context.Background(), "postgres://delta:password@localhost:5431/delta?search_path=delta")
	if err != nil {
		log.Fatal(err)
	}

	controllers := delta.NewControllers()
	delta.AddController(controllers, &emailController{})
	delta.AddController(controllers, &mailController{})

	deltaClient, err := delta.NewClient(db, &delta.Config{
		Logger: slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelWarn,
		})),
		Namespaces: map[string]delta.NamespaceConfig{
			"events": {
				ResourceExpiry: 10 * time.Second, // expire resources after 10 minutes
			},
		},
		Controllers:                    controllers,
		ResourceInformInterval:         30 * time.Second, // re-run controller Informers every 30 seconds
		MaintenanceJobInterval:         5 * time.Second,  // run maintenance jobs every 5 seconds
		DeletedResourceRetentionPeriod: 30 * time.Second, // keep deleted resources for 30 seconds
	})
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Starting Delta client")
	if err := deltaClient.Start(context.Background()); err != nil {
		log.Fatal(err)
	}

	log.Println("Starting server on :8080")

	srv := &http.Server{
		Addr:    ":8080",
		Handler: nil,
	}

	// Event subscription
	events, cancel := deltaClient.Subscribe(delta.EventCategoryObjectSynced, delta.EventCategoryObjectDeleted)
	defer cancel()
	go func() {
		for event := range events {
			if event.EventCategory == delta.EventCategoryObjectSynced {
				if event.Resource.Attempt == 1 {
					// Note that in this example, it's possible for us to delete a resource, for the cleaner
					// to hard-delete it from the DB and then for subsequent Informer controller to re-create
					// the "mail" resource.
					// This doesn't really model the real world but it demonstrates potential capabilities.
					log.Printf("Resource created: %s:%s", event.Resource.ObjectKind, event.Resource.ObjectID)
				} else {
					log.Printf("Resource updated: %s:%s", event.Resource.ObjectKind, event.Resource.ObjectID)
				}
			} else if event.EventCategory == delta.EventCategoryObjectDeleted {
				log.Printf("Resource deleted: %s:%s", event.Resource.ObjectKind, event.Resource.ObjectID)
			}
		}
	}()

	// Handle graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		log.Println("Shutting down server...")
		if err := srv.Shutdown(context.Background()); err != nil {
			log.Printf("HTTP server shutdown error: %v", err)
		}
	}()

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
	log.Println("Server stopped")
}

func healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "healthy")
}

type Email struct {
	InboxId string
	To      string
	From    string
	Body    string
}

func (e Email) ID() string {
	return e.InboxId
}

func (e Email) Kind() string {
	return "email"
}

func (e Email) InformOpts() delta.InformOpts {
	return delta.InformOpts{
		Namespace: "events",
		Tags:      []string{"test"},
	}
}

type emailController struct {
	delta.WorkerDefaults[Email]
	delta.InformerDefaults[Email]
}

func (c *emailController) Work(ctx context.Context, resource *delta.Resource[Email]) error {
	log.Printf("Processing email: %+v", resource.Object.ID())
	<-time.After(time.Duration(rand.Intn(5)) * time.Second)
	log.Println("Finished processing email")
	return nil
}

func (c *emailController) Inform(ctx context.Context, opts *delta.InformOptions) (<-chan Email, error) {
	queue := make(chan Email)
	go func() {
		defer close(queue)
		log.Println("Informing emails...")
		queue <- Email{
			InboxId: "123",
			To:      "max@test.com",
			From:    "test@test.com",
			Body:    "hi max",
		}
		queue <- Email{
			InboxId: "456",
			To:      "matt@test.com",
			From:    "test@test.com",
			Body:    "hi matt",
		}
		log.Println("Informed emails")
	}()
	return queue, nil
}

type Mail struct {
	ItemID string
	From   string
	To     string
	Body   string
}

func (m Mail) ID() string {
	return m.ItemID
}

func (m Mail) Kind() string {
	return "mail"
}

func (m Mail) InformOpts() delta.InformOpts {
	return delta.InformOpts{
		Namespace: "events",
	}
}

type mailController struct {
	delta.WorkerDefaults[Mail]
	delta.InformerDefaults[Mail]
}

func (c *mailController) Work(ctx context.Context, resource *delta.Resource[Mail]) error {
	log.Printf("Processing mail: %+v", resource.Object.ID())
	<-time.After(time.Duration(rand.Intn(5)) * time.Second)

	if resource.Attempt > 2 {
		return delta.ResourceDelete(fmt.Errorf("failed after 3 attempts"))
	}

	log.Println("Finished processing mail")
	return nil
}

func (c *mailController) Inform(ctx context.Context, opts *delta.InformOptions) (<-chan Mail, error) {
	queue := make(chan Mail)
	go func() {
		defer close(queue)
		log.Println("Informing mails...")
		queue <- Mail{
			ItemID: "abc",
			From:   "Test Service, Inc.",
			To:     "Matthew Magaldi",
			Body:   "dear matt",
		}
	}()
	return queue, nil
}
