package delta

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// recordingMetrics is a simple MetricsCollector that captures every call
// for inspection in tests.
type recordingMetrics struct {
	mu         sync.Mutex
	counters   []recordedMetric
	histograms []recordedMetric
}

type recordedMetric struct {
	name   string
	value  float64
	labels map[string]string
}

func (r *recordingMetrics) Counter(_ context.Context, name string, value float64, labels map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters = append(r.counters, recordedMetric{name: name, value: value, labels: copyLabels(labels)})
}

func (r *recordingMetrics) Histogram(_ context.Context, name string, value float64, labels map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.histograms = append(r.histograms, recordedMetric{name: name, value: value, labels: copyLabels(labels)})
}

func (r *recordingMetrics) countersByName(name string) []recordedMetric {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedMetric, 0, len(r.counters))
	for _, m := range r.counters {
		if m.name == name {
			out = append(out, m)
		}
	}
	return out
}

func copyLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		out[k] = v
	}
	return out
}

func TestNoopMetrics_DoesNotPanic(t *testing.T) {
	t.Parallel()

	var m MetricsCollector = noopMetrics{}
	m.Counter(context.Background(), "x", 1, nil)
	m.Histogram(context.Background(), "x", 1, map[string]string{"k": "v"})
}

func TestRecordingMetrics_Captures(t *testing.T) {
	t.Parallel()

	rec := &recordingMetrics{}
	var collector MetricsCollector = rec
	collector.Counter(context.Background(), MetricInformObjects, 1, map[string]string{"kind": "k1", "result": ObjectResultCreated})
	collector.Histogram(context.Background(), MetricWorkDuration, 0.5, map[string]string{"kind": "k1", "state": "synced"})

	require.Len(t, rec.counters, 1)
	require.Equal(t, MetricInformObjects, rec.counters[0].name)
	require.Equal(t, ObjectResultCreated, rec.counters[0].labels["result"])
	require.Len(t, rec.histograms, 1)
	require.Equal(t, MetricWorkDuration, rec.histograms[0].name)
}
