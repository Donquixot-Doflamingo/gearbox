// SPDX-License-Identifier: MIT

// Command gearbox-sidecar serves Gearbox decisions over gRPC next to an
// application. See sidecar/README.md.
//
//	gearbox-sidecar                         # sync from the control plane (GEARBOX_* env)
//	gearbox-sidecar --bundle-file f.bundle  # offline bundle
//	gearbox-sidecar --definitions-dir d --watch --allow-unsigned
//	gearbox-sidecar healthcheck             # exit 0 when /readyz is OK
//	gearbox-sidecar version
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ORG/gearbox/engine"
	"github.com/ORG/gearbox/sidecar/internal/app"
	"github.com/ORG/gearbox/sidecar/internal/config"
	"github.com/ORG/gearbox/sidecar/internal/server"
)

// version is set with -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	server.Version = version
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel()})))
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "healthcheck":
			os.Exit(healthcheck())
		case "version":
			fmt.Printf("gearbox-sidecar %s (engine %s)\n", version, engine.Version)
			return
		}
	}
	cfg, err := config.Load(args, os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := app.Run(ctx, cfg, app.Options{}); err != nil {
		slog.Error("sidecar stopped", "error", err)
		os.Exit(1)
	}
}

func logLevel() slog.Level {
	switch os.Getenv("GEARBOX_LOG_LEVEL") {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

// healthcheck calls /readyz on the metrics address (distroless images have
// no curl).
func healthcheck() int {
	addr := os.Getenv("GEARBOX_METRICS_ADDR")
	if addr == "" {
		addr = ":9464"
	}
	cfg := &config.Config{MetricsAddr: addr}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(cfg.LocalMetricsURL("/readyz"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: not ready:", resp.Status)
		return 1
	}
	return 0
}
