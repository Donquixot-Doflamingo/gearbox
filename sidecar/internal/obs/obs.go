// SPDX-License-Identifier: MIT

// Package obs defines the sidecar's Prometheus metrics (names from the
// observability design, plus gearbox_decision_breaker_open_total).
package obs

import (
	"github.com/ORG/gearbox/sidecar/internal/metrics"
)

// Metrics are all sidecar metrics.
type Metrics struct {
	Reg              *metrics.Registry
	Decisions        *metrics.Counter
	DecisionDuration *metrics.Histogram
	NodeErrors       *metrics.Counter
	Generation       *metrics.Gauge
	SyncFailures     *metrics.Counter
	SwapDuration     *metrics.Histogram
	AICalls          *metrics.Counter
	AIDuration       *metrics.Histogram
	LogDropped       *metrics.Counter
	BreakerOpen      *metrics.Counter
	Inflight         *metrics.Gauge
	Exposures        *metrics.Counter
	Updates          *metrics.Counter
}

// New registers every metric. syncLag returns seconds since the last
// successful sync check.
func New(syncLag func() float64) *Metrics {
	r := metrics.NewRegistry()
	m := &Metrics{
		Reg:              r,
		Decisions:        r.NewCounter("gearbox_decisions_total", "Decide calls by decision, version, gRPC status and fallback.", "decision", "version", "status", "fallback"),
		DecisionDuration: r.NewHistogram("gearbox_decision_duration_seconds", "Evaluation time inside the sidecar (excludes transport).", metrics.DefaultLatencyBuckets, "decision", "version"),
		NodeErrors:       r.NewCounter("gearbox_node_errors_total", "Nodes that failed (took onError, fallback or returned NODE_ERROR).", "decision", "node"),
		Generation:       r.NewGauge("gearbox_bundle_generation", "Generation of the active bundle."),
		SyncFailures:     r.NewCounter("gearbox_sync_failures_total", "Failed sync attempts by reason.", "reason"),
		SwapDuration:     r.NewHistogram("gearbox_swap_duration_seconds", "Time from download start to swap.", []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30}),
		AICalls:          r.NewCounter("gearbox_ai_calls_total", "AI node calls by result.", "decision", "node", "mode", "result"),
		AIDuration:       r.NewHistogram("gearbox_ai_duration_seconds", "AI call latency.", []float64{.05, .1, .25, .5, 1, 2, 5, 10}, "decision", "node"),
		LogDropped:       r.NewCounter("gearbox_log_dropped_total", "Log records dropped because a buffer was full or a sink failed.", "log"),
		BreakerOpen:      r.NewCounter("gearbox_decision_breaker_open_total", "Times a decision's circuit breaker opened.", "decision"),
		Inflight:         r.NewGauge("gearbox_inflight_requests", "RPCs being served."),
		Exposures:        r.NewCounter("gearbox_exposures_total", "Exposure records written.", "experiment"),
		Updates:          r.NewCounter("gearbox_updates_total", "Update records by result.", "result"),
	}
	r.NewGaugeFunc("gearbox_sync_lag_seconds", "Seconds since the last successful sync check.", syncLag)
	return m
}
