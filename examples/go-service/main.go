// Command go-service is a tiny HTTP service that routes used-car leads by
// asking the Gearbox sidecar for the lead_routing decision.
//
//	GEARBOX_TARGET=unix:///var/run/gearbox/engine.sock go run .
//	curl -XPOST localhost:8081/route -d @sample-lead.json
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	gearbox "github.com/ORG/gearbox/clients/go"
)

// fallback is what the service does when the sidecar cannot answer in time.
var fallback = map[string]any{"route": "manual_review", "reason": "client_fallback"}

type routeResponse struct {
	Route              any    `json:"route"`
	Reason             any    `json:"reason,omitempty"`
	Version            string `json:"version"`
	DependencyHash     string `json:"dependencyHash"`
	Generation         int64  `json:"generation"`
	FallbackUsed       bool   `json:"fallbackUsed"`
	FromClientFallback bool   `json:"fromClientFallback"`
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	target := env("GEARBOX_TARGET", "unix:///var/run/gearbox/engine.sock")
	client, err := gearbox.Dial(ctx, target)
	if err != nil {
		return err
	}
	defer client.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /route", func(w http.ResponseWriter, r *http.Request) {
		var lead map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&lead); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		unit := leadID(lead)

		d, err := client.Decide(r.Context(), "lead_routing", lead,
			gearbox.WithUnit(unit),
			gearbox.WithTimeout(5*time.Millisecond),
			gearbox.WithFallback(fallback),
		)
		if err != nil {
			// Caller errors (for example INPUT_SCHEMA) are not covered by the fallback.
			log.Warn("decide failed", "unit", unit, "err", err)
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		// Log the exact version with the business event so the call can be replayed.
		log.Info("lead routed", "unit", unit, "route", d.Output["route"], "version", d.Version,
			"dependencyHash", d.DependencyHash, "generation", d.Generation,
			"fallbackUsed", d.FallbackUsed, "fromClientFallback", d.FromClientFallback)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(routeResponse{
			Route: d.Output["route"], Reason: d.Output["reason"],
			Version: d.Version, DependencyHash: d.DependencyHash, Generation: d.Generation,
			FallbackUsed: d.FallbackUsed, FromClientFallback: d.FromClientFallback,
		})
	})

	srv := &http.Server{Addr: env("EXAMPLE_ADDR", ":8081"), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", srv.Addr, "gearbox", target)

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func leadID(lead map[string]any) string {
	if l, ok := lead["lead"].(map[string]any); ok {
		if id, ok := l["id"].(string); ok {
			return id
		}
	}
	return ""
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
