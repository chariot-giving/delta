# Monitoring Delta

Delta gives you three kinds of signal:

1. **Logs.** Structured `slog` lines from Delta's job middleware and workers, plus
   River's own logs, all written through `Config.Logger`.
2. **Metrics.** Counters and histograms pushed to the `Config.Metrics`
   `MetricsCollector` you provide. The default collector discards them.
3. **Stats.** `Client.Stats(ctx)` returns a point-in-time snapshot of resources
   by kind and state, and of unfinalized River jobs. Read it at scrape time to
   back gauges.

Logs work without any setup. Metrics and stats need a little wiring in the
application (see [Wiring Prometheus](#wiring-prometheus)).

## Log lines worth alerting on

All lines are JSON. With the stdlib JSON handler, Delta's job middleware puts
job fields under a `riverjob` group, so after `| json` in LogQL they appear as
`riverjob_job_kind`, `riverjob_state`, `riverjob_error` and so on. River's own
lines carry `name=riverqueue`.

| Message | Level | Meaning |
|---|---|---|
| `resource failed` | WARN | `Work()` returned an error. `riverjob_state` is `failed` (will retry) or `degraded` (gave up). |
| `job failed` | ERROR | A delta job returned an error (resource, inform, scheduler or maintenance). |
| `job cancelled` | INFO | Expected cancellation, e.g. the resource was deleted. |
| `rescheduled resources` | INFO | The rescheduler reset expired or stuck resources. |
| `degraded orphaned failed resources` | INFO | Failed resources whose River job disappeared were retired. |
| `delta: database pool is smaller than ...` | WARN | Logged at startup. The pool can't serve the configured parallelism. |
| `producer: Error fetching jobs` | ERROR | River couldn't fetch jobs in time. This usually means pool or DB pressure. |

## Alert rules (drafts)

These are drafts. Thresholds come from a week of production data (Sept 24 to
Oct 1, 2026) and should be tuned once they've been live for a while. The Loki
rules work today against `grafanacloud-logs`. The Prometheus rules need the
wiring below.

### Fix the existing "River failed to rescue job" rule

The current rule queries `{environment="prod"} |= "Attempted to rescue"`.
There is no `environment` label in Loki; the label is
`deployment_environment_name`. As written, the rule always returns NoData, and
NoData is mapped to OK, so it can never fire. Change the query to:

```logql
sum(count_over_time({deployment_environment_name="prod"} |= "Attempted to rescue" [5m]))
```

### Loki rules (available now)

**Delta resource degraded.** A resource exhausted its attempts and Delta gave
up on it. Each one needs a human. Fires on any occurrence.

```logql
sum by (service_name, riverjob_job_kind) (
  count_over_time({deployment_environment_name="prod", service_name=~"bank|supervisor|iris"}
    |= "resource failed" | json | message="resource failed" | riverjob_state="degraded" [1h])
)
```

Condition: `> 0`. Pending period: 0m. Severity: warning.

**Delta resource failure rate high.** Many retries at once usually means an
upstream outage or a bad deploy. Last week's background was about 140 a day,
mostly one poison event.

```logql
sum by (service_name) (
  count_over_time({deployment_environment_name="prod", service_name=~"bank|supervisor|iris"}
    |= "resource failed" | json | message="resource failed" [15m])
)
```

Condition: `> 50`. Pending period: 15m. Severity: warning.

**Delta informer stopped.** No successful inform run for a controller kind.
Every controller runs at least hourly (`column_event` and `increase_event`
every 5m). List the kinds you care about explicitly, because absence alerts
need a concrete series.

```logql
sum by (riverjob_job_kind) (
  count_over_time({deployment_environment_name="prod", service_name="bank"}
    |= "job completed" |= "delta.inform." | json | message="job completed"
    | riverjob_job_kind=~"delta.inform.(column_event|increase_event)" [30m])
)
```

Condition: `< 1`. Set the no-data state to **Alerting**. Pending period: 10m.
Add a second copy for hourly kinds with a `[3h]` window.

**River job fetch timeouts.** Delta's River client is timing out fetching or
completing jobs, which usually means pool exhaustion or DB pressure. Last week
there were bursts of 40–80 per hour during incidents.

```logql
sum by (service_name) (
  count_over_time({deployment_environment_name="prod", service_name=~"bank|supervisor|iris"}
    |~ "Error fetching jobs|BatchCompleter.*timed out|Error attempting to elect" [10m])
)
```

Condition: `> 20`. Pending period: 10m. Severity: warning.

### Prometheus rules (after wiring metrics)

Metric names below assume the collector from
[Wiring Prometheus](#wiring-prometheus): dots become underscores, counters get
`_total`, histograms are in seconds.

| Alert | Expression | For |
|---|---|---|
| Resources degrading | `sum by (kind) (increase(delta_work_runs_total{state="degraded"}[1h])) > 0` | 0m |
| Work failure ratio | `sum by (kind) (rate(delta_work_runs_total{state=~"failed\|degraded"}[15m])) / sum by (kind) (rate(delta_work_runs_total[15m])) > 0.2` | 15m |
| Informer errors | `sum by (kind) (increase(delta_inform_runs_total{result="error"}[1h])) >= 3` | 0m |
| Informer stopped | `sum by (kind) (increase(delta_inform_runs_total{result="success"}[3h])) == 0` | 10m |
| Job backlog | `max by (queue) (delta_jobs_oldest_scheduled_age_seconds{state="available"}) > 300` | 10m |
| Stuck failed resources | `max by (kind) (delta_resources_oldest_age_seconds{state="failed"}) > 6 * 3600` | 30m |
| Degraded resources present | `sum by (kind) (delta_resources{state="degraded"}) > 0` | 1h |
| Events dropped | `sum(increase(delta_subscription_dropped_total[15m])) > 0` | 0m |

`delta_resource_enqueued_total{result="deduplicated"}` doesn't need an alert.
Watch it on the dashboard: a rising deduplication rate means something is
re-informing the same resources repeatedly.

## Wiring Prometheus

Delta doesn't depend on a metrics library. Implement `delta.MetricsCollector`
in the application and register a collector that reads `Client.Stats` on
scrape. Here is a sketch using `prometheus/client_golang`:

```go
type deltaMetrics struct {
	mu         sync.Mutex
	counters   map[string]*prometheus.CounterVec
	histograms map[string]*prometheus.HistogramVec
	registry   prometheus.Registerer
}

func promName(name string) string { return strings.ReplaceAll(name, ".", "_") }

func labelNames(labels map[string]string) []string {
	names := make([]string, 0, len(labels))
	for k := range labels {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func (m *deltaMetrics) Counter(_ context.Context, name string, value float64, labels map[string]string) {
	m.mu.Lock()
	vec, ok := m.counters[name]
	if !ok {
		vec = prometheus.NewCounterVec(prometheus.CounterOpts{Name: promName(name) + "_total"}, labelNames(labels))
		m.registry.MustRegister(vec)
		m.counters[name] = vec
	}
	m.mu.Unlock()
	vec.With(labels).Add(value)
}

func (m *deltaMetrics) Histogram(_ context.Context, name string, value float64, labels map[string]string) {
	m.mu.Lock()
	vec, ok := m.histograms[name]
	if !ok {
		vec = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    promName(name) + "_seconds",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
		}, labelNames(labels))
		m.registry.MustRegister(vec)
		m.histograms[name] = vec
	}
	m.mu.Unlock()
	vec.With(labels).Observe(value)
}

// statsCollector exposes Client.Stats as gauges, read on every scrape.
type statsCollector struct{ client *delta.Client }

var (
	resourcesDesc = prometheus.NewDesc("delta_resources", "Delta resources by kind and state.", []string{"kind", "state"}, nil)
	resourceAgeDesc = prometheus.NewDesc("delta_resources_oldest_age_seconds", "Age of the least recently attempted resource.", []string{"kind", "state"}, nil)
	jobsDesc = prometheus.NewDesc("delta_jobs", "Unfinalized delta River jobs.", []string{"queue", "kind", "state"}, nil)
	jobAgeDesc = prometheus.NewDesc("delta_jobs_oldest_scheduled_age_seconds", "Age of the oldest scheduled_at.", []string{"queue", "kind", "state"}, nil)
)

func (c statsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- resourcesDesc
	ch <- resourceAgeDesc
	ch <- jobsDesc
	ch <- jobAgeDesc
}

func (c statsCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stats, err := c.client.Stats(ctx)
	if err != nil {
		return
	}
	for _, r := range stats.Resources {
		ch <- prometheus.MustNewConstMetric(resourcesDesc, prometheus.GaugeValue, float64(r.Count), r.Kind, string(r.State))
		ch <- prometheus.MustNewConstMetric(resourceAgeDesc, prometheus.GaugeValue, r.OldestAge.Seconds(), r.Kind, string(r.State))
	}
	for _, j := range stats.Jobs {
		ch <- prometheus.MustNewConstMetric(jobsDesc, prometheus.GaugeValue, float64(j.Count), j.Queue, j.Kind, j.State)
		ch <- prometheus.MustNewConstMetric(jobAgeDesc, prometheus.GaugeValue, j.OldestScheduledAge.Seconds(), j.Queue, j.Kind, j.State)
	}
}
```

Delta always emits the same label keys for a given metric name, so registering
a vector with the labels from the first call is safe. Every replica exports the
same `Stats` values, so aggregate with `max`, not `sum`.

Delta's River client does not register River's OpenTelemetry middleware. If you
want River-level `river_work_*` metrics broken down per kind, make sure Adaptive
Metrics isn't aggregating away the `kind`/`queue` labels. In production they
currently are.

## Dashboard

[`grafana/delta-dashboard.json`](grafana/delta-dashboard.json) is an importable
dashboard with two rows:

- **Logs (works today).** Resource failures by kind, degraded transitions, job
  errors vs. cancellations, River fetch timeouts, inform runs per kind, and job
  duration percentiles parsed from the middleware's `duration` field.
- **Metrics (after wiring).** Work runs by state, Work p95 duration, enqueue
  deduplication, resources by state, oldest failed/degraded age, and River job
  backlog age.

Import it with Dashboards → New → Import and select the `grafanacloud-logs` and
`grafanacloud-prom` data sources when prompted.

## Other gaps worth closing

- **RDS metrics.** There's no CloudWatch data source, so DB CPU, connections and
  IOPS are invisible, and the River timeout bursts can't be tied to a database
  cause. Add the CloudWatch data source for `chariot-prod-master`.
- **Pool metrics.** Export `pgxpool.Stat()` (`AcquireCount`, `AcquireDuration`,
  `EmptyAcquireCount`, `AcquiredConns`, `MaxConns`) for the pool passed to
  Delta. `EmptyAcquireCount` climbing is the most direct sign of pool starvation.
- **`db_client_operation_duration_seconds` buckets.** They're configured
  0–10000 on a metric measured in seconds, so every p99 reads 4.95s. Fix the
  buckets in the OTel SDK view.
- **Traces.** Delta's own pool isn't instrumented with `otelpgx`, so worker,
  informer and maintenance queries don't show up in Tempo.
