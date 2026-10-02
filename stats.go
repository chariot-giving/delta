package delta

import (
	"context"
	"fmt"
	"time"

	"github.com/chariot-giving/delta/deltatype"
	"github.com/chariot-giving/delta/internal/db/sqlc"
)

// Stats is a point-in-time snapshot of Delta's resources and River jobs,
// intended to back gauges (for example a Prometheus collector that calls
// Client.Stats on each scrape).
//
// Reading stats at scrape time, instead of having Delta push gauges from a
// maintenance job, avoids stale values when River leadership moves between
// processes.
type Stats struct {
	// Resources has one entry per resource kind and state that has resources.
	Resources []ResourceStat
	// Jobs has one entry per queue, job kind and state for unfinalized delta
	// River jobs.
	Jobs []JobStat
}

// ResourceStat describes the resources of one kind in one state.
type ResourceStat struct {
	Kind  string
	State deltatype.ResourceState
	Count int64
	// OldestAge is the time since the least recently attempted resource in
	// the group was attempted (or created, if it was never attempted). For
	// failed, degraded and scheduled resources this shows how long work has
	// been stuck.
	OldestAge time.Duration
}

// JobStat describes the unfinalized River jobs of one kind in one queue and
// state.
type JobStat struct {
	Queue string
	Kind  string
	State string
	Count int64
	// OldestScheduledAge is the time since the earliest scheduled_at in the
	// group. For available jobs it's how long the oldest job has waited to be
	// picked up. It's negative for jobs scheduled in the future.
	OldestScheduledAge time.Duration
}

// Stats returns a snapshot of resource counts by kind and state, and of
// unfinalized delta River jobs. Both queries scan their tables, so call it on
// a scrape interval (tens of seconds), not in a hot path.
func (c *Client) Stats(ctx context.Context) (*Stats, error) {
	queries := sqlc.New(c.dbPool)

	resourceRows, err := queries.ResourceStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get resource stats: %w", err)
	}
	jobRows, err := queries.JobStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get job stats: %w", err)
	}

	stats := &Stats{
		Resources: make([]ResourceStat, len(resourceRows)),
		Jobs:      make([]JobStat, len(jobRows)),
	}
	for i, row := range resourceRows {
		stats.Resources[i] = ResourceStat{
			Kind:      row.Kind,
			State:     deltatype.ResourceState(row.State),
			Count:     row.Count,
			OldestAge: secondsToDuration(row.OldestAgeSeconds),
		}
	}
	for i, row := range jobRows {
		stats.Jobs[i] = JobStat{
			Queue:              row.Queue,
			Kind:               row.Kind,
			State:              row.State,
			Count:              row.Count,
			OldestScheduledAge: secondsToDuration(row.OldestScheduledAgeSeconds),
		}
	}
	return stats, nil
}

func secondsToDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}
