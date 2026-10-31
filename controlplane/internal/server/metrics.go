package server

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/ORG/gearbox/engine"
)

// metrics are exported in Prometheus text format on GEARBOX_METRICS_ADDR.
type metrics struct {
	requests  [6]atomic.Int64 // by status class 1xx..5xx (index 1..5)
	publishes atomic.Int64
	failed    atomic.Int64 // failed publishes
	watchers  atomic.Int64
}

var serverMetrics metrics

// MetricsHandler serves /metrics, /readyz and /livez.
func (s *Server) MetricsHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if _, err := s.st.ListApps(ctx, "", 1, nil); err != nil {
			http.Error(w, "store unavailable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		m := &serverMetrics
		fmt.Fprintf(w, "# HELP gearbox_server_info Server build information.\n# TYPE gearbox_server_info gauge\ngearbox_server_info{version=%q,engine=%q,engine_impl=%q} 1\n", Version, engine.Version, s.EngineName())
		fmt.Fprintf(w, "# HELP gearbox_http_requests_total API requests by status class.\n# TYPE gearbox_http_requests_total counter\n")
		for i := 1; i <= 5; i++ {
			fmt.Fprintf(w, "gearbox_http_requests_total{code=\"%dxx\"} %d\n", i, m.requests[i].Load())
		}
		fmt.Fprintf(w, "# HELP gearbox_publishes_total Published generations.\n# TYPE gearbox_publishes_total counter\ngearbox_publishes_total %d\n", m.publishes.Load())
		fmt.Fprintf(w, "# HELP gearbox_publish_failures_total Failed publish attempts.\n# TYPE gearbox_publish_failures_total counter\ngearbox_publish_failures_total %d\n", m.failed.Load())
		fmt.Fprintf(w, "# HELP gearbox_watch_streams Open sidecar change-feed streams.\n# TYPE gearbox_watch_streams gauge\ngearbox_watch_streams %d\n", m.watchers.Load())
	})
	return mux
}

func countRequest(status int) {
	if c := status / 100; c >= 1 && c <= 5 {
		serverMetrics.requests[c].Add(1)
	}
}
