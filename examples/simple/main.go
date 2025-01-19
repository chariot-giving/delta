package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
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

	controllers := delta.NewControllers(db)
	delta.AddController(controllers, &emailController{})
	delta.AddController(controllers, &mailController{})
	deltaClient, err := delta.NewClient(db, delta.Config{
		Logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
		Namespaces: map[string]delta.NamespaceConfig{
			"events": {
				ResourceExpiry: 10 * time.Second,
			},
		},
		Controllers: controllers,
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
	}
}

type emailController struct {
}

func (c *emailController) Work(ctx context.Context, resource *delta.Resource[Email]) error {
	log.Printf("Processing email: %+v", resource.Object.ID())
	<-time.After(5 * time.Second)
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
}

func (c *mailController) Work(ctx context.Context, resource *delta.Resource[Mail]) error {
	log.Printf("Processing mail: %+v", resource.Object.ID())
	<-time.After(5 * time.Second)
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
