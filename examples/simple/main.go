package main

import (
	"context"
	"fmt"
	"log"
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
	delta.AddController(controllers, &simpleController{})
	deltaClient, err := delta.NewClient(db, delta.Config{
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

type simpleController struct {
}

func (c *simpleController) Work(ctx context.Context, resource *delta.Resource[Email]) error {
	log.Println("Work called")
	log.Printf("Processing email: %+v", resource.Object.ID())
	<-time.After(5 * time.Second)
	log.Println("Finished processing email")
	return nil
}

func (c *simpleController) Inform(ctx context.Context, opts *delta.InformOptions) (<-chan Email, error) {
	log.Println("Inform called")
	queue := make(chan Email)
	go func() {
		defer close(queue)
		log.Println("Informing email")
		queue <- Email{
			InboxId: "123",
			To:      "test@test.com",
			From:    "test@test.com",
			Body:    "test",
		}
	}()
	return queue, nil
}
