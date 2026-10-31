// Command gearbox-server runs the Gearbox control plane.
//
//	gearbox-server [serve] [--seed]   run the API (default)
//	gearbox-server seed [--demo] [--dir DIR] [--env dev]
//	                                  create the demo app and publish it, then exit
//	gearbox-server keygen [--out DIR] [--id ID] [--if-missing]
//	                                  write an Ed25519 signing key and trusted-keys line
//	gearbox-server version
//
// Configuration comes from GEARBOX_* environment variables (see
// controlplane/README.md and docs/04-reference.md).
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ORG/gearbox/controlplane/internal/auth"
	"github.com/ORG/gearbox/controlplane/internal/client"
	"github.com/ORG/gearbox/controlplane/internal/engineapi"
	"github.com/ORG/gearbox/controlplane/internal/seed"
	"github.com/ORG/gearbox/controlplane/internal/server"
	"github.com/ORG/gearbox/engine"
)

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve(args)
	case "seed":
		err = seedCmd(args)
	case "keygen":
		err = keygen(args)
	case "version":
		fmt.Printf("gearbox-server %s (engine %s)\n", server.Version, engine.Version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gearbox-server:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  gearbox-server [serve] [--seed]                     run the control plane
  gearbox-server seed [--demo] [--dir DIR] [--env E]  seed the demo app and exit
  gearbox-server keygen [--out DIR] [--id ID] [--if-missing]
  gearbox-server version
`)
}

func setupLogging(level string) {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	h := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: l})
	if os.Getenv("GEARBOX_LOG_FORMAT") == "text" {
		h2 := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l})
		slog.SetDefault(slog.New(h2))
		return
	}
	slog.SetDefault(slog.New(h))
}

func newServer(ctx context.Context) (*server.Server, server.Config, error) {
	cfg, err := server.FromEnv()
	if err != nil {
		return nil, cfg, err
	}
	setupLogging(cfg.LogLevel)
	if cfg.Dev() {
		slog.Warn("development auth: every request is signed in as " + cfg.DevUser + " (" + cfg.DevRole + "); set GEARBOX_AUTH_MODE=oidc in production")
	}
	if e, b := engineapi.RealImplemented(); !e || !b {
		slog.Warn("data-plane engine not fully available; using the lite fallback where needed", "engine", e, "bundle", b, "GEARBOX_ENGINE", cfg.Engine)
	}
	s, err := server.New(ctx, cfg, server.Deps{})
	return s, cfg, err
}

func runSeed(ctx context.Context, s *server.Server, cfg server.Config, dir, env, credential string) error {
	c := &client.Client{Handler: s.SystemHandler()}
	opt := seed.Options{Env: env, Credential: credential, Log: os.Stdout,
		RegisterCredential: func(ctx context.Context, app, env, secret string) (string, error) {
			out, err := s.CreateCredential(ctx, &server.Principal{Kind: "dev"}, app, env, "seed", secret)
			return out.Credential.ID, err
		}}
	var res seed.Result
	var err error
	if dir != "" {
		res, err = seed.Dir(ctx, c, dir, opt)
	} else {
		res, err = seed.Demo(ctx, c, opt)
	}
	if err != nil {
		return err
	}
	fmt.Printf("\nseeded %s: %d definitions, %s generation %d\n", seed.App, res.ResourcesCount, env, res.Generation)
	if res.Credential != "" {
		fmt.Printf("sidecar credential for %s/%s (shown once): %s\n", seed.App, env, res.Credential)
	}
	fmt.Printf("trusted key: %s\n", s.SigningPublicKeyLine())
	return nil
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	doSeed := fs.Bool("seed", false, "seed the demo app before serving (GEARBOX_SEED)")
	_ = fs.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s, cfg, err := newServer(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	if *doSeed || cfg.Seed {
		if err := runSeed(ctx, s, cfg, cfg.SeedDir, "dev", cfg.SeedCredential); err != nil {
			return fmt.Errorf("seed: %w", err)
		}
	}
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute, // large member uploads
		IdleTimeout:       2 * time.Minute,
		// No WriteTimeout: /sidecar/v1/watch streams for hours.
	}
	errc := make(chan error, 2)
	go func() {
		slog.Info("gearbox-server listening", "addr", cfg.Addr, "public_url", cfg.PublicURL, "store", redact(cfg.Store), "object_store", cfg.ObjectStore, "engine", s.EngineName(), "auth", cfg.Auth)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	var msrv *http.Server
	if cfg.MetricsAddr != "" && cfg.MetricsAddr != "off" && cfg.MetricsAddr != cfg.Addr {
		msrv = &http.Server{Addr: cfg.MetricsAddr, Handler: s.MetricsHandler(), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := msrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("metrics listener failed", "addr", cfg.MetricsAddr, "err", err)
			}
		}()
	}
	select {
	case <-ctx.Done():
	case err := <-errc:
		return err
	}
	slog.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if msrv != nil {
		_ = msrv.Shutdown(sctx)
	}
	// SSE watchers end when their request context is cancelled by Shutdown's
	// connection close; sidecars reconnect to another replica.
	go func() { <-sctx.Done(); _ = srv.Close() }()
	return srv.Shutdown(sctx)
}

func redact(u string) string {
	if i := strings.Index(u, "@"); i > 0 && strings.Contains(u, "://") {
		return u[:strings.Index(u, "://")+3] + "…" + u[i:]
	}
	return u
}

func seedCmd(args []string) error {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	_ = fs.Bool("demo", true, "seed the built-in demo app (lead_service)")
	dir := fs.String("dir", "", "definitions directory to seed instead of the built-in demo")
	env := fs.String("env", "dev", "environment to publish to")
	cred := fs.String("credential", os.Getenv("GEARBOX_SEED_CREDENTIAL"), "register this secret as the sidecar credential")
	_ = fs.Parse(args)
	ctx := context.Background()
	s, cfg, err := newServer(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	if strings.HasPrefix(cfg.Store, "memory") || cfg.Store == "" {
		fmt.Fprintln(os.Stderr, "note: GEARBOX_STORE=memory keeps nothing after this command exits; use GEARBOX_STORE=file:///path (or mongo) to seed for a later `serve`, or run `serve --seed`")
	}
	return runSeed(ctx, s, cfg, *dir, *env, *cred)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "", "directory for signing.key and trusted-keys.txt (default: print only)")
	id := fs.String("id", "", "key id (default: gbx-<date>)")
	ifMissing := fs.Bool("if-missing", false, "keep an existing signing.key")
	_ = fs.Parse(args)
	if *id == "" {
		*id = "gbx-" + time.Now().UTC().Format("20060102")
	}
	var key ed25519.PrivateKey
	if *out != "" {
		path := filepath.Join(*out, "signing.key")
		if b, err := os.ReadFile(path); err == nil {
			if !*ifMissing {
				return fmt.Errorf("%s exists (use --if-missing to keep it)", path)
			}
			k, err := auth.ParsePrivateKey(b)
			if err != nil {
				return err
			}
			key = k
			if t, err := os.ReadFile(filepath.Join(*out, "trusted-keys.txt")); err == nil {
				if kid, _, ok := strings.Cut(strings.TrimSpace(string(t)), ":"); ok {
					*id = kid
				}
			}
		}
	}
	if key == nil {
		_, k, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		key = k
	}
	line := auth.TrustedKeyLine(*id, key)
	if *out != "" {
		if err := os.MkdirAll(*out, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(*out, "signing.key"), []byte(base64.StdEncoding.EncodeToString(key.Seed())+"\n"), 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(*out, "trusted-keys.txt"), []byte(line+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %s and %s\n", filepath.Join(*out, "signing.key"), filepath.Join(*out, "trusted-keys.txt"))
		fmt.Printf("GEARBOX_SIGNING_KEY_FILE=%s GEARBOX_SIGNING_KEY_ID=%s\n", filepath.Join(*out, "signing.key"), *id)
	} else {
		fmt.Printf("GEARBOX_SIGNING_KEY=%s\nGEARBOX_SIGNING_KEY_ID=%s\n", base64.StdEncoding.EncodeToString(key.Seed()), *id)
	}
	fmt.Printf("trusted key: %s\n", line)
	return nil
}
