package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ORG/gearbox/controlplane/internal/model"
	"github.com/ORG/gearbox/controlplane/internal/objstore"
	"github.com/ORG/gearbox/controlplane/internal/store"
)

func (s *Server) handleHead(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	st, _, err := s.st.GetEnv(ctx, p.CredApp, p.CredEnv)
	if err != nil {
		return err
	}
	if st.Generation == 0 {
		writeError(w, r, errNotFound("Nothing is published to %s/%s yet.", p.CredApp, p.CredEnv))
		return nil
	}
	rel, err := s.st.GetRelease(ctx, p.CredApp, p.CredEnv, st.Generation)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	var haveList []string
	for _, h := range strings.Split(r.URL.Query().Get("have"), ",") {
		if h = strings.TrimSpace(h); h != "" && !have[h] {
			have[h] = true
			haveList = append(haveList, h)
		}
	}
	sort.Strings(haveList)
	hh := sha256.Sum256([]byte(strings.Join(haveList, ",")))
	etag := fmt.Sprintf(`"g%d-%s"`, st.Generation, hex.EncodeToString(hh[:6]))
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		for _, t := range strings.Split(inm, ",") {
			if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "W/")) == etag {
				w.WriteHeader(http.StatusNotModified)
				return nil
			}
		}
	}
	ttl := s.cfg.URLTTL
	envURL, err := s.obj.URL(ctx, objstore.BlobKey(rel.EnvelopeSHA256), ttl)
	if err != nil {
		return err
	}
	head := model.BundleHead{
		App: p.CredApp, Environment: p.CredEnv, Generation: st.Generation,
		Envelope:  model.BundleHeadEnvelope{URL: envURL, SHA256: rel.EnvelopeSHA256},
		Resources: []model.BundleHeadResource{}, ExpiresAt: s.now().Add(ttl).Truncate(time.Second),
	}
	for _, sha := range rel.BlobHashes {
		if have[sha] {
			continue
		}
		u, err := s.obj.URL(ctx, objstore.BlobKey(sha), ttl)
		if err != nil {
			return err
		}
		head.Resources = append(head.Resources, model.BundleHeadResource{SHA256: sha, URL: u})
	}
	writeJSON(w, http.StatusOK, head)
	return nil
}

func (s *Server) handleWatch(w http.ResponseWriter, r *http.Request, p *Principal) error {
	fl, ok := w.(http.Flusher)
	if !ok {
		return errors.New("streaming unsupported")
	}
	// SSE streams outlive the server's write timeout.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	ch, cancel := s.bcast.Subscribe(p.CredApp, p.CredEnv)
	defer cancel()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	send := func(event, data string) error {
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return err
		}
		fl.Flush()
		return nil
	}
	last := int64(0)
	if st, _, err := s.st.GetEnv(r.Context(), p.CredApp, p.CredEnv); err == nil && st.Generation > 0 {
		last = st.Generation
		if err := send("generation", fmt.Sprintf(`{"generation":%d}`, last)); err != nil {
			return nil
		}
	} else {
		_ = send("ping", "{}")
	}
	t := time.NewTicker(s.cfg.PingInterval)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case ev := <-ch:
			if ev.Generation <= last {
				continue
			}
			last = ev.Generation
			if err := send("generation", fmt.Sprintf(`{"generation":%d}`, ev.Generation)); err != nil {
				return nil
			}
		case <-t.C:
			if err := send("ping", "{}"); err != nil {
				return nil
			}
		}
	}
}

func (s *Server) handleBlob(w http.ResponseWriter, r *http.Request, _ *Principal) error {
	sha := r.PathValue("sha256")
	exp, err := strconv.ParseInt(r.URL.Query().Get("exp"), 10, 64)
	if err != nil {
		return errInvalid("exp must be an integer")
	}
	if !s.blobSig.Verify(sha, r.URL.Query().Get("sig"), exp) {
		return errForbidden("The download link is invalid or expired; fetch /sidecar/v1/head again.")
	}
	if tok := bearer(r); tok != "" {
		// Blob links are self-authenticating; a credential, when sent, must still be valid.
		if c, err := s.st.CredentialByHash(r.Context(), s.hasher.Hash(tok)); err != nil || c.State != "active" {
			return errUnauthenticated("The app credential is unknown or revoked.")
		}
	}
	if s.obj.Kind() != "file" {
		return errNotFound("Blobs are served by the object store; use the presigned URL from /sidecar/v1/head.")
	}
	b, err := s.obj.Get(r.Context(), objstore.BlobKey(sha))
	if errors.Is(err, objstore.ErrNotFound) {
		return errNotFound("Blob %s does not exist.", sha)
	}
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
	return nil
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var hb model.Heartbeat
	if err := decodeJSON(r, &hb); err != nil {
		return err
	}
	now := s.now()
	if err := s.st.UpsertSidecar(r.Context(), model.SidecarRecord{App: p.CredApp, Environment: p.CredEnv, Heartbeat: hb, LastSeenAt: now}); err != nil {
		return err
	}
	sample, _ := json.Marshal(hb)
	_ = s.st.AppendRecords(r.Context(), store.RecHeartbeats, []store.Record{{
		ID: store.NewRecordID(now), TS: now, Fields: map[string]string{"app": p.CredApp, "env": p.CredEnv, "sidecar": hb.ID},
		Data: sample, ExpireAt: now.Add(25 * time.Hour),
	}})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) handleIngestLogs(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var in struct {
		Records []json.RawMessage `json:"records"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	now := s.now()
	exp := now.Add(s.cfg.LogRetention)
	by := map[string][]store.Record{}
	for i, raw := range in.Records {
		var head struct {
			Type            string    `json:"type"`
			TS              time.Time `json:"ts"`
			Decision        string    `json:"decision"`
			DecisionVersion string    `json:"decisionVersion"`
			RequestID       string    `json:"requestId"`
			UnitID          string    `json:"unitId"`
			FallbackUsed    bool      `json:"fallbackUsed"`
			Result          string    `json:"result"`
			Experiment      string    `json:"experiment"`
			Error           string    `json:"error"`
			SidecarID       string    `json:"sidecarId"`
			ToGeneration    int64     `json:"toGeneration"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			return errInvalid("records/%d: %v", i, err)
		}
		ts := head.TS
		if ts.IsZero() {
			ts = now
		}
		f := map[string]string{"app": p.CredApp, "env": p.CredEnv}
		var coll string
		switch head.Type {
		case "decision":
			coll = store.RecDecisions
			f["decision"], f["requestId"], f["unitId"] = head.Decision, head.RequestID, head.UnitID
			f["fallback"] = strconv.FormatBool(head.FallbackUsed)
			if _, v, ok := strings.Cut(head.DecisionVersion, "@"); ok {
				f["version"] = v
			}
		case "update":
			coll = store.RecUpdates
			f["result"] = head.Result
			if head.Result == "kept_previous" || head.Error != "" {
				s.notify(r.Context(), "sidecar.sync_failed", map[string]any{"app": p.CredApp, "environment": p.CredEnv, "sidecarId": head.SidecarID, "toGeneration": head.ToGeneration, "error": head.Error})
			}
		case "exposure":
			coll = store.RecExposures
			f["experiment"], f["unitId"] = head.Experiment, head.UnitID
		default:
			return errInvalid("records/%d: unknown type %q", i, head.Type)
		}
		by[coll] = append(by[coll], store.Record{ID: store.NewRecordID(ts), TS: ts, Fields: f, Data: raw, ExpireAt: exp})
	}
	for coll, recs := range by {
		if err := s.st.AppendRecords(r.Context(), coll, recs); err != nil {
			return err
		}
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}
