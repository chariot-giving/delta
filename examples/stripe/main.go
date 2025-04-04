package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
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

	deltaClient, err := delta.NewClient(db, &delta.Config{
		Logger: slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})),
		Namespaces: map[string]delta.NamespaceConfig{
			"stripe": {
				MaxWorkers: 3,
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
	CustomerID    string
	Email         string
	CreatedAtTime time.Time
}

func (c StripeCustomer) ID() string {
	return c.CustomerID
}

func (c StripeCustomer) Kind() string {
	return "customer"
}

func (c StripeCustomer) CreatedAt() time.Time {
	return c.CreatedAtTime
}

func (c StripeCustomer) InformOpts() delta.InformOpts {
	return delta.InformOpts{
		Namespace: "stripe",
		Tags:      []string{"billing"},
	}
}

func (c StripeCustomer) Settings() delta.ObjectSettings {
	return delta.ObjectSettings{
		Parallelism:    10,
		InformInterval: 60 * time.Second,
	}
}

type customerController struct {
	delta.WorkerDefaults[StripeCustomer]
	delta.InformerDefaults[StripeCustomer]
}

func (c *customerController) Work(ctx context.Context, resource *delta.Resource[StripeCustomer]) error {
	log.Printf("Processing customer[%d]: %+v", resource.ID, resource.Object)
	return nil
}

func (c *customerController) Inform(ctx context.Context, opts *delta.InformOptions) (<-chan StripeCustomer, error) {
	queue := make(chan StripeCustomer)

	uri, err := url.Parse("https://api.stripe.com/v1/customers")
	if err != nil {
		log.Printf("Error parsing URL: %v", err)
		return nil, err
	}

	go func() {
		defer close(queue)
		log.Println("Informing stripe customers...")

		// Create HTTP client
		client := &http.Client{}

		hasMore := true
		startingAfter := ""

		query := url.Values{}
		for hasMore {
			if startingAfter != "" {
				query.Add("starting_after", startingAfter)
			}
			// Add created[gt] parameter if After is specified (Stripe's API uses unix seconds)
			if opts != nil && opts.After != nil && !opts.After.IsZero() {
				query.Add("created[gt]", strconv.FormatInt(opts.After.Unix(), 10))
			}
			// Stripe allows limit between 1 and 100
			if opts != nil && opts.Limit > 0 {
				query.Add("limit", strconv.Itoa(min(opts.Limit, 100)))
			} else {
				// default to 100
				query.Add("limit", "100")
			}

			uri.RawQuery = query.Encode()

			req, err := http.NewRequestWithContext(ctx, "GET", uri.String(), nil)
			if err != nil {
				log.Printf("Error creating request: %v", err)
				return
			}

			// Add Stripe authentication header
			req.Header.Add("Authorization", "Bearer "+os.Getenv("STRIPE_SECRET_KEY"))

			log.Printf("Making request to %s...", uri.String())
			resp, err := client.Do(req)
			if err != nil {
				log.Printf("Error making request: %v", err)
				return
			}
			defer resp.Body.Close()

			var result struct {
				HasMore bool `json:"has_more"`
				Data    []struct {
					ID        string `json:"id"`
					Email     string `json:"email"`
					CreatedAt int64  `json:"created"`
				} `json:"data"`
			}

			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				log.Printf("Error decoding response: %v", err)
				return
			}

			log.Printf("Received %d customers", len(result.Data))

			// Send customers to queue
			for _, customer := range result.Data {
				select {
				case <-ctx.Done():
					return
				case queue <- StripeCustomer{
					CustomerID:    customer.ID,
					Email:         customer.Email,
					CreatedAtTime: time.Unix(customer.CreatedAt, 0),
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
