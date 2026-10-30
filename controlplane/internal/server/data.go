package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/ORG/gearbox/controlplane/internal/model"
	"github.com/ORG/gearbox/controlplane/internal/store"
)

const maxMembers = 1_000_000

// PIIMember is the stored form of a pii segment member: hex HMAC-SHA256
// under the installation HMAC key (GEARBOX_HMAC_KEY_FILE, shared with
// sidecars so they can test membership).
func PIIMember(key []byte, member string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(member))
	return hex.EncodeToString(m.Sum(nil))
}

func isCSV(r *http.Request) bool {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return mt == "text/csv"
}

type segmentSpec struct {
	Type   string `json:"type"`
	Static *struct {
		KeyType string `json:"keyType"`
		PII     bool   `json:"pii"`
	} `json:"static"`
}

func (s *Server) handleReplaceSegmentMembers(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, name := r.PathValue("app"), r.PathValue("name")
	rec, ver, err := s.loadResource(ctx, p, app, model.KindSegments, name)
	if err != nil {
		return err
	}
	if err := p.require(model.RoleAuthor, app); err != nil {
		return err
	}
	var spec segmentSpec
	_ = json.Unmarshal(rec.Draft.Spec, &spec)
	if spec.Type != "static" {
		return errUnprocessable("VALIDATION_FAILED", "Not a static segment", "Only static segments have uploaded members; %s is %s.", name, spec.Type)
	}
	var members []string
	if isCSV(r) {
		cr := csv.NewReader(r.Body)
		cr.FieldsPerRecord = -1
		first := true
		for {
			row, err := cr.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				var mbe *http.MaxBytesError
				if errors.As(err, &mbe) {
					return err
				}
				return errUnprocessable("VALIDATION_FAILED", "Invalid CSV", "%v", err)
			}
			if len(row) == 0 {
				continue
			}
			v := strings.TrimSpace(row[0])
			if first && strings.EqualFold(v, "key") {
				first = false
				continue
			}
			first = false
			if v != "" {
				members = append(members, v)
			}
		}
	} else {
		var in struct {
			Members []string `json:"members"`
		}
		if err := decodeJSON(r, &in); err != nil {
			return err
		}
		members = in.Members
	}
	if len(members) > maxMembers {
		return errUnprocessable("VALIDATION_FAILED", "Too many members", "At most %d members.", maxMembers)
	}
	pii, numeric := false, false
	if spec.Static != nil {
		pii, numeric = spec.Static.PII, spec.Static.KeyType == "number"
	}
	var bad []FieldError
	seen := make(map[string]bool, len(members))
	out := make([]string, 0, len(members))
	for i, m := range members {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if numeric {
			f, err := strconv.ParseFloat(m, 64)
			if err != nil {
				if len(bad) < 50 {
					bad = append(bad, FieldError{Path: fmt.Sprintf("/members/%d", i), Message: "not a number"})
				}
				continue
			}
			m = strconv.FormatFloat(f, 'f', -1, 64)
		}
		if pii {
			m = PIIMember(s.hmacKey, m)
		}
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	if len(bad) > 0 {
		return errUnprocessable("VALIDATION_FAILED", "Invalid members", "%d members are not numbers (keyType number).", len(bad)).with(bad...)
	}
	sort.Strings(out)
	ref, err := s.storeData(ctx, out, len(out))
	if err != nil {
		return err
	}
	rec.Draft.Data = ref
	rec.Draft.Revision++
	rec.Draft.UpdatedAt, rec.Draft.UpdatedBy = s.now(), p.Actor()
	rec.UpdatedAt, rec.UpdatedBy = rec.Draft.UpdatedAt, p.Actor()
	if err := s.st.PutResource(ctx, rec, ver); err != nil {
		if errors.Is(err, store.ErrVersion) {
			return errConflict("REVISION_CONFLICT", "Draft changed", "The draft changed during the upload; retry.")
		}
		return err
	}
	s.audit(ctx, p, "segment.members_replaced", app, "", "segments/"+name, map[string]any{"total": len(out), "sha256": ref.SHA256})
	writeJSON(w, http.StatusOK, map[string]any{"total": len(out), "revision": rec.Draft.Revision})
	return nil
}

func (s *Server) handleListMembersOfSegment(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	rec, _, err := s.loadResource(ctx, p, r.PathValue("app"), model.KindSegments, r.PathValue("name"))
	if err != nil {
		return err
	}
	pg, err := pagination(r)
	if err != nil {
		return err
	}
	members, err := s.loadMembers(ctx, rec.Draft.Data)
	if err != nil {
		return err
	}
	if prefix := r.URL.Query().Get("prefix"); prefix != "" {
		i := sort.SearchStrings(members, prefix)
		j := i
		for j < len(members) && strings.HasPrefix(members[j], prefix) {
			j++
		}
		members = members[i:j]
	}
	items, next, err := pageSlice(members, pg)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, totalPage[string]{Items: nonNil(items), Total: len(members), NextCursor: encodeCursor(next)})
	return nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// totalPage is {items, total, nextCursor}.
type totalPage[T any] struct {
	Items      []T    `json:"items"`
	Total      int    `json:"total"`
	NextCursor string `json:"nextCursor,omitempty"`
}

func (s *Server) handleCheckSegment(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	rec, _, err := s.loadResource(ctx, p, r.PathValue("app"), model.KindSegments, r.PathValue("name"))
	if err != nil {
		return err
	}
	var in struct {
		sourceSel
		Key        string          `json:"key"`
		Unit       string          `json:"unit"`
		Attributes json.RawMessage `json:"attributes"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	snap, _, err := s.compileTarget(ctx, rec, in.sourceSel)
	if err != nil {
		return err
	}
	key := in.Key
	var spec segmentSpec
	_ = json.Unmarshal(rec.Draft.Spec, &spec)
	if spec.Type == "static" && spec.Static != nil && spec.Static.PII && key != "" {
		key = PIIMember(s.hmacKey, key)
	}
	member, reason, err := snap.CheckSegment(rec.Name, key, in.Unit, in.Attributes)
	if err != nil {
		if pr := engineErrorProblem(err, http.StatusUnprocessableEntity); pr != nil {
			return pr
		}
		return errInvalid("%v", err)
	}
	out := map[string]any{"member": member}
	if reason != "" {
		out["reason"] = reason
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// ---------------------------------------------------------------- lookups

func (s *Server) handleReplaceLookupRows(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, name := r.PathValue("app"), r.PathValue("name")
	rec, ver, err := s.loadResource(ctx, p, app, model.KindLookups, name)
	if err != nil {
		return err
	}
	if err := p.require(model.RoleAuthor, app); err != nil {
		return err
	}
	var spec struct {
		KeyType     string `json:"keyType"`
		ValueSchema struct {
			Name    string `json:"name"`
			Version int64  `json:"version"`
		} `json:"valueSchema"`
	}
	_ = json.Unmarshal(rec.Draft.Spec, &spec)
	var rows []model.LookupRow
	if isCSV(r) {
		cr := csv.NewReader(r.Body)
		cr.FieldsPerRecord = -1
		cr.LazyQuotes = true
		first := true
		line := 0
		for {
			rec, err := cr.Read()
			if err == io.EOF {
				break
			}
			line++
			if err != nil {
				return errUnprocessable("VALIDATION_FAILED", "Invalid CSV", "%v", err)
			}
			if first && len(rec) >= 2 && strings.EqualFold(strings.TrimSpace(rec[0]), "key") && strings.EqualFold(strings.TrimSpace(rec[1]), "value") {
				first = false
				continue
			}
			first = false
			if len(rec) < 2 {
				return errUnprocessable("VALIDATION_FAILED", "Invalid CSV", "line %d: want key,value", line)
			}
			val := strings.Join(rec[1:], ",")
			var raw json.RawMessage
			if json.Valid([]byte(val)) {
				raw = json.RawMessage(val)
			} else {
				raw, _ = json.Marshal(val)
			}
			rows = append(rows, model.LookupRow{Key: strings.TrimSpace(rec[0]), Value: raw})
		}
	} else {
		var in struct {
			Rows []model.LookupRow `json:"rows"`
		}
		if err := decodeJSON(r, &in); err != nil {
			return err
		}
		rows = in.Rows
	}
	schema, err := s.loadVersion(ctx, app, model.KindSchemas, spec.ValueSchema.Name, spec.ValueSchema.Version)
	if err != nil {
		return errUnprocessable("SCHEMA_NOT_FOUND", "Schema not registered", "valueSchema %s@%d is not registered.", spec.ValueSchema.Name, spec.ValueSchema.Version)
	}
	var bad []FieldError
	seen := map[string]bool{}
	for i, row := range rows {
		if seen[row.Key] {
			bad = append(bad, FieldError{Path: fmt.Sprintf("/rows/%d/key", i), Message: "duplicate key " + row.Key})
			continue
		}
		seen[row.Key] = true
		if spec.KeyType == "number" {
			if _, err := strconv.ParseFloat(row.Key, 64); err != nil {
				bad = append(bad, FieldError{Path: fmt.Sprintf("/rows/%d/key", i), Message: "not a number"})
			}
		}
		if len(row.Value) == 0 {
			row.Value = json.RawMessage("null")
			rows[i] = row
		}
		ok, errs, err := s.eng.ValidateDocument(schema.Spec, row.Value)
		if err != nil {
			return errUnprocessable("VALIDATION_FAILED", "Schema unusable", "%v", err)
		}
		if !ok {
			for _, e := range errs {
				bad = append(bad, FieldError{Path: fmt.Sprintf("/rows/%d/value%s", i, strings.TrimSuffix(e.Path, "/")), Message: e.Message})
			}
		}
		if len(bad) >= 50 {
			break
		}
	}
	if len(bad) > 0 {
		return errUnprocessable("VALIDATION_FAILED", "Invalid rows", "Rows do not match valueSchema %s@%d.", spec.ValueSchema.Name, spec.ValueSchema.Version).with(bad...)
	}
	for i := range rows {
		var buf bytes.Buffer
		if json.Compact(&buf, rows[i].Value) == nil {
			rows[i].Value = buf.Bytes()
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	if rows == nil {
		rows = []model.LookupRow{}
	}
	ref, err := s.storeData(ctx, rows, len(rows))
	if err != nil {
		return err
	}
	rec.Draft.Data = ref
	rec.Draft.Revision++
	rec.Draft.UpdatedAt, rec.Draft.UpdatedBy = s.now(), p.Actor()
	rec.UpdatedAt, rec.UpdatedBy = rec.Draft.UpdatedAt, p.Actor()
	if err := s.st.PutResource(ctx, rec, ver); err != nil {
		if errors.Is(err, store.ErrVersion) {
			return errConflict("REVISION_CONFLICT", "Draft changed", "The draft changed during the upload; retry.")
		}
		return err
	}
	s.audit(ctx, p, "lookup.rows_replaced", app, "", "lookups/"+name, map[string]any{"total": len(rows), "sha256": ref.SHA256})
	writeJSON(w, http.StatusOK, map[string]any{"total": len(rows), "revision": rec.Draft.Revision})
	return nil
}

func (s *Server) handleListLookupRows(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	rec, _, err := s.loadResource(ctx, p, r.PathValue("app"), model.KindLookups, r.PathValue("name"))
	if err != nil {
		return err
	}
	pg, err := pagination(r)
	if err != nil {
		return err
	}
	rows, err := s.loadRows(ctx, rec.Draft.Data)
	if err != nil {
		return err
	}
	if key := r.URL.Query().Get("key"); key != "" {
		var hit []model.LookupRow
		i := sort.Search(len(rows), func(i int) bool { return rows[i].Key >= key })
		if i < len(rows) && rows[i].Key == key {
			hit = rows[i : i+1]
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(hit), "total": len(hit)})
		return nil
	}
	items, next, err := pageSlice(rows, pg)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, totalPage[model.LookupRow]{Items: nonNil(items), Total: len(rows), NextCursor: encodeCursor(next)})
	return nil
}
