package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/ORG/gearbox/controlplane/internal/model"
	"github.com/ORG/gearbox/controlplane/internal/store"
)

const (
	webhookRetryWindow = 24 * time.Hour
	webhookMaxBackoff  = time.Hour
)

// WebhookSignature is the X-Gearbox-Signature value for a body.
func WebhookSignature(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// notify enqueues an event for every subscribed webhook (outbox pattern:
// the event is durable before delivery is attempted).
func (s *Server) notify(ctx context.Context, event string, payload map[string]any) {
	ctx = context.WithoutCancel(ctx)
	hooks, err := s.st.ListWebhooks(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "webhooks: list failed", "err", err)
		return
	}
	now := s.now()
	body := map[string]any{"event": event, "at": now}
	for k, v := range payload {
		body[k] = v
	}
	data, _ := json.Marshal(body)
	for _, h := range hooks {
		if !slices.Contains(h.Events, event) {
			continue
		}
		it := model.OutboxItem{ID: store.NewRecordID(now), WebhookID: h.ID, Event: event, Payload: data, NextAt: now, CreatedAt: now}
		if err := s.st.EnqueueOutbox(ctx, it); err != nil {
			slog.ErrorContext(ctx, "webhooks: enqueue failed", "webhook", h.ID, "err", err)
		}
	}
}

func (s *Server) webhookWorker(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.DeliverWebhooks(ctx)
		}
	}
}

// DeliverWebhooks attempts every due delivery once (the worker calls it
// every second; tests call it directly).
func (s *Server) DeliverWebhooks(ctx context.Context) int {
	items, err := s.st.ClaimOutbox(ctx, s.now(), 50, time.Minute)
	if err != nil {
		slog.ErrorContext(ctx, "webhooks: claim failed", "err", err)
		return 0
	}
	delivered := 0
	for _, it := range items {
		if s.deliver(ctx, it) {
			delivered++
		}
	}
	return delivered
}

func (s *Server) deliver(ctx context.Context, it model.OutboxItem) bool {
	h, err := s.st.GetWebhook(ctx, it.WebhookID)
	if errors.Is(err, store.ErrNotFound) {
		_ = s.st.DeleteOutbox(ctx, it.ID) // webhook removed
		return false
	}
	if err != nil {
		return false
	}
	status, derr := s.post(ctx, h, it)
	now := s.now()
	_, _ = s.st.UpdateWebhook(ctx, h.ID, func(w *model.WebhookRecord) error {
		w.LastDelivery = &model.WebhookDelivery{At: now, Status: status}
		return nil
	})
	if derr == nil && status >= 200 && status < 300 {
		_ = s.st.DeleteOutbox(ctx, it.ID)
		return true
	}
	it.Attempts++
	if derr != nil {
		it.LastError = derr.Error()
	} else {
		it.LastError = fmt.Sprintf("HTTP %d", status)
	}
	backoff := 5 * time.Second << min(it.Attempts-1, 12)
	if backoff > webhookMaxBackoff {
		backoff = webhookMaxBackoff
	}
	it.NextAt, it.LeaseUntil = now.Add(backoff), time.Time{}
	if it.NextAt.Sub(it.CreatedAt) > webhookRetryWindow {
		slog.WarnContext(ctx, "webhooks: giving up after 24h", "webhook", h.ID, "event", it.Event, "attempts", it.Attempts, "err", it.LastError)
		_ = s.st.DeleteOutbox(ctx, it.ID)
		return false
	}
	_ = s.st.PutOutbox(ctx, it)
	return false
}

func (s *Server) post(ctx context.Context, h model.WebhookRecord, it model.OutboxItem) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(it.Payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "gearbox-webhooks/"+Version)
	req.Header.Set("X-Gearbox-Event", it.Event)
	req.Header.Set("X-Gearbox-Delivery", it.ID)
	req.Header.Set("X-Gearbox-Signature", WebhookSignature(h.Secret, it.Payload))
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}
