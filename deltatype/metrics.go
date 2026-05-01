package deltatype

import "context"

// MetricsCollector receives metric signals from Delta. Implementations are
// responsible for translating these to whatever observability backend the
// caller uses (Prometheus, OpenTelemetry, statsd, etc.).
//
// Methods MUST be safe for concurrent use and SHOULD return quickly: they
// are called from the informer/worker/maintenance hot paths.
type MetricsCollector interface {
	// Counter increments the named counter by value with the given labels.
	Counter(ctx context.Context, name string, value float64, labels map[string]string)
	// Histogram records an observed value (typically a duration in seconds)
	// with the given labels.
	Histogram(ctx context.Context, name string, value float64, labels map[string]string)
}

// Metric names emitted by Delta. These are stable and may be relied on by
// dashboards.
const (
	// MetricInformRuns counts inform job invocations. Label: result=success|error.
	MetricInformRuns = "delta.inform.runs"
	// MetricInformDuration records inform job duration in seconds.
	MetricInformDuration = "delta.inform.duration"
	// MetricInformObjects counts objects processed by the informer.
	// Labels: kind, result=created|updated|skipped|deleted.
	MetricInformObjects = "delta.inform.objects"

	// MetricWorkRuns counts Work() invocations. Labels: kind, state=synced|failed|degraded|deleted.
	MetricWorkRuns = "delta.work.runs"
	// MetricWorkDuration records Work() duration in seconds. Labels: kind, state.
	MetricWorkDuration = "delta.work.duration"

	// MetricMaintenanceCleaned counts resources hard-deleted by the cleaner.
	// Labels: namespace.
	MetricMaintenanceCleaned = "delta.maintenance.cleaned"
	// MetricMaintenanceExpired counts resources expired by the namespace expirer.
	// Labels: namespace.
	MetricMaintenanceExpired = "delta.maintenance.expired"
	// MetricMaintenanceRescued counts resources rescued by the rescheduler.
	// Labels: type=stuck|expired.
	MetricMaintenanceRescued = "delta.maintenance.rescued"

	// MetricSubscriptionDropped counts events dropped because a subscriber's
	// channel was full. Label: category.
	MetricSubscriptionDropped = "delta.subscription.dropped"
)

// Stable label values used by Delta-emitted metrics.
const (
	ObjectResultCreated = "created"
	ObjectResultUpdated = "updated"
	ObjectResultSkipped = "skipped"
	ObjectResultDeleted = "deleted"

	ResultSuccess = "success"
	ResultError   = "error"

	RescueTypeStuck   = "stuck"
	RescueTypeExpired = "expired"
)

// NoopMetrics is a MetricsCollector that drops every signal. It's the
// default when Config.Metrics is nil.
type NoopMetrics struct{}

func (NoopMetrics) Counter(context.Context, string, float64, map[string]string)   {}
func (NoopMetrics) Histogram(context.Context, string, float64, map[string]string) {}
