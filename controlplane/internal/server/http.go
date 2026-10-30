package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"

	"github.com/ORG/gearbox/controlplane/internal/objstore"
	"github.com/ORG/gearbox/controlplane/internal/store"
)

// FieldError is the contract's FieldError.
type FieldError struct {
	Path    string `json:"path"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// Problem is an RFC 9457 problem with the contract's stable code.
type Problem struct {
	Type     string       `json:"type"`
	Title    string       `json:"title"`
	Status   int          `json:"status"`
	Code     string       `json:"code"`
	Detail   string       `json:"detail,omitempty"`
	Instance string       `json:"instance,omitempty"`
	Errors   []FieldError `json:"errors,omitempty"`
}

func (p *Problem) Error() string { return p.Code + ": " + p.Detail }

func problemf(status int, code, title, format string, args ...any) *Problem {
	return &Problem{Type: "about:blank", Title: title, Status: status, Code: code, Detail: fmt.Sprintf(format, args...)}
}

func (p *Problem) with(errs ...FieldError) *Problem {
	p.Errors = append(p.Errors, errs...)
	return p
}

func errNotFound(format string, args ...any) *Problem {
	return problemf(http.StatusNotFound, "NOT_FOUND", "Not found", format, args...)
}

func errInvalid(format string, args ...any) *Problem {
	return problemf(http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid request", format, args...)
}

func errUnprocessable(code, title, format string, args ...any) *Problem {
	return problemf(http.StatusUnprocessableEntity, code, title, format, args...)
}

func errConflict(code, title, format string, args ...any) *Problem {
	return problemf(http.StatusConflict, code, title, format, args...)
}

func errForbidden(format string, args ...any) *Problem {
	return problemf(http.StatusForbidden, "FORBIDDEN", "Not allowed", format, args...)
}

func errUnauthenticated(format string, args ...any) *Problem {
	return problemf(http.StatusUnauthorized, "UNAUTHENTICATED", "Sign in required", format, args...)
}

// writeJSON writes v with status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// writeError maps any error to a problem response.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var p *Problem
	switch {
	case errors.As(err, &p):
	case errors.Is(err, store.ErrNotFound), errors.Is(err, objstore.ErrNotFound):
		p = errNotFound("The resource does not exist.")
	case errors.Is(err, store.ErrExists):
		p = errConflict("NAME_TAKEN", "Name taken", "A resource with that name already exists.")
	case errors.Is(err, store.ErrVersion):
		p = errConflict("CONFLICT", "Concurrent update", "Someone else changed this at the same time; retry.")
	default:
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			p = problemf(http.StatusRequestEntityTooLarge, "TOO_LARGE", "Request too large", "The request body exceeds %d bytes.", mbe.Limit)
			break
		}
		slog.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		p = problemf(http.StatusInternalServerError, "INTERNAL", "Internal error", "The server failed to handle the request.")
	}
	if p.Type == "" {
		p.Type = "about:blank"
	}
	p.Instance = r.URL.Path
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// decodeJSON reads a JSON body into v.
func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return errInvalid("A JSON body is required.")
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return errInvalid("A JSON body is required.")
	}
	if err := json.Unmarshal(data, v); err != nil {
		return errInvalid("The body is not valid JSON: %v", err)
	}
	return nil
}

// decodeOptionalJSON is decodeJSON that accepts an empty body.
func decodeOptionalJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, v); err != nil {
		return errInvalid("The body is not valid JSON: %v", err)
	}
	return nil
}

// ---------------------------------------------------------------- pagination

const (
	defaultLimit = 50
	maxLimit     = 200
)

func encodeCursor(s string) string {
	if s == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func decodeCursor(c string) (string, error) {
	if c == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", errInvalid("cursor is not valid")
	}
	return string(b), nil
}

type pageParams struct {
	Cursor string // decoded
	Limit  int
}

func pagination(r *http.Request) (pageParams, error) {
	p := pageParams{Limit: defaultLimit}
	if l := r.URL.Query().Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > maxLimit {
			return p, errInvalid("limit must be between 1 and %d", maxLimit)
		}
		p.Limit = n
	}
	c, err := decodeCursor(r.URL.Query().Get("cursor"))
	p.Cursor = c
	return p, err
}

// listResponse is {items, nextCursor}.
type listResponse[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

func list[T any](items []T, next string) listResponse[T] {
	if items == nil {
		items = []T{}
	}
	return listResponse[T]{Items: items, NextCursor: encodeCursor(next)}
}

// pageSlice paginates an in-memory slice by index.
func pageSlice[T any](items []T, p pageParams) ([]T, string, error) {
	start := 0
	if p.Cursor != "" {
		n, err := strconv.Atoi(p.Cursor)
		if err != nil || n < 0 {
			return nil, "", errInvalid("cursor is not valid")
		}
		start = n
	}
	if start > len(items) {
		start = len(items)
	}
	end := start + p.Limit
	next := ""
	if end < len(items) {
		next = strconv.Itoa(end)
	} else {
		end = len(items)
	}
	return items[start:end], next, nil
}

// ---------------------------------------------------------------- contract validation

func init() {
	openapi3filter.RegisterBodyDecoder("text/csv", func(body io.Reader, _ http.Header, _ *openapi3.SchemaRef, _ openapi3filter.EncodingFn) (any, error) {
		b, err := io.ReadAll(body)
		return string(b), err
	})
}

// validateRequest checks a request against its contract operation.
// Body errors become 422 VALIDATION_FAILED when the operation declares 422,
// otherwise 400 INVALID_ARGUMENT.
func (s *Server) validateRequest(r *http.Request, rt *route) error {
	op := rt.spec
	params := map[string]string{}
	collect := func(ps openapi3.Parameters) {
		for _, p := range ps {
			if p.Value != nil && p.Value.In == "path" {
				params[p.Value.Name] = r.PathValue(p.Value.Name)
			}
		}
	}
	collect(op.PathItem.Parameters)
	collect(op.Op.Parameters)
	in := &openapi3filter.RequestValidationInput{
		Request:    r,
		PathParams: params,
		Route:      &routers.Route{Spec: s.doc, Path: op.Path, PathItem: op.PathItem, Method: op.Method, Operation: op.Op},
		Options: &openapi3filter.Options{
			MultiError:         true,
			ExcludeRequestBody: rt.skipBody,
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
			SkipSettingDefaults: true,
		},
	}
	err := openapi3filter.ValidateRequest(r.Context(), in)
	if err == nil {
		return nil
	}
	fes, bodyErr := flattenRequestErrors(err)
	status, code, title := http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid request"
	if bodyErr && op.Op.Responses.Status(http.StatusUnprocessableEntity) != nil {
		status, code, title = http.StatusUnprocessableEntity, "VALIDATION_FAILED", "Invalid request body"
	}
	msg := "The request does not match the API contract."
	if len(fes) > 0 {
		msg = fes[0].Path + ": " + fes[0].Message
	}
	return (&Problem{Type: "about:blank", Title: title, Status: status, Code: code, Detail: msg}).with(fes...)
}

func flattenRequestErrors(err error) (out []FieldError, body bool) {
	var walk func(error, string)
	walk = func(e error, prefix string) {
		var me openapi3.MultiError
		if errors.As(e, &me) {
			for _, x := range me {
				walk(x, prefix)
			}
			return
		}
		var re *openapi3filter.RequestError
		if errors.As(e, &re) {
			p := prefix
			if re.Parameter != nil {
				p = "/" + re.Parameter.In + "/" + re.Parameter.Name
			} else if re.RequestBody != nil {
				body = true
			}
			if re.Err != nil {
				var inner openapi3.MultiError
				var se *openapi3.SchemaError
				if errors.As(re.Err, &inner) || errors.As(re.Err, &se) {
					walk(re.Err, p)
					return
				}
			}
			out = append(out, FieldError{Path: orRoot(p), Message: re.Reason + errSuffix(re.Err)})
			return
		}
		var se *openapi3.SchemaError
		if errors.As(e, &se) {
			ptr := prefix
			for _, part := range se.JSONPointer() {
				ptr += "/" + strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1")
			}
			msg := se.Reason
			if se.SchemaField != "" {
				msg = se.Reason
			}
			out = append(out, FieldError{Path: orRoot(ptr), Message: msg})
			return
		}
		out = append(out, FieldError{Path: orRoot(prefix), Message: e.Error()})
	}
	walk(err, "")
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return dedupe(out), body
}

func errSuffix(err error) string {
	if err == nil {
		return ""
	}
	return ": " + err.Error()
}

func orRoot(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

func dedupe(in []FieldError) []FieldError {
	seen := map[string]bool{}
	out := in[:0]
	for _, e := range in {
		k := e.Path + "\x00" + e.Message
		if !seen[k] {
			seen[k] = true
			out = append(out, e)
		}
	}
	return out
}

// schemaErrors validates a decoded value against a component schema and
// returns field errors with pointers prefixed by prefix.
func schemaErrors(schema *openapi3.Schema, v any, prefix string) []FieldError {
	err := schema.VisitJSON(v, openapi3.MultiErrors())
	if err == nil {
		return nil
	}
	var out []FieldError
	var walk func(error)
	walk = func(e error) {
		var me openapi3.MultiError
		if errors.As(e, &me) {
			for _, x := range me {
				walk(x)
			}
			return
		}
		var se *openapi3.SchemaError
		if errors.As(e, &se) {
			// For oneOf failures, report the most specific branch error.
			if se.Origin != nil {
				var inner openapi3.MultiError
				if errors.As(se.Origin, &inner) || errors.As(se.Origin, new(*openapi3.SchemaError)) {
					before := len(out)
					walk(se.Origin)
					if len(out) > before {
						return
					}
				}
			}
			ptr := prefix
			for _, part := range se.JSONPointer() {
				ptr += "/" + strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1")
			}
			out = append(out, FieldError{Path: orRoot(ptr), Message: se.Reason})
			return
		}
		out = append(out, FieldError{Path: orRoot(prefix), Message: e.Error()})
	}
	walk(err)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return dedupe(out)
}
