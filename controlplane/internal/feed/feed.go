// Package feed fans generation events out to watching sidecars.
//
// Every server process keeps an in-process Broadcaster. With Redis
// configured, Publish goes through a Redis channel so every replica's
// subscribers hear about every publish; without it, delivery is local.
// Subscribers only ever see increasing generations per app+environment
// (duplicates from the two paths are dropped).
package feed

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Event announces a new generation.
type Event struct {
	App         string `json:"app"`
	Environment string `json:"environment"`
	Generation  int64  `json:"generation"`
}

// Broadcaster delivers events to local subscribers.
type Broadcaster struct {
	mu   sync.Mutex
	subs map[string]map[chan Event]struct{}
	last map[string]int64
}

// NewBroadcaster returns an empty broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: map[string]map[chan Event]struct{}{}, last: map[string]int64{}}
}

func key(app, env string) string { return app + "/" + env }

// Subscribe returns a channel of events for app+env and a cancel func.
// Slow subscribers miss intermediate events but always get the newest one
// (the channel holds one pending event).
func (b *Broadcaster) Subscribe(app, env string) (<-chan Event, func()) {
	ch := make(chan Event, 1)
	b.mu.Lock()
	k := key(app, env)
	if b.subs[k] == nil {
		b.subs[k] = map[chan Event]struct{}{}
	}
	b.subs[k][ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs[k], ch)
		b.mu.Unlock()
	}
}

// Subscribers counts subscribers (tests, metrics).
func (b *Broadcaster) Subscribers(app, env string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs[key(app, env)])
}

// Deliver sends ev to local subscribers unless an equal or newer generation
// was already delivered.
func (b *Broadcaster) Deliver(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := key(ev.App, ev.Environment)
	if ev.Generation <= b.last[k] {
		return
	}
	b.last[k] = ev.Generation
	for ch := range b.subs[k] {
		select {
		case ch <- ev:
		default:
			// Replace the stale pending event with the newest.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- ev:
			default:
			}
		}
	}
}

// Publisher is what the server calls after a release.
type Publisher interface {
	Publish(ctx context.Context, ev Event)
	Close() error
}

// Local publishes in-process only.
type Local struct{ B *Broadcaster }

func (l Local) Publish(_ context.Context, ev Event) { l.B.Deliver(ev) }
func (l Local) Close() error                        { return nil }

// Channel is the Redis pub/sub channel.
const Channel = "gearbox:generation"

// Redis publishes through Redis pub/sub and relays every message it hears
// to the local broadcaster.
type Redis struct {
	b      *Broadcaster
	client *redis.Client
	cancel context.CancelFunc
	done   chan struct{}
	ready  chan struct{}
}

// NewRedis connects (redis://… URL) and starts relaying.
func NewRedis(url string, b *Broadcaster) (*Redis, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opt)
	ctx, cancel := context.WithCancel(context.Background())
	r := &Redis{b: b, client: client, cancel: cancel, done: make(chan struct{}), ready: make(chan struct{})}
	pctx, pcancel := context.WithTimeout(ctx, 5*time.Second)
	defer pcancel()
	if err := client.Ping(pctx).Err(); err != nil {
		cancel()
		_ = client.Close()
		return nil, err
	}
	go r.relay(ctx)
	select {
	case <-r.ready:
	case <-time.After(5 * time.Second):
	}
	return r, nil
}

func (r *Redis) relay(ctx context.Context) {
	defer close(r.done)
	once := sync.Once{}
	for ctx.Err() == nil {
		sub := r.client.Subscribe(ctx, Channel)
		if _, err := sub.Receive(ctx); err != nil {
			_ = sub.Close()
			if ctx.Err() != nil {
				return
			}
			slog.Warn("feed: redis subscribe failed; retrying", "err", err)
			time.Sleep(time.Second)
			continue
		}
		once.Do(func() { close(r.ready) })
		ch := sub.Channel()
		for m := range ch {
			var ev Event
			if err := json.Unmarshal([]byte(m.Payload), &ev); err != nil {
				continue
			}
			r.b.Deliver(ev)
		}
		_ = sub.Close()
	}
}

// Publish sends through Redis and also delivers locally (so local watchers
// are not delayed by, or lost to, a Redis hiccup).
func (r *Redis) Publish(ctx context.Context, ev Event) {
	r.b.Deliver(ev)
	data, _ := json.Marshal(ev)
	if err := r.client.Publish(ctx, Channel, data).Err(); err != nil {
		slog.Warn("feed: redis publish failed", "err", err)
	}
}

func (r *Redis) Close() error {
	r.cancel()
	err := r.client.Close()
	<-r.done
	return err
}
