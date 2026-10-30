package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"

	"github.com/ORG/gearbox/controlplane/internal/apispec"
	"github.com/ORG/gearbox/controlplane/internal/client"
	"github.com/ORG/gearbox/controlplane/internal/engineapi"
	"github.com/ORG/gearbox/controlplane/internal/objstore"
	"github.com/ORG/gearbox/controlplane/internal/store"
)

var (
	coverMu  sync.Mutex
	covered  = map[string]int{}
	statuses = map[string]map[int]bool{}
)

func init() {
	openapi3filter.RegisterBodyDecoder("text/event-stream", func(body io.Reader, _ http.Header, _ *openapi3.SchemaRef, _ openapi3filter.EncodingFn) (any, error) {
		b, err := io.ReadAll(body)
		return string(b), err
	})
}

func TestMain(m *testing.M) {
	flag.Parse()
	if !testing.Verbose() {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 4})))
	}
	code := m.Run()
	full := flag.Lookup("test.run").Value.String() == "" && !testing.Short()
	if full && code == 0 {
		ops, _ := apispec.Operations()
		var missing []string
		for id := range ops {
			if covered[id] == 0 {
				missing = append(missing, id)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "contract coverage: %d operations never validated: %v\n", len(missing), missing)
			code = 1
		} else {
			n := 0
			for _, s := range statuses {
				n += len(s)
			}
			fmt.Fprintf(os.Stderr, "contract coverage: all %d operations validated (%d operation/status pairs)\n", len(ops), n)
		}
	}
	os.Exit(code)
}

// teeWriter copies the response for validation while streaming it.
type teeWriter struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (w *teeWriter) WriteHeader(c int) { w.status = c; w.ResponseWriter.WriteHeader(c) }
func (w *teeWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	w.buf.Write(b)
	return w.ResponseWriter.Write(b)
}
func (w *teeWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *teeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// matchRoute finds the contract operation for a request path.
func (s *Server) matchRoute(method, path string) (*route, map[string]string) {
	path = strings.TrimPrefix(path, "/api/v1")
	segs := strings.Split(strings.Trim(path, "/"), "/")
	var best *route
	var bestParams map[string]string
	bestLit := -1
	for _, rt := range s.routes {
		if rt.method != method {
			continue
		}
		ps := strings.Split(strings.Trim(rt.pattern, "/"), "/")
		if len(ps) != len(segs) {
			continue
		}
		params, lit, ok := map[string]string{}, 0, true
		for i, p := range ps {
			if strings.HasPrefix(p, "{") {
				params[strings.Trim(p, "{}")] = segs[i]
			} else if p == segs[i] {
				lit++
			} else {
				ok = false
				break
			}
		}
		if ok && lit > bestLit {
			best, bestParams, bestLit = rt, params, lit
		}
	}
	return best, bestParams
}

// contractValidator wraps the handler and checks every response.
func (s *Server) contractValidator(t *testing.T, h http.Handler) http.Handler {
	problem := apispec.Schema("Problem")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tw := &teeWriter{ResponseWriter: w}
		h.ServeHTTP(tw, r)
		rt, params := s.matchRoute(r.Method, r.URL.Path)
		if rt == nil || !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			return
		}
		status := tw.status
		if status == 0 {
			status = 200
		}
		coverMu.Lock()
		covered[rt.op]++
		if statuses[rt.op] == nil {
			statuses[rt.op] = map[int]bool{}
		}
		statuses[rt.op][status] = true
		coverMu.Unlock()
		declared := rt.spec.Op.Responses.Status(status) != nil
		if !declared {
			if status < 400 {
				t.Errorf("contract: %s %s returned undeclared status %d", r.Method, r.URL.Path, status)
				return
			}
			if ct := tw.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
				t.Errorf("contract: %s %s error %d has content type %q", r.Method, r.URL.Path, status, ct)
				return
			}
			var v any
			if err := json.Unmarshal(tw.buf.Bytes(), &v); err != nil {
				t.Errorf("contract: %s %s error body is not JSON: %v", r.Method, r.URL.Path, err)
				return
			}
			if err := problem.VisitJSON(v); err != nil {
				t.Errorf("contract: %s %s error body is not a Problem: %v", r.Method, r.URL.Path, err)
			}
			return
		}
		in := &openapi3filter.ResponseValidationInput{
			RequestValidationInput: &openapi3filter.RequestValidationInput{
				Request: r, PathParams: params,
				Route: &routers.Route{Spec: s.doc, Path: rt.spec.Path, PathItem: rt.spec.PathItem, Method: rt.method, Operation: rt.spec.Op},
			},
			Status: status, Header: tw.Header(), Body: io.NopCloser(bytes.NewReader(tw.buf.Bytes())),
			Options: &openapi3filter.Options{MultiError: true, IncludeResponseStatus: true},
		}
		if err := openapi3filter.ValidateResponse(context.Background(), in); err != nil {
			body := tw.buf.String()
			if len(body) > 600 {
				body = body[:600] + "…"
			}
			t.Errorf("contract: %s %s (%s) status %d does not match the contract: %v\nbody: %s", r.Method, r.URL.Path, rt.op, status, err, body)
		}
	})
}

// ---------------------------------------------------------------- harness

type harness struct {
	t    *testing.T
	s    *Server
	ts   *httptest.Server
	st   *store.Store
	obj  objstore.Store
	key  ed25519.PrivateKey
	user string // default X-Gearbox-Dev-User
}

type harnessOpt func(*Config, *Deps)

func withEngine(e engineapi.Engine) harnessOpt {
	return func(_ *Config, d *Deps) { d.Engine = e }
}

func newHarness(t *testing.T, opts ...harnessOpt) *harness {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	mem, _ := store.NewMemory("")
	st := store.New(mem)
	_, key, _ := ed25519.GenerateKey(nil)
	cfg := Config{Auth: "dev", PublicURL: "http://" + ts.Listener.Addr().String(), SigningKeyID: "test", Engine: "lite", PingInterval: 50e6}
	d := Deps{Store: st, SigningKey: key, Secret: []byte("test-secret"), NoBackground: true, Engine: engineapi.Lite{}, Bundler: engineapi.LiteBundler{}}
	for _, o := range opts {
		o(&cfg, &d)
	}
	if cfg.DataDir == "" {
		cfg.DataDir = t.TempDir()
	}
	if cfg.ObjectStore == "" {
		cfg.ObjectStore = "file://" + t.TempDir()
	}
	s, err := New(context.Background(), cfg, d)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, s: s, ts: ts, st: st, obj: s.obj, key: key, user: "admin@example.com"}
	ts.Config.Handler = s.contractValidator(t, s.Handler())
	ts.Start()
	t.Cleanup(func() { ts.Close(); _ = s.Close() })
	return h
}

// client returns an API client acting as the given dev user.
func (h *harness) client(user string) *client.Client {
	hdr := http.Header{}
	if user != "" {
		hdr.Set("X-Gearbox-Dev-User", user)
	}
	return &client.Client{BaseURL: h.ts.URL, Header: hdr}
}

type resp struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r resp) JSON(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("decode %s: %v", r.Body, err)
	}
}

func (r resp) Map(t *testing.T) map[string]any {
	var m map[string]any
	r.JSON(t, &m)
	return m
}

func (r resp) Code(t *testing.T) string {
	var p struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(r.Body, &p)
	return p.Code
}

type reqOpt func(*http.Request)

func as(user string) reqOpt { return func(r *http.Request) { r.Header.Set("X-Gearbox-Dev-User", user) } }
func bearerTok(tok string) reqOpt {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
}
func hdr(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

type csvBody string

// do sends a request; body may be nil, a csvBody, []byte (JSON) or any JSON value.
func (h *harness) do(method, path string, body any, opts ...reqOpt) resp {
	h.t.Helper()
	var rd io.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
	case csvBody:
		rd, ct = strings.NewReader(string(b)), "text/csv"
	case []byte:
		rd, ct = bytes.NewReader(b), "application/json"
	case string:
		rd, ct = strings.NewReader(b), "application/json"
	default:
		j, _ := json.Marshal(b)
		rd, ct = bytes.NewReader(j), "application/json"
	}
	req, _ := http.NewRequest(method, h.ts.URL+"/api/v1"+path, rd)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set("X-Gearbox-Dev-User", h.user)
	for _, o := range opts {
		o(req)
	}
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{Status: res.StatusCode, Header: res.Header, Body: b}
}

// must asserts the status.
func (h *harness) must(status int, method, path string, body any, opts ...reqOpt) resp {
	h.t.Helper()
	r := h.do(method, path, body, opts...)
	if r.Status != status {
		h.t.Fatalf("%s %s: status %d, want %d: %s", method, path, r.Status, status, r.Body)
	}
	return r
}

// mustCode asserts status and problem code.
func (h *harness) mustCode(status int, code, method, path string, body any, opts ...reqOpt) resp {
	h.t.Helper()
	r := h.must(status, method, path, body, opts...)
	if got := r.Code(h.t); got != code {
		h.t.Fatalf("%s %s: code %q, want %q: %s", method, path, got, code, r.Body)
	}
	return r
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
