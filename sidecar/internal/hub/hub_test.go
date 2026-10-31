// SPDX-License-Identifier: MIT

package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ORG/gearbox/engine"
	"github.com/ORG/gearbox/sidecar/internal/obs"
)

func TestHubCall(t *testing.T) {
	var got completionRequest
	var hdr http.Header
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header
		_ = json.NewDecoder(r.Body).Decode(&got)
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"code":"RATE_LIMITED","message":"slow down"}`))
			return
		}
		_, _ = w.Write([]byte(`{"output":{"route":"fast_track"},"model":"m-2026","usage":{"input_tokens":5,"output_tokens":2}}`))
	}))
	defer srv.Close()
	tok := filepath.Join(t.TempDir(), "tok")
	_ = os.WriteFile(tok, []byte("hub-secret\n"), 0o600)
	c := New(srv.URL, tok, obs.New(func() float64 { return 0 }))
	call := engine.AICall{Decision: "d", DecisionVersion: 3, Node: "n", Model: "reasoning-default", System: "sys", User: "u", ResponseSchema: json.RawMessage(`{"type":"object"}`), MaxOutputTokens: 300, Timeout: time.Second, CallID: "call-1"}
	ans, err := c.Call(WithRequestID(context.Background(), "rq"), call)
	if err != nil || string(ans.Output) != `{"route":"fast_track"}` || ans.Model != "m-2026" || ans.OutputTokens != 2 {
		t.Fatalf("answer = %+v %v", ans, err)
	}
	if hdr.Get("Authorization") != "Bearer hub-secret" || hdr.Get("Idempotency-Key") != "call-1" {
		t.Fatalf("headers = %v", hdr)
	}
	if got.Model != "reasoning-default" || len(got.Messages) != 2 || got.Messages[0].Role != "system" || got.TimeoutMs != 900 || got.Metadata["decision"] != "d@3" || got.Metadata["request_id"] != "rq" || got.Temperature != 0 {
		t.Fatalf("request = %+v", got)
	}
	status = http.StatusTooManyRequests
	var he *HTTPError
	for i := 0; i < 5; i++ {
		if _, err := c.Call(context.Background(), call); !errors.As(err, &he) || he.Code != "RATE_LIMITED" {
			t.Fatalf("err = %v", err)
		}
	}
	if _, err := c.Call(context.Background(), call); !errors.Is(err, ErrBreakerOpen) {
		t.Fatalf("breaker = %v", err)
	}
}
