package feed

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestBroadcaster(t *testing.T) {
	b := NewBroadcaster()
	ch, cancel := b.Subscribe("a", "dev")
	defer cancel()
	other, cancel2 := b.Subscribe("a", "prod")
	defer cancel2()
	b.Deliver(Event{App: "a", Environment: "dev", Generation: 1})
	b.Deliver(Event{App: "a", Environment: "dev", Generation: 1}) // duplicate dropped
	b.Deliver(Event{App: "a", Environment: "dev", Generation: 2}) // replaces pending 1
	select {
	case ev := <-ch:
		if ev.Generation != 2 {
			t.Fatalf("got %d, want newest 2", ev.Generation)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
	select {
	case ev := <-other:
		t.Fatalf("prod got %+v", ev)
	default:
	}
	if b.Subscribers("a", "dev") != 1 {
		t.Fatal("subscriber count")
	}
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// StartRedis runs a throwaway redis-server, or skips the test.
func StartRedis(t *testing.T) string {
	t.Helper()
	if u := os.Getenv("GEARBOX_TEST_REDIS_URL"); u != "" {
		return u
	}
	bin, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server not installed")
	}
	port := freePort(t)
	dir := t.TempDir()
	cmd := exec.Command(bin, "--port", fmt.Sprint(port), "--bind", "127.0.0.1", "--save", "", "--appendonly", "no", "--dir", dir)
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		t.Skipf("redis-server: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return "redis://" + addr + "/0"
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Skip("redis-server did not start")
	return ""
}

// Two "replicas" share Redis: a publish on one reaches subscribers of both.
func TestRedisFanOut(t *testing.T) {
	url := StartRedis(t)
	b1, b2 := NewBroadcaster(), NewBroadcaster()
	r1, err := NewRedis(url, b1)
	if err != nil {
		t.Fatal(err)
	}
	defer r1.Close()
	r2, err := NewRedis(url, b2)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	ch1, c1 := b1.Subscribe("app", "prod")
	defer c1()
	ch2, c2 := b2.Subscribe("app", "prod")
	defer c2()
	r1.Publish(context.Background(), Event{App: "app", Environment: "prod", Generation: 7})
	for i, ch := range []<-chan Event{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.Generation != 7 {
				t.Fatalf("replica %d got %+v", i+1, ev)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("replica %d got nothing", i+1)
		}
	}
	// The relayed copy on replica 1 is a duplicate and must not arrive twice.
	select {
	case ev := <-ch1:
		t.Fatalf("duplicate %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}
