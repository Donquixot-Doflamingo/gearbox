// Package store persists control-plane state.
//
// Two layers:
//
//   - Backend: a tiny document store with compare-and-set versions, prefix
//     listing, append-only records with a retention TTL, and leases. It has an
//     in-memory implementation (optionally snapshotted to a directory) and a
//     MongoDB implementation (package store/mongostore).
//   - Store: typed repositories for apps, environments, resources, versions,
//     change requests, releases, members, tokens, credentials, audit, fleet,
//     logs and webhooks, written once on top of any Backend so the
//     concurrency rules (revision conflicts, strictly increasing generations)
//     are the same everywhere.
package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

var (
	// ErrNotFound: no document with that id.
	ErrNotFound = errors.New("store: not found")
	// ErrExists: create of an id that already exists.
	ErrExists = errors.New("store: already exists")
	// ErrVersion: compare-and-set saw a different version.
	ErrVersion = errors.New("store: version conflict")
)

// Put expectations.
const (
	// CreateOnly fails with ErrExists when the document exists.
	CreateOnly int64 = 0
	// Upsert writes unconditionally.
	Upsert int64 = -1
)

// Doc is one stored document. Ver starts at 1 and increments on every write.
type Doc struct {
	ID   string
	Data []byte
	Ver  int64
}

// ListQuery selects documents of one collection by id prefix, in id order.
type ListQuery struct {
	Prefix string
	// After is an exclusive cursor (an id); with Desc it means "before".
	After string
	Desc  bool
	// Limit 0 = no limit.
	Limit int
	// Filter, when set, is applied before Limit.
	Filter func(Doc) bool
}

// Record is one append-only entry (audit, logs, heartbeats).
type Record struct {
	ID       string            // time-ordered; see NewRecordID
	TS       time.Time         // event time
	Fields   map[string]string // indexed equality fields
	Data     []byte            // JSON
	ExpireAt time.Time         // zero = keep forever
}

// RecordQuery selects records newest first.
type RecordQuery struct {
	Eq     map[string]string
	From   time.Time // inclusive, zero = unbounded
	To     time.Time // exclusive, zero = unbounded
	Before string    // exclusive id cursor
	Limit  int
	Filter func(Record) bool
}

// Backend is implemented by the memory and MongoDB stores.
type Backend interface {
	Get(ctx context.Context, coll, id string) (Doc, error)
	// Put writes data. expect: CreateOnly, Upsert, or the current Ver for
	// compare-and-set. Returns the new Ver.
	Put(ctx context.Context, coll, id string, data []byte, expect int64) (int64, error)
	Delete(ctx context.Context, coll, id string) error
	List(ctx context.Context, coll string, q ListQuery) ([]Doc, error)

	Append(ctx context.Context, coll string, recs []Record) error
	Query(ctx context.Context, coll string, q RecordQuery) ([]Record, error)

	// Lock takes a named lease, waiting until it is free or ctx ends. The
	// lease expires after ttl even if never released (crashed holder).
	Lock(ctx context.Context, key string, ttl time.Duration) (unlock func(), err error)

	Close() error
}

var recSeq atomic.Uint64

// NewRecordID returns an id that sorts by ts, unique across processes.
func NewRecordID(ts time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%016x%06x%s", uint64(ts.UnixNano()), recSeq.Add(1)&0xffffff, hex.EncodeToString(b[:]))
}

// matches applies the common RecordQuery predicate.
func (q RecordQuery) matches(r Record, now time.Time) bool {
	if !r.ExpireAt.IsZero() && !r.ExpireAt.After(now) {
		return false
	}
	if q.Before != "" && r.ID >= q.Before {
		return false
	}
	if !q.From.IsZero() && r.TS.Before(q.From) {
		return false
	}
	if !q.To.IsZero() && !r.TS.Before(q.To) {
		return false
	}
	for k, v := range q.Eq {
		if r.Fields[k] != v {
			return false
		}
	}
	if q.Filter != nil && !q.Filter(r) {
		return false
	}
	return true
}

// Matches is exported for backends in other packages.
func (q RecordQuery) Matches(r Record, now time.Time) bool { return q.matches(r, now) }
