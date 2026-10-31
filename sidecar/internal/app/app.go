// SPDX-License-Identifier: MIT

// Package app wires the sidecar together: listeners, gRPC services, sync
// loop, logs, metrics endpoints, heartbeats and graceful shutdown.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"

	"github.com/ORG/gearbox/engine"
	"github.com/ORG/gearbox/engine/bundle"
	gearboxv1 "github.com/ORG/gearbox/gen/go/gearbox/v1"
	"github.com/ORG/gearbox/sidecar/internal/breaker"
	"github.com/ORG/gearbox/sidecar/internal/config"
	"github.com/ORG/gearbox/sidecar/internal/defs"
	"github.com/ORG/gearbox/sidecar/internal/hub"
	"github.com/ORG/gearbox/sidecar/internal/logs"
	"github.com/ORG/gearbox/sidecar/internal/obs"
	"github.com/ORG/gearbox/sidecar/internal/server"
	"github.com/ORG/gearbox/sidecar/internal/state"
	"github.com/ORG/gearbox/sidecar/internal/syncer"
)

// App is a running sidecar.
type App struct {
	Cfg     *config.Config
	State   *state.State
	Metrics *obs.Metrics
	Logger  *logs.Logger
	Loader  *syncer.Loader
	Server  *server.Server
	Syncer  *syncer.Syncer

	health  *health.Server
	ready   chan struct{}
	once    sync.Once
	logOut  *os.File
	metrics net.Listener
}

// Options for tests.
type Options struct {
	LogSinks    []logs.Sink // replaces the configured sinks
	MetricsAddr string      // overrides cfg.MetricsAddr ("" keeps it; "-" disables)
	LogFlush    time.Duration
	LogStdout   *os.File
	BreakerOpen time.Duration
}

// Ready is closed when the first bundle is active.
func (a *App) Ready() <-chan struct{} { return a.ready }

// Run starts the sidecar and blocks until ctx is cancelled, then shuts down
// gracefully within cfg.ShutdownGrace.
func Run(ctx context.Context, cfg *config.Config, opt Options) error {
	a, err := New(cfg, opt)
	if err != nil {
		return err
	}
	return a.Run(ctx)
}

// New builds the app without starting it.
func New(cfg *config.Config, opt Options) (*App, error) {
	st := state.New()
	m := obs.New(st.SyncLagSeconds)
	var keys bundle.TrustedKeys
	if cfg.TrustedKeys != "" {
		k, err := bundle.ParseTrustedKeys(cfg.TrustedKeys)
		if err != nil {
			return nil, err
		}
		keys = k
	}
	httpClient := &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 8, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second}}
	base := ""
	if cfg.ServerURL != "" {
		base = syncer.APIBase(cfg.ServerURL)
	}
	sinks := opt.LogSinks
	if sinks == nil {
		out := opt.LogStdout
		if out == nil {
			out = os.Stdout
		}
		if cfg.LogSink == "otlp" {
			slog.Warn("GEARBOX_LOG_SINK=otlp is not implemented yet; writing JSON lines to stdout")
		}
		if cfg.LogSink == "stdout" || cfg.LogSink == "both" || cfg.LogSink == "otlp" {
			sinks = append(sinks, &logs.StdoutSink{W: out})
		}
		if cfg.LogSink == "server" || cfg.LogSink == "both" {
			sinks = append(sinks, &logs.HTTPSink{URL: base + "/sidecar/v1/logs", Credential: cfg.Credential, Client: httpClient})
		}
	}
	logKey := cfg.LogHMACKey
	if len(logKey) == 0 {
		logKey = []byte("gearbox-log-hmac:" + cfg.App + ":" + cfg.Env)
		slog.Warn("GEARBOX_HMAC_KEY_FILE is not set; logged HMACs and pii segment lookups use keys derived from the app name")
	}
	logger := logs.New(cfg.LogBuffer, logKey, sinks, func(log string, n int) { m.LogDropped.Add(float64(n), log) })
	logger.SetBatch(cfg.LogBatch)
	if opt.LogFlush > 0 {
		logger.SetFlushInterval(opt.LogFlush)
	}
	a := &App{Cfg: cfg, State: st, Metrics: m, Logger: logger, health: health.NewServer(), ready: make(chan struct{})}
	a.health.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	a.health.SetServingStatus(gearboxv1.EngineService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_NOT_SERVING)
	cacheDir := cfg.CacheDir
	if cfg.Offline() {
		cacheDir = ""
	}
	app, env := cfg.App, cfg.Env
	if cfg.Offline() || cfg.ServerURL == "" {
		app, env = "", ""
	}
	a.Loader = &syncer.Loader{
		State: st, Keys: keys, PIIKey: cfg.PIIKey, MaxDecompressed: cfg.MaxDecompressedBytes(),
		Logger: logger, Metrics: m, SidecarID: cfg.SidecarID, CacheDir: cacheDir, App: app, Env: env,
		OnSwap: func(*state.Active) { a.markReady() },
	}
	br := breaker.New(func(d string) {
		m.BreakerOpen.Inc(d)
		slog.Error("decision circuit breaker opened; serving its fallback", "decision", d, "for", breaker.OpenFor)
	})
	if opt.BreakerOpen > 0 {
		br.OpenFor = opt.BreakerOpen
	}
	var hc *hub.Client
	if cfg.HubURL != "" {
		hc = hub.New(cfg.HubURL, cfg.HubTokenFile, m)
	}
	a.Server = &server.Server{State: st, Metrics: m, Logger: logger, Breakers: br, Hub: hc, SidecarID: cfg.SidecarID, MaxInputBytes: cfg.MaxInputBytes, LogSample: cfg.LogSample}
	if !cfg.Offline() {
		a.Syncer = &syncer.Syncer{Loader: a.Loader, BaseURL: base, Credential: cfg.Credential, HTTP: httpClient, PollInterval: cfg.PollInterval, Feed: cfg.Feed, FetchJitter: cfg.FetchJitter, MaxBundle: cfg.MaxBundleBytes()}
	}
	if opt.MetricsAddr != "" {
		cfg.MetricsAddr = opt.MetricsAddr
	}
	if cfg.OTLPEndpoint != "" {
		slog.Warn("GEARBOX_OTLP_ENDPOINT is not supported yet; logs go to the configured sinks", "endpoint", cfg.OTLPEndpoint)
	}
	return a, nil
}

func (a *App) markReady() {
	a.once.Do(func() {
		a.health.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
		a.health.SetServingStatus(gearboxv1.EngineService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
		close(a.ready)
	})
}

func (a *App) newGRPC(token string) *grpc.Server {
	s := grpc.NewServer(
		grpc.UnaryInterceptor(server.UnaryInterceptor(a.Metrics, a.Cfg.RPCTimeout, token)),
		grpc.MaxRecvMsgSize(a.Cfg.MaxInputBytes*server.MaxBatch+(1<<20)),
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: 2 * time.Minute, Timeout: 20 * time.Second}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 10 * time.Second, PermitWithoutStream: true}),
	)
	gearboxv1.RegisterEngineServiceServer(s, a.Server)
	healthpb.RegisterHealthServer(s, a.health)
	if a.Cfg.Reflection {
		reflection.Register(s)
	}
	return s
}

// ListenUnix creates the socket with mode 0660, replacing a stale file.
func ListenUnix(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o770); err != nil {
		return nil, err
	}
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		if c, err := net.DialTimeout("unix", path, 200*time.Millisecond); err == nil {
			c.Close()
			return nil, fmt.Errorf("%s is in use by another process", path)
		}
		_ = os.Remove(path)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

// Run serves until ctx is done.
func (a *App) Run(ctx context.Context) error {
	cfg := a.Cfg
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	logCtx, stopLogs := context.WithCancel(context.Background())
	go a.Logger.Run(logCtx)

	// Metrics and probes.
	var httpSrv *http.Server
	if cfg.MetricsAddr != "" && cfg.MetricsAddr != "-" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", a.Metrics.Reg)
		mux.HandleFunc("/livez", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok\n"))
		})
		mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
			if a.State.Ready() {
				_, _ = w.Write([]byte("ready\n"))
				return
			}
			http.Error(w, "no verified bundle loaded", http.StatusServiceUnavailable)
		})
		l, err := net.Listen("tcp", cfg.MetricsAddr)
		if err != nil {
			stopLogs()
			return fmt.Errorf("metrics listener: %w", err)
		}
		a.metrics = l
		httpSrv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() { _ = httpSrv.Serve(l) }()
	}

	// gRPC listeners.
	var servers []*grpc.Server
	ul, err := ListenUnix(cfg.Socket)
	if err != nil {
		stopLogs()
		return fmt.Errorf("unix socket: %w", err)
	}
	us := a.newGRPC("")
	servers = append(servers, us)
	go func() { _ = us.Serve(ul) }()
	slog.Info("serving", "socket", cfg.Socket, "metrics", cfg.MetricsAddr, "offline", cfg.Offline())
	if cfg.TCPAddr != "" {
		tl, err := net.Listen("tcp", cfg.TCPAddr)
		if err != nil {
			us.Stop()
			stopLogs()
			return fmt.Errorf("tcp listener: %w", err)
		}
		ts := a.newGRPC(cfg.TCPToken)
		servers = append(servers, ts)
		go func() { _ = ts.Serve(tl) }()
	}

	// Bundles.
	switch {
	case cfg.BundleFile != "":
		if err := a.Loader.LoadFile(ctx, cfg.BundleFile, "file", cfg.AllowUnsigned); err != nil {
			slog.Error("could not load --bundle-file", "error", err)
			a.State.SyncFailed(err)
		}
	case cfg.DefinitionsDir != "":
		wg.Add(1)
		go func() { defer wg.Done(); a.runDefinitions(ctx) }()
	default:
		if err := a.Loader.LoadCache(ctx); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("last-known-good bundle not usable", "error", err)
		}
		if cfg.FallbackBundle != "" {
			if err := a.Loader.LoadFile(ctx, cfg.FallbackBundle, "fallback", false); err != nil {
				slog.Info("fallback bundle not loaded", "reason", err)
			}
		}
		wg.Add(1)
		go func() { defer wg.Done(); a.Syncer.Run(ctx) }()
		if cfg.HeartbeatEvery > 0 {
			hb := &syncer.Heartbeater{BaseURL: a.Syncer.BaseURL, Credential: cfg.Credential, HTTP: a.Syncer.HTTP, Every: cfg.HeartbeatEvery, Build: a.heartbeat}
			wg.Add(1)
			go func() { defer wg.Done(); hb.Run(ctx) }()
		}
	}

	<-ctx.Done()
	slog.Info("shutting down", "grace", cfg.ShutdownGrace)
	a.health.Shutdown()
	done := make(chan struct{})
	go func() {
		for _, s := range servers {
			s.GracefulStop()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(cfg.ShutdownGrace):
		for _, s := range servers {
			s.Stop()
		}
	}
	_ = os.Remove(cfg.Socket)
	wg.Wait()
	if httpSrv != nil {
		sctx, scancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = httpSrv.Shutdown(sctx)
		scancel()
	}
	stopLogs()
	<-a.Logger.Done()
	return nil
}

// MetricsAddr returns the bound metrics address (tests).
func (a *App) MetricsAddr() string {
	if a.metrics == nil {
		return ""
	}
	return a.metrics.Addr().String()
}

func (a *App) heartbeat() syncer.Heartbeat {
	p99, cpm, fbr := a.Server.Stats()
	hb := syncer.Heartbeat{ID: a.Cfg.SidecarID, Node: a.Cfg.Node, SidecarVersion: server.Version, EngineVersion: engine.Version, Generation: a.State.Generation(), LastError: a.State.LastError(), P99Micros: p99, CallsPerMinute: cpm, FallbackRate: fbr}
	if t := a.State.LastSync(); !t.IsZero() {
		t = t.UTC()
		hb.LastSyncAt = &t
	}
	return hb
}

// runDefinitions compiles the definitions directory, and recompiles on
// change with --watch. Each reload is a new local generation.
func (a *App) runDefinitions(ctx context.Context) {
	var gen int64
	load := func() {
		set, err := defs.Load(a.Cfg.DefinitionsDir)
		if err != nil {
			slog.Error("definitions not loaded", "error", err)
			a.State.SyncFailed(err)
			return
		}
		gen++
		set.Generation = gen
		built, err := bundle.Build(set, bundle.Meta{ReleaseType: "change_request", ReleaseID: fmt.Sprintf("local-%d", gen), PublishedBy: "definitions-dir"}, bundle.Signer{})
		if err != nil {
			slog.Error("definitions not loaded", "error", err)
			a.State.SyncFailed(err)
			return
		}
		blobs := map[string][]byte{}
		for _, b := range built.Blobs {
			blobs[b.SHA256] = b.Data
		}
		if _, err := a.Loader.Install(ctx, syncer.Install{Envelope: built.Envelope, Blobs: blobs, Source: "definitions", AllowUnsigned: true}); err != nil {
			a.State.SyncFailed(err)
			return
		}
		a.State.SyncOK(time.Now())
	}
	load()
	if !a.Cfg.Watch {
		return
	}
	last, _ := defs.Fingerprint(a.Cfg.DefinitionsDir)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fp, err := defs.Fingerprint(a.Cfg.DefinitionsDir)
			if err != nil || fp == last {
				continue
			}
			last = fp
			slog.Info("definitions changed; recompiling")
			load()
		}
	}
}
