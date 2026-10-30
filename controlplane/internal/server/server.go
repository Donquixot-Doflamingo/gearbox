// Package server is the Gearbox control plane: the HTTP API described by
// contracts/openapi/gearbox-admin.v1.yaml, publishing, the sidecar feed and
// background work (webhook outbox).
package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/ORG/gearbox/controlplane/internal/apispec"
	"github.com/ORG/gearbox/controlplane/internal/auth"
	"github.com/ORG/gearbox/controlplane/internal/engineapi"
	"github.com/ORG/gearbox/controlplane/internal/feed"
	"github.com/ORG/gearbox/controlplane/internal/objstore"
	"github.com/ORG/gearbox/controlplane/internal/store"
	"github.com/ORG/gearbox/engine/bundle"
)

// Server is the control plane.
type Server struct {
	cfg     Config
	st      *store.Store
	obj     objstore.Store
	blobSig objstore.Signer
	eng     engineapi.Engine
	bun     engineapi.Bundler
	signer  bundle.Signer
	trusted bundle.TrustedKeys
	hasher  auth.Hasher
	hmacKey []byte // installation HMAC key (pii segment members; shared with sidecars)
	oidc    *auth.OIDC
	bcast   *feed.Broadcaster
	pub     feed.Publisher
	doc     *openapi3.T
	ops     map[string]apispec.Operation
	routes  []*route
	now     func() time.Time
	client  *http.Client

	versions *versionCache
	snaps    *snapshotCache
	lastUsed sync.Map // credential/token hash -> time of last lastUsedAt write

	bgCancel context.CancelFunc
	bgWG     sync.WaitGroup
	closers  []func() error
}

// Deps lets tests and tools inject dependencies; nil fields are built from
// the Config.
type Deps struct {
	Store       *store.Store
	ObjectStore objstore.Store
	Engine      engineapi.Engine
	Bundler     engineapi.Bundler
	SigningKey  ed25519.PrivateKey
	Secret      []byte
	OIDC        *auth.OIDC
	Publisher   feed.Publisher
	Broadcaster *feed.Broadcaster
	Now         func() time.Time
	HTTPClient  *http.Client
	// NoBackground disables the webhook worker (tests drive it by hand).
	NoBackground bool
}

// New builds a server.
func New(ctx context.Context, cfg Config, d Deps) (*Server, error) {
	cfg.defaults()
	s := &Server{cfg: cfg, now: d.Now, client: d.HTTPClient}
	if s.now == nil {
		s.now = func() time.Time { return time.Now().UTC() }
	}
	if s.client == nil {
		s.client = &http.Client{Timeout: 10 * time.Second}
	}
	var err error
	if s.doc, err = apispec.Load(); err != nil {
		return nil, err
	}
	if s.ops, err = apispec.Operations(); err != nil {
		return nil, err
	}

	// Store.
	s.st = d.Store
	if s.st == nil {
		b, err := OpenBackend(ctx, cfg)
		if err != nil {
			return nil, err
		}
		s.st = store.New(b)
		s.closers = append(s.closers, s.st.Close)
	}

	// Signing key and server secret.
	key := d.SigningKey
	if key == nil {
		switch {
		case cfg.SigningKey != "":
			if key, err = auth.LoadSigningKey(cfg.SigningKey); err != nil {
				return nil, err
			}
		case cfg.Dev():
			path := filepath.Join(cfg.DataDir, "signing.key")
			k, created, err := auth.LoadOrCreateKeyFile(path)
			if err != nil {
				return nil, fmt.Errorf("dev signing key: %w", err)
			}
			key = k
			if created {
				slog.Warn("generated a development signing key", "file", path)
			}
			slog.Info("sidecars must trust this key", "GEARBOX_TRUSTED_KEYS", auth.TrustedKeyLine(cfg.SigningKeyID, key))
		default:
			return nil, errors.New("GEARBOX_SIGNING_KEY is required outside development mode")
		}
	}
	s.signer = bundle.Signer{KeyID: cfg.SigningKeyID, PrivateKey: key}
	s.trusted = bundle.TrustedKeys{cfg.SigningKeyID: key.Public().(ed25519.PublicKey)}
	secret := d.Secret
	if secret == nil {
		if cfg.HMACKey != "" {
			secret = auth.LoadSecret(cfg.HMACKey)
		} else {
			// Derived from the signing key so dev restarts keep tokens valid.
			secret = auth.Hasher{Key: key.Seed()}.Derive("secret")
		}
	}
	s.hmacKey = secret
	s.hasher = auth.Hasher{Key: secret}
	s.blobSig = objstore.Signer{Key: s.hasher.Derive("blob-url"), BaseURL: cfg.PublicURL + "/api/v1", Now: s.now}

	// Object store.
	s.obj = d.ObjectStore
	if s.obj == nil {
		if s.obj, err = openObjectStore(cfg, s.blobSig); err != nil {
			return nil, err
		}
	}

	// Engine.
	s.eng, s.bun = d.Engine, d.Bundler
	if s.eng == nil || s.bun == nil {
		e, b, err := engineapi.Select(cfg.Engine)
		if err != nil {
			return nil, err
		}
		if s.eng == nil {
			s.eng = e
		}
		if s.bun == nil {
			s.bun = b
		}
	}

	// Auth.
	s.oidc = d.OIDC
	if cfg.Auth == "oidc" && s.oidc == nil {
		var secret []byte
		if cfg.OIDCClientSecretFile != "" {
			if secret, err = os.ReadFile(cfg.OIDCClientSecretFile); err != nil {
				return nil, fmt.Errorf("GEARBOX_OIDC_CLIENT_SECRET_FILE: %w", err)
			}
		}
		if s.oidc, err = auth.NewOIDC(ctx, cfg.OIDCIssuer, cfg.OIDCClientID, strings.TrimSpace(string(secret)), cfg.PublicURL+"/api/v1/auth/callback"); err != nil {
			return nil, err
		}
	}

	// Feed.
	s.bcast = d.Broadcaster
	if s.bcast == nil {
		s.bcast = feed.NewBroadcaster()
	}
	s.pub = d.Publisher
	if s.pub == nil {
		if cfg.RedisURL != "" {
			r, err := feed.NewRedis(cfg.RedisURL, s.bcast)
			if err != nil {
				return nil, fmt.Errorf("GEARBOX_REDIS_URL: %w", err)
			}
			s.pub = r
			s.closers = append(s.closers, r.Close)
		} else {
			s.pub = feed.Local{B: s.bcast}
		}
	}

	s.versions = newVersionCache(4096)
	s.snaps = newSnapshotCache()
	if err := s.buildRoutes(); err != nil {
		return nil, err
	}

	bg, cancel := context.WithCancel(context.Background())
	s.bgCancel = cancel
	if !d.NoBackground {
		s.bgWG.Add(1)
		go func() { defer s.bgWG.Done(); s.webhookWorker(bg) }()
	}
	return s, nil
}

// Close stops background work and closes the store and feed.
func (s *Server) Close() error {
	s.bgCancel()
	s.bgWG.Wait()
	var errs []error
	for i := len(s.closers) - 1; i >= 0; i-- {
		if err := s.closers[i](); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Store exposes the store (seed, tests).
func (s *Server) Store() *store.Store { return s.st }

// SigningPublicKeyLine is "id:base64" for GEARBOX_TRUSTED_KEYS.
func (s *Server) SigningPublicKeyLine() string {
	return auth.TrustedKeyLine(s.signer.KeyID, s.signer.PrivateKey)
}

// EngineName reports the engine and bundler in use.
func (s *Server) EngineName() string { return s.eng.Name() + "/" + s.bun.Name() }

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// ---------------------------------------------------------------- routing

type authMode int

const (
	authUser authMode = iota // session, PAT or dev user
	authCred                 // app credential only
	authAny                  // app credential or user
	authNone                 // public
)

type handlerFunc func(w http.ResponseWriter, r *http.Request, p *Principal) error

type route struct {
	op       string
	method   string
	pattern  string // ServeMux pattern under /api/v1
	auth     authMode
	skipBody bool // body validated by the handler (resource specs)
	h        handlerFunc
	spec     apispec.Operation
}

// Routes lists the registered operation ids (tests).
func (s *Server) Routes() []string {
	out := make([]string, 0, len(s.routes))
	for _, r := range s.routes {
		out = append(out, r.op)
	}
	return out
}

func (s *Server) buildRoutes() error {
	R := func(op string, a authMode, h handlerFunc) *route { return &route{op: op, auth: a, h: h} }
	rs := []*route{
		R("login", authNone, s.handleLogin),
		R("logout", authUser, s.handleLogout),
		R("getSession", authUser, s.handleGetSession),

		R("listApps", authUser, s.handleListApps),
		R("createApp", authUser, s.handleCreateApp),
		R("getApp", authUser, s.handleGetApp),
		R("updateApp", authUser, s.handleUpdateApp),
		R("listEnvironments", authUser, s.handleListEnvironments),
		R("updateEnvironment", authUser, s.handleUpdateEnvironment),
		R("listCredentials", authUser, s.handleListCredentials),
		R("createCredential", authUser, s.handleCreateCredential),
		R("revokeCredential", authUser, s.handleRevokeCredential),

		R("listMembers", authUser, s.handleListMembers),
		R("addMember", authUser, s.handleAddMember),
		R("updateMember", authUser, s.handleUpdateMember),
		R("removeMember", authUser, s.handleRemoveMember),
		R("createToken", authUser, s.handleCreateToken),

		R("listResources", authUser, s.handleListResources),
		{op: "createResource", auth: authUser, skipBody: true, h: s.handleCreateResource},
		R("getResource", authUser, s.handleGetResource),
		R("archiveResource", authUser, s.handleArchiveResource),
		{op: "saveDraft", auth: authUser, skipBody: true, h: s.handleSaveDraft},
		{op: "validateResource", auth: authUser, skipBody: true, h: s.handleValidateResource},
		R("listVersions", authUser, s.handleListVersions),
		R("cutVersion", authUser, s.handleCutVersion),
		R("getVersion", authUser, s.handleGetVersion),
		R("diffVersions", authUser, s.handleDiff),
		R("listUsages", authUser, s.handleUsages),

		R("validateDocument", authUser, s.handleValidateDocument),
		R("checkSchemaCompatibility", authUser, s.handleCompatibility),

		R("evaluateDecision", authUser, s.handleEvaluate),
		R("runDecisionTests", authUser, s.handleRunTests),
		R("getShadowReport", authUser, s.handleShadowReport),

		R("listSegmentMembers", authUser, s.handleListMembersOfSegment),
		R("replaceSegmentMembers", authUser, s.handleReplaceSegmentMembers),
		R("checkSegmentMembership", authUser, s.handleCheckSegment),
		R("listLookupRows", authUser, s.handleListLookupRows),
		R("replaceLookupRows", authUser, s.handleReplaceLookupRows),

		R("listChangeRequests", authUser, s.handleListCRs),
		R("createChangeRequest", authUser, s.handleCreateCR),
		R("getChangeRequest", authUser, s.handleGetCR),
		R("approveChangeRequest", authUser, s.handleApproveCR),
		R("rejectChangeRequest", authUser, s.handleRejectCR),
		R("commentChangeRequest", authUser, s.handleCommentCR),
		R("withdrawChangeRequest", authUser, s.handleWithdrawCR),

		R("listReleases", authUser, s.handleListReleases),
		R("publishRelease", authUser, s.handlePublish),
		R("getRelease", authUser, s.handleGetRelease),

		R("getOverview", authUser, s.handleOverview),
		R("listSidecars", authUser, s.handleListSidecars),
		R("searchDecisionLogs", authUser, s.handleSearchDecisionLogs),
		R("listUpdateLogs", authUser, s.handleUpdateLogs),
		R("listExposureLogs", authUser, s.handleExposureLogs),
		R("listAudit", authUser, s.handleAudit),
		R("listWebhooks", authUser, s.handleListWebhooks),
		R("createWebhook", authUser, s.handleCreateWebhook),
		R("deleteWebhook", authUser, s.handleDeleteWebhook),

		R("decide", authAny, s.handleDecide),
		R("checkGate", authAny, s.handleCheckGate),
		R("assignVariant", authAny, s.handleAssignVariant),

		R("getBundleHead", authCred, s.handleHead),
		R("watchBundles", authCred, s.handleWatch),
		R("getBlob", authNone, s.handleBlob),
		R("sidecarHeartbeat", authCred, s.handleHeartbeat),
		R("ingestLogs", authCred, s.handleIngestLogs),

		R("getExprCatalogue", authUser, s.handleExprCatalogue),
		R("getServerVersion", authNone, s.handleServerVersion),
	}
	for _, rt := range rs {
		op, ok := s.ops[rt.op]
		if !ok {
			return fmt.Errorf("server: route %s is not in the contract", rt.op)
		}
		rt.spec = op
		rt.method = op.Method
		rt.pattern = op.Path
	}
	if len(rs) != len(s.ops) {
		var missing []string
		have := map[string]bool{}
		for _, rt := range rs {
			have[rt.op] = true
		}
		for id := range s.ops {
			if !have[id] {
				missing = append(missing, id)
			}
		}
		return fmt.Errorf("server: contract operations without a handler: %v", missing)
	}
	s.routes = rs
	return nil
}

// Handler returns the full HTTP handler: API under /api/v1, health checks,
// and the static UI when GEARBOX_UI_DIR is set.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, rt := range s.routes {
		rt := rt
		mux.HandleFunc(rt.method+" /api/v1"+rt.pattern, func(w http.ResponseWriter, r *http.Request) { s.serve(w, r, rt) })
	}
	// Not in the contract (described in prose): the OIDC redirect target.
	mux.HandleFunc("GET /api/v1/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		if err := s.handleCallback(w, r); err != nil {
			writeError(w, r, err)
		}
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if _, err := s.st.ListApps(ctx, "", 1, nil); err != nil {
			writeError(w, r, problemf(503, "UNAVAILABLE", "Store unavailable", "%v", err))
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, errNotFound("No API operation matches %s %s.", r.Method, r.URL.Path))
	})
	if s.cfg.UIDir != "" {
		mux.Handle("/", spaHandler(s.cfg.UIDir))
	}
	return s.withCORS(s.withLogging(mux))
}

const maxBody = 96 << 20 // members uploads of up to 1,000,000 keys

func (s *Server) serve(w http.ResponseWriter, r *http.Request, rt *route) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	p, err := s.authenticate(r, rt)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.validateRequest(r, rt); err != nil {
		writeError(w, r, err)
		return
	}
	if err := rt.h(w, r, p); err != nil {
		writeError(w, r, err)
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) withLogging(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		h.ServeHTTP(sw, r)
		lvl := slog.LevelDebug
		if sw.status >= 500 {
			lvl = slog.LevelError
		} else if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodGet {
			lvl = slog.LevelInfo
		}
		slog.Log(r.Context(), lvl, "http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}

func (s *Server) withCORS(h http.Handler) http.Handler {
	origin := s.cfg.UIOrigin
	if origin == "" {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") == origin && strings.HasPrefix(r.URL.Path, "/api/") {
			hd := w.Header()
			hd.Set("Access-Control-Allow-Origin", origin)
			hd.Set("Access-Control-Allow-Credentials", "true")
			hd.Add("Vary", "Origin")
			hd.Set("Access-Control-Expose-Headers", "ETag, Location")
			if r.Method == http.MethodOptions {
				hd.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				hd.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token, If-None-Match, X-Gearbox-Dev-User, X-Gearbox-Dev-Role")
				hd.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

// spaHandler serves files from dir, falling back to index.html for
// client-side routes.
func spaHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Clean("/" + r.URL.Path)
		full := filepath.Join(dir, filepath.FromSlash(p))
		if st, err := os.Stat(full); err != nil || st.IsDir() && !fileExists(filepath.Join(full, "index.html")) {
			if strings.Contains(filepath.Base(p), ".") && err != nil {
				http.NotFound(w, r) // a missing asset, not a route
				return
			}
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		if strings.HasPrefix(p, "/_app/immutable/") || strings.HasPrefix(p, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fs.ServeHTTP(w, r)
	})
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
