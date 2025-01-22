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

/* Example request to schedule inform:
*		curl --location --request GET 'localhost:8081/schedule-inform' \
*		--header 'Content-Type: application/json' \
*		--data '{
*			"labels": {
*				"scanStatus": "completed",
*				"to": "Chariot",
*				"from": "IRS"
*			},
*			"limit": 50,
*			"since": "2024-09-06T00:00:00Z"
*		}'
 */

func main() {
	db, err := pgxpool.New(context.Background(), "postgres://delta:password@localhost:5432/delta?search_path=delta")
	if err != nil {
		log.Fatal(err)
	}

	controllers := delta.NewControllers()
	delta.AddController(controllers, &mailController{})

	deltaClient, err := delta.NewClient(db, delta.Config{
		Logger: slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})),
		Namespaces: map[string]delta.NamespaceConfig{
			"stable": {
				MaxWorkers: 3,
			},
		},
		Controllers:            controllers,
		ResourceInformInterval: 60 * time.Second, // re-run controller Informers every 60 seconds
		MaintenanceJobInterval: 60 * time.Second, // run maintenance jobs every 60 seconds
	})
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Starting Delta client")
	if err := deltaClient.Start(context.Background()); err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("/health", healthz)
	http.HandleFunc("/schedule-inform", ScheduleInform(deltaClient))

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

func ScheduleInform(deltaClient *delta.Client) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		params := delta.ScheduleInformParams{
			ResourceKind:    "stable_mail_item", // same as resource.Kind()
			ProcessExisting: false,
			RunForeground:   false,
		}

		var opts *delta.InformOptions
		err := json.NewDecoder(r.Body).Decode(&opts)
		if err != nil {
			log.Printf("Error decoding InformOptions: %v", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		fmt.Printf("Scheduling inform with options: %+v\n", opts)

		err = deltaClient.ScheduleInform(ctx, params, opts)
		if err != nil {
			log.Printf("Error scheduling inform: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}

type MailItem struct {
	MailItemID string
}

func (m MailItem) ID() string {
	return m.MailItemID
}

func (m MailItem) Kind() string {
	return "stable_mail_item"
}

func (m MailItem) InformOpts() delta.InformOpts {
	return delta.InformOpts{
		Namespace: "stable",
		Tags:      []string{"mail"},
	}
}

type mailController struct{}

type GetMailItemsResponse struct {
	Edges []struct {
		Cursor string         `json:"cursor"`
		Node   stableMailItem `json:"node"`
	} `json:"edges"`
	PageInfo struct {
		EndCursor       string `json:"endCursor"`
		HasNextPage     bool   `json:"hasNextPage"`
		HasPreviousPage bool   `json:"hasPreviousPage"`
		StartCursor     string `json:"startCursor"`
		TotalCount      int    `json:"totalCount"`
	} `json:"pageInfo"`
}

type stableMailItem struct {
	ID         string `json:"id"`
	From       string `json:"from"`
	Recipients struct {
		Business struct {
			Id   string `json:"id"`
			Name string `json:"name"`
		} `json:"business"`
	} `json:"recipients"`
	ImageURL string `json:"imageUrl"`
	Location struct {
		ID string `json:"id"`
	} `json:"location"`
	ScanDetails struct {
		ImageURL string `json:"imageUrl"`
		Status   string `json:"status"`
	} `json:"scanDetails"`
	CreatedAt time.Time `json:"createdAt"`
}

type StableApiParams struct {
	ID               string    `json:"id"`
	LocationID       string    `json:"locationId"`
	CreatedAtGt      time.Time `json:"createdAt_gt"`
	CreatedAtGte     string    `json:"createdAt_gte"`
	CreatedAtLt      string    `json:"createdAt_lt"`
	CreatedAtLte     string    `json:"createdAt_lte"`
	ScanStatus       string    `json:"scan.status"`
	ScanCreatedAt    string    `json:"scan.createdAt"`
	ScanCreatedAtGt  string    `json:"scan.createdAt_gt"`
	ScanCreatedAtGte string    `json:"scan.createdAt_gte"`
	ScanCreatedAtLt  string    `json:"scan.createdAt_lt"`
	ScanCreatedAtLte string    `json:"scan.createdAt_lte"`
	First            string    `json:"first"` // return first n items
	After            string    `json:"after"`
	Last             string    `json:"last"` // return last n items
	Before           string    `json:"before"`
}

func (c *mailController) Work(ctx context.Context, resource *delta.Resource[MailItem]) error {
	log.Printf("Processing mail item[%d]: %+v", resource.ID, resource.Object)
	return nil
}

func (c *mailController) Inform(ctx context.Context, opts *delta.InformOptions) (<-chan MailItem, error) {
	queue := make(chan MailItem)
	fmt.Printf("Informing mail with options: %+v\n", opts)
	go func() {
		defer close(queue)
		log.Println("Informing mail...")

		client := &http.Client{}

		// Set up Stable API query parameters
		values := url.Values{}
		if opts != nil {
			if opts.Since != nil {
				values.Set("createdAt_gt", opts.Since.Format("2006-01-02"))
			}

			if opts.Limit > 0 {
				values.Set("first", strconv.Itoa(min(opts.Limit, 100)))
			} else {
				values.Set("first", "100")
			}

			if opts.Labels != nil {
				for key, label := range opts.Labels {
					switch key {
					case "scanStatus":
						values.Set("scan.status", label)
					}
				}
			}
		}

		for {
			url := fmt.Sprintf("https://api.dev.usestable.com/v1/mail-items?%s", values.Encode())

			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				log.Printf("Error creating request: %v", err)
				return
			}

			req.Header.Set("x-api-key", os.Getenv("STABLE_ABI_KEY"))
			req.Header.Set("Accept", "application/json")

			log.Printf("Making request to %s...", url)
			resp, err := client.Do(req)
			if err != nil {
				log.Printf("Error making request: %v", err)
				return
			}
			defer resp.Body.Close()

			var response GetMailItemsResponse
			if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
				log.Printf("Error decoding response: %v", err)
				return
			}

			log.Printf("Received %d mail items", len(response.Edges))

			for _, edge := range response.Edges {
				item := edge.Node

				// Post filter on labels that are not supported by the API
				if opts != nil && opts.Labels != nil {
					if to, ok := opts.Labels["to"]; ok && item.Recipients.Business.Name != to {
						continue
					}

					if from, ok := opts.Labels["from"]; ok && item.From != from {
						continue
					}
				}

				// Send mail items to queue
				select {
				case <-ctx.Done():
					return
				case queue <- MailItem{
					MailItemID: item.ID,
				}:
				}
			}

			// Handle pagination
			if response.PageInfo.HasNextPage {
				values.Set("after", response.PageInfo.EndCursor)
			} else {
				break
			}
		}

		log.Println("Finished informing stable mail items")
	}()
	return queue, nil
}
