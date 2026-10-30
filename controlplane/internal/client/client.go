// Package client is a small typed client for the control-plane API, used by
// gearboxctl and the seeder. It talks HTTP to a base URL or, for in-process
// use, directly to an http.Handler.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"
)

// Client calls /api/v1.
type Client struct {
	BaseURL string // e.g. http://localhost:8080 (without /api/v1)
	Token   string // gbx_pat_… or gbx_cred_…
	HTTP    *http.Client
	// Handler, when set, serves requests in-process instead of BaseURL.
	Handler http.Handler
	// Header is added to every request (e.g. X-Gearbox-Dev-User in tests).
	Header http.Header
}

// Problem is an API error (RFC 9457).
type Problem struct {
	Status int          `json:"status"`
	Code   string       `json:"code"`
	Title  string       `json:"title"`
	Detail string       `json:"detail"`
	Errors []FieldError `json:"errors"`
}

// FieldError is one validation error.
type FieldError struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (p *Problem) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s: %s", p.Status, p.Code, p.Detail)
	for i, e := range p.Errors {
		if i == 10 {
			fmt.Fprintf(&b, "\n  … %d more", len(p.Errors)-10)
			break
		}
		fmt.Fprintf(&b, "\n  %s: %s", e.Path, e.Message)
	}
	return b.String()
}

// IsCode reports whether err is a Problem with that code.
func IsCode(err error, code string) bool {
	var p *Problem
	return errors.As(err, &p) && p.Code == code
}

// IsStatus reports whether err is a Problem with that HTTP status.
func IsStatus(err error, status int) bool {
	var p *Problem
	return errors.As(err, &p) && p.Status == status
}

// Response is a raw response.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Raw sends a request with an arbitrary body and content type.
func (c *Client) Raw(ctx context.Context, method, path string, body io.Reader, contentType string, hdr http.Header) (*Response, error) {
	u := strings.TrimRight(c.BaseURL, "/") + "/api/v1" + path
	if c.Handler != nil {
		u = "http://in-process/api/v1" + path
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	for k, vs := range c.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}
	if c.Handler != nil {
		rec := httptest.NewRecorder()
		c.Handler.ServeHTTP(rec, req)
		return &Response{Status: rec.Code, Header: rec.Header(), Body: rec.Body.Bytes()}, nil
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Minute}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: b}, err
}

// Do sends JSON (in may be nil) and decodes JSON into out (may be nil).
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	ct := ""
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body, ct = bytes.NewReader(b), "application/json"
	}
	resp, err := c.Raw(ctx, method, path, body, ct, nil)
	if err != nil {
		return err
	}
	return decode(resp, out)
}

func decode(resp *Response, out any) error {
	if resp.Status >= 400 {
		p := &Problem{Status: resp.Status}
		if json.Unmarshal(resp.Body, p) != nil || p.Code == "" {
			p.Code = http.StatusText(resp.Status)
			p.Detail = strings.TrimSpace(string(resp.Body))
		}
		p.Status = resp.Status
		return p
	}
	if out == nil || len(resp.Body) == 0 {
		return nil
	}
	if raw, ok := out.(*[]byte); ok {
		*raw = resp.Body
		return nil
	}
	return json.Unmarshal(resp.Body, out)
}

// Path joins escaped path segments: Path("apps", app, "decisions").
func Path(parts ...any) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(fmt.Sprint(p)))
	}
	return b.String()
}

// ---------------------------------------------------------------- typed helpers

// Resource is the subset of the Resource schema the CLI needs.
type Resource struct {
	Kind          string          `json:"kind"`
	Name          string          `json:"name"`
	Description   string          `json:"description,omitempty"`
	LatestVersion int64           `json:"latestVersion"`
	Draft         Draft           `json:"draft"`
	Live          map[string]Live `json:"live"`
}

type Draft struct {
	Spec       json.RawMessage `json:"spec"`
	Revision   int64           `json:"revision"`
	Validation *struct {
		Valid    bool `json:"valid"`
		Problems []struct {
			Code     string `json:"code"`
			Severity string `json:"severity"`
			Path     string `json:"path"`
			Message  string `json:"message"`
		} `json:"problems"`
	} `json:"validation,omitempty"`
}

type Live struct {
	Version int64 `json:"version"`
	Rollout *struct {
		FromVersion int64 `json:"fromVersion"`
		Percent     int   `json:"percent"`
	} `json:"rollout,omitempty"`
}

// VersionInfo of a cut version.
type VersionInfo struct {
	Version     int64  `json:"version"`
	ContentHash string `json:"contentHash"`
	Message     string `json:"message,omitempty"`
}

// GetResource returns a resource (Problem 404 when missing).
func (c *Client) GetResource(ctx context.Context, app, kind, name string) (Resource, error) {
	var r Resource
	err := c.Do(ctx, http.MethodGet, Path("apps", app, kind, name), nil, &r)
	return r, err
}

// UpsertDraft creates the resource or saves its draft when spec or
// description changed. changed reports whether anything was written.
func (c *Client) UpsertDraft(ctx context.Context, app, kind, name, description string, spec json.RawMessage) (r Resource, changed bool, err error) {
	cur, err := c.GetResource(ctx, app, kind, name)
	if IsStatus(err, http.StatusNotFound) {
		err = c.Do(ctx, http.MethodPost, Path("apps", app, kind), map[string]any{"name": name, "description": description, "spec": spec}, &r)
		return r, err == nil, err
	}
	if err != nil {
		return r, false, err
	}
	if jsonEqual(cur.Draft.Spec, spec) && cur.Description == description {
		return cur, false, nil
	}
	body := map[string]any{"revision": cur.Draft.Revision, "spec": spec, "description": description}
	err = c.Do(ctx, http.MethodPut, Path("apps", app, kind, name, "draft"), body, &r)
	return r, err == nil, err
}

// jsonEqual compares JSON semantically (ignoring read-only counters).
func jsonEqual(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	strip := func(v any) {
		if m, ok := v.(map[string]any); ok {
			delete(m, "rowCount")
			if st, ok := m["static"].(map[string]any); ok {
				delete(st, "memberCount")
			}
		}
	}
	strip(av)
	strip(bv)
	ab, _ := json.Marshal(av)
	bb, _ := json.Marshal(bv)
	return bytes.Equal(ab, bb)
}

// CutVersion cuts a version from the current draft; ok=false (no error)
// when the draft equals the latest version.
func (c *Client) CutVersion(ctx context.Context, app, kind, name, message string, allowBreaking bool) (VersionInfo, bool, error) {
	cur, err := c.GetResource(ctx, app, kind, name)
	if err != nil {
		return VersionInfo{}, false, err
	}
	var v VersionInfo
	err = c.Do(ctx, http.MethodPost, Path("apps", app, kind, name, "versions"), map[string]any{"revision": cur.Draft.Revision, "message": message, "allowBreaking": allowBreaking}, &v)
	if IsCode(err, "NO_CHANGES") {
		return VersionInfo{Version: cur.LatestVersion}, false, nil
	}
	return v, err == nil, err
}
