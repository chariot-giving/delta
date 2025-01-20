package main

import (
	"context"
	"encoding/json"
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

	controllers := delta.NewControllers()
	delta.AddController(controllers, &customerController{})

	deltaClient, err := delta.NewClient(db, delta.Config{
		Logger: slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelWarn,
		})),
		Namespaces: map[string]delta.NamespaceConfig{
			"stripe": {
				MaxWorkers: 3,
			},
		},
		Controllers:            controllers,
		ResourceInformInterval: 1 * time.Hour,    // re-run controller Informers every 1 hour
		MaintenanceJobInterval: 60 * time.Second, // run maintenance jobs every 60 seconds
	})
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Starting Delta client")
	if err := deltaClient.Start(context.Background()); err != nil {
		log.Fatal(err)
	}

	log.Println("Starting server on :8081")

	srv := &http.Server{
		Addr:    ":8081",
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

type StripeCustomer struct {
	CustomerID string
	Email      string
}

func (c StripeCustomer) ID() string {
	return c.CustomerID
}

func (c StripeCustomer) Kind() string {
	return "customer"
}

func (c StripeCustomer) InformOpts() delta.InformOpts {
	return delta.InformOpts{
		Namespace: "stripe",
		Tags:      []string{"billing"},
	}
}

type customerController struct {
}

func (c *customerController) Work(ctx context.Context, resource *delta.Resource[StripeCustomer]) error {
	log.Printf("Processing customer[%d]: %+v", resource.ID, resource.Object)
	return nil
}

func (c *customerController) Inform(ctx context.Context, opts *delta.InformOptions) (<-chan StripeCustomer, error) {
	queue := make(chan StripeCustomer)
	go func() {
		defer close(queue)
		log.Println("Informing stripe customers...")

		// Create HTTP client
		client := &http.Client{}

		hasMore := true
		startingAfter := ""

		for hasMore {
			url := "https://api.stripe.com/v1/customers?limit=100"
			if startingAfter != "" {
				url += "&starting_after=" + startingAfter
			}

			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				log.Printf("Error creating request: %v", err)
				return
			}

			// Add Stripe authentication header
			req.Header.Add("Authorization", "Bearer "+os.Getenv("STRIPE_SECRET_KEY"))

			resp, err := client.Do(req)
			if err != nil {
				log.Printf("Error making request: %v", err)
				return
			}
			defer resp.Body.Close()

			var result struct {
				HasMore bool `json:"has_more"`
				Data    []struct {
					ID    string `json:"id"`
					Email string `json:"email"`
				} `json:"data"`
			}

			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				log.Printf("Error decoding response: %v", err)
				return
			}

			// Send customers to queue
			for _, customer := range result.Data {
				select {
				case <-ctx.Done():
					return
				case queue <- StripeCustomer{
					CustomerID: customer.ID,
					Email:      customer.Email,
				}:
				}
			}

			hasMore = result.HasMore
			if len(result.Data) > 0 {
				startingAfter = result.Data[len(result.Data)-1].ID
			}
		}

		log.Println("Finished informing stripe customers")
	}()
	return queue, nil
}
