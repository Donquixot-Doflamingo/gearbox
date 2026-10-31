package server

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ORG/gearbox/controlplane/internal/engineapi"
	"github.com/ORG/gearbox/controlplane/internal/model"
	"github.com/ORG/gearbox/controlplane/internal/store"
)

func startRedis(t *testing.T) string {
	bin, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server not installed")
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cmd := exec.Command(bin, "--port", fmt.Sprint(port), "--bind", "127.0.0.1", "--save", "", "--appendonly", "no", "--dir", t.TempDir())
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return "redis://" + addr
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Skip("redis did not start")
	return ""
}

// Two replicas share a store, an object store and Redis: a sidecar watching
// replica B hears about a publish on replica A.
func TestRedisFanOutAcrossReplicas(t *testing.T) {
	url := startRedis(t)
	mem, _ := store.NewMemory("")
	st := store.New(mem)
	_, key, _ := ed25519.GenerateKey(nil)
	objDir := t.TempDir()
	var urls []string
	var servers []*Server
	for i := 0; i < 2; i++ {
		ts := httptest.NewUnstartedServer(nil)
		cfg := Config{Auth: "dev", PublicURL: "http://" + ts.Listener.Addr().String(), SigningKeyID: "k", RedisURL: url, ObjectStore: "file://" + objDir, DataDir: t.TempDir(), PingInterval: time.Hour}
		s, err := New(context.Background(), cfg, Deps{Store: st, SigningKey: key, Secret: []byte("shared"), NoBackground: true, Engine: engineapi.Lite{}, Bundler: engineapi.LiteBundler{}})
		if err != nil {
			t.Fatal(err)
		}
		ts.Config.Handler = s.Handler()
		ts.Start()
		t.Cleanup(func() { ts.Close(); _ = s.Close() })
		urls, servers = append(urls, ts.URL), append(servers, s)
	}
	ctx := context.Background()
	a := servers[0]
	if err := st.CreateApp(ctx, model.App{Name: "app_x", Environments: []string{"dev"}}, []model.EnvState{{App: "app_x", Name: "dev", Policy: model.DefaultPolicy("dev"), Live: map[string]model.LiveEntry{}, KilledGates: map[string]string{}}}); err != nil {
		t.Fatal(err)
	}
	cred, err := a.CreateCredential(ctx, &Principal{Kind: "dev"}, "app_x", "dev", "t", "")
	if err != nil {
		t.Fatal(err)
	}
	// A layer is the smallest resource that can be released.
	post := func(base, path, body string) {
		req, _ := http.NewRequest("POST", base+"/api/v1"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode >= 300 {
			t.Fatalf("POST %s: %d", path, res.StatusCode)
		}
	}
	post(urls[0], "/apps/app_x/layers", `{"name":"lay","spec":{"allocations":[]}}`)
	post(urls[0], "/apps/app_x/layers/lay/versions", `{"revision":1}`)

	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, _ := http.NewRequestWithContext(wctx, "GET", urls[1]+"/api/v1/sidecar/v1/watch", nil)
	req.Header.Set("Authorization", "Bearer "+cred.Secret)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	got := make(chan string, 4)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				got <- d
			}
		}
	}()
	<-got // initial ping (nothing published yet)
	post(urls[0], "/apps/app_x/change-requests", `{"environment":"dev","title":"t","items":[{"kind":"layers","name":"lay","version":1}]}`)
	post(urls[0], "/apps/app_x/environments/dev/releases", `{"type":"change_request","changeRequestId":"cr_1"}`)
	select {
	case d := <-got:
		if d != `{"generation":1}` {
			t.Fatalf("event %s", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replica B did not hear about the publish on replica A")
	}
}
