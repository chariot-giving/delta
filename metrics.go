package delta

import "github.com/chariot-giving/delta/deltatype"

// MetricsCollector receives metric signals from Delta. Implementations are
// responsible for translating these to whatever observability backend the
// caller uses (Prometheus, OpenTelemetry, statsd, etc.).
//
// Methods MUST be safe for concurrent use and SHOULD return quickly: they
// are called from the informer/worker/maintenance hot paths.
//
// The metric names Delta emits are exposed as exported constants in this
// package and form part of Delta's public contract.
type MetricsCollector = deltatype.MetricsCollector

// Metric names emitted by Delta. These are stable and may be relied on by
// dashboards.
const (
	MetricInformRuns          = deltatype.MetricInformRuns
	MetricInformDuration      = deltatype.MetricInformDuration
	MetricInformObjects       = deltatype.MetricInformObjects
	MetricWorkRuns            = deltatype.MetricWorkRuns
	MetricWorkDuration        = deltatype.MetricWorkDuration
	MetricMaintenanceCleaned  = deltatype.MetricMaintenanceCleaned
	MetricMaintenanceExpired  = deltatype.MetricMaintenanceExpired
	MetricMaintenanceRescued  = deltatype.MetricMaintenanceRescued
	MetricSubscriptionDropped = deltatype.MetricSubscriptionDropped
)

// Stable label values used by Delta-emitted metrics.
const (
	ObjectResultCreated = deltatype.ObjectResultCreated
	ObjectResultUpdated = deltatype.ObjectResultUpdated
	ObjectResultSkipped = deltatype.ObjectResultSkipped
	ObjectResultDeleted = deltatype.ObjectResultDeleted

	ResultSuccess = deltatype.ResultSuccess
	ResultError   = deltatype.ResultError

	RescueTypeStuck   = deltatype.RescueTypeStuck
	RescueTypeExpired = deltatype.RescueTypeExpired

	DropSourceBus        = deltatype.DropSourceBus
	DropSourceSubscriber = deltatype.DropSourceSubscriber
)

// noopMetrics is the default MetricsCollector that drops every signal.
type noopMetrics = deltatype.NoopMetrics
