// Package mongostore implements store.Backend on MongoDB (driver v2).
//
// Documents live in one collection per logical collection ("gbx_<coll>") as
// {_id, d: <JSON bytes>, v: <version>}; compare-and-set is an UpdateOne
// filtered on v. Records live in "gbx_rec_<coll>" as {_id, ts, f: {…}, d, e}
// with a TTL index on e. Leases live in "gbx_locks". Works on a standalone
// server (no transactions needed).
package mongostore

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ORG/gearbox/controlplane/internal/auth"
	"github.com/ORG/gearbox/controlplane/internal/store"
)

// Backend is a MongoDB store.Backend.
type Backend struct {
	client  *mongo.Client
	db      *mongo.Database
	owner   string
	mu      sync.Mutex
	indexed map[string]bool
	now     func() time.Time
}

// DBName extracts the database name from a mongodb:// URI path, or def.
func DBName(uri, def string) string {
	rest := uri
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.Index(rest, "/"); i >= 0 {
		name := rest[i+1:]
		if j := strings.IndexAny(name, "?"); j >= 0 {
			name = name[:j]
		}
		if name != "" {
			return name
		}
	}
	return def
}

// Open connects and pings.
func Open(ctx context.Context, uri, db string) (*Backend, error) {
	c, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("mongostore: connect: %w", err)
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.Ping(pctx, nil); err != nil {
		_ = c.Disconnect(context.Background())
		return nil, fmt.Errorf("mongostore: ping: %w", err)
	}
	return &Backend{client: c, db: c.Database(db), owner: auth.RandomID("owner_"), indexed: map[string]bool{}, now: time.Now}, nil
}

// Drop removes the database (tests).
func (b *Backend) Drop(ctx context.Context) error { return b.db.Drop(ctx) }

func (b *Backend) Close() error { return b.client.Disconnect(context.Background()) }

func (b *Backend) docs(coll string) *mongo.Collection { return b.db.Collection("gbx_" + coll) }

type docRec struct {
	ID string `bson:"_id"`
	D  []byte `bson:"d"`
	V  int64  `bson:"v"`
}

func (b *Backend) Get(ctx context.Context, coll, id string) (store.Doc, error) {
	var r docRec
	err := b.docs(coll).FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&r)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return store.Doc{}, store.ErrNotFound
	}
	if err != nil {
		return store.Doc{}, err
	}
	return store.Doc{ID: r.ID, Data: r.D, Ver: r.V}, nil
}

func (b *Backend) Put(ctx context.Context, coll, id string, data []byte, expect int64) (int64, error) {
	c := b.docs(coll)
	switch {
	case expect == store.CreateOnly:
		_, err := c.InsertOne(ctx, docRec{ID: id, D: data, V: 1})
		if mongo.IsDuplicateKeyError(err) {
			return 0, store.ErrExists
		}
		if err != nil {
			return 0, err
		}
		return 1, nil
	case expect == store.Upsert:
		var r docRec
		err := c.FindOneAndUpdate(ctx, bson.D{{Key: "_id", Value: id}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "d", Value: data}}}, {Key: "$inc", Value: bson.D{{Key: "v", Value: int64(1)}}}},
			options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&r)
		if err != nil {
			return 0, err
		}
		return r.V, nil
	default:
		res, err := c.UpdateOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "v", Value: expect}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "d", Value: data}, {Key: "v", Value: expect + 1}}}})
		if err != nil {
			return 0, err
		}
		if res.MatchedCount == 0 {
			if _, err := b.Get(ctx, coll, id); errors.Is(err, store.ErrNotFound) {
				return 0, store.ErrNotFound
			}
			return 0, store.ErrVersion
		}
		return expect + 1, nil
	}
}

func (b *Backend) Delete(ctx context.Context, coll, id string) error {
	res, err := b.docs(coll).DeleteOne(ctx, bson.D{{Key: "_id", Value: id}})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (b *Backend) List(ctx context.Context, coll string, q store.ListQuery) ([]store.Doc, error) {
	idf := bson.D{}
	if q.Prefix != "" {
		idf = append(idf, bson.E{Key: "$regex", Value: bson.Regex{Pattern: "^" + regexp.QuoteMeta(q.Prefix)}})
	}
	if q.After != "" {
		op := "$gt"
		if q.Desc {
			op = "$lt"
		}
		idf = append(idf, bson.E{Key: op, Value: q.After})
	}
	filter := bson.D{}
	if len(idf) > 0 {
		filter = bson.D{{Key: "_id", Value: idf}}
	}
	dir := 1
	if q.Desc {
		dir = -1
	}
	opts := options.Find().SetSort(bson.D{{Key: "_id", Value: dir}})
	if q.Filter == nil && q.Limit > 0 {
		opts.SetLimit(int64(q.Limit))
	}
	cur, err := b.docs(coll).Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []store.Doc{}
	for cur.Next(ctx) {
		var r docRec
		if err := cur.Decode(&r); err != nil {
			return nil, err
		}
		d := store.Doc{ID: r.ID, Data: r.D, Ver: r.V}
		if q.Filter != nil && !q.Filter(d) {
			continue
		}
		out = append(out, d)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, cur.Err()
}

type recDoc struct {
	ID string            `bson:"_id"`
	TS time.Time         `bson:"ts"`
	F  map[string]string `bson:"f,omitempty"`
	D  []byte            `bson:"d"`
	E  *time.Time        `bson:"e,omitempty"`
}

func (b *Backend) recs(ctx context.Context, coll string) *mongo.Collection {
	c := b.db.Collection("gbx_rec_" + coll)
	b.mu.Lock()
	done := b.indexed[coll]
	b.indexed[coll] = true
	b.mu.Unlock()
	if !done {
		_, _ = c.Indexes().CreateMany(ctx, []mongo.IndexModel{
			{Keys: bson.D{{Key: "e", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
			{Keys: bson.D{{Key: "ts", Value: -1}}},
			{Keys: bson.D{{Key: "f.app", Value: 1}, {Key: "_id", Value: -1}}},
		})
	}
	return c
}

func (b *Backend) Append(ctx context.Context, coll string, recs []store.Record) error {
	if len(recs) == 0 {
		return nil
	}
	docs := make([]any, 0, len(recs))
	for _, r := range recs {
		d := recDoc{ID: r.ID, TS: r.TS, F: r.Fields, D: r.Data}
		if !r.ExpireAt.IsZero() {
			e := r.ExpireAt
			d.E = &e
		}
		docs = append(docs, d)
	}
	_, err := b.recs(ctx, coll).InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	if mongo.IsDuplicateKeyError(err) {
		return nil // retried batch
	}
	return err
}

func (b *Backend) Query(ctx context.Context, coll string, q store.RecordQuery) ([]store.Record, error) {
	filter := bson.D{}
	for k, v := range q.Eq {
		filter = append(filter, bson.E{Key: "f." + k, Value: v})
	}
	if q.Before != "" {
		filter = append(filter, bson.E{Key: "_id", Value: bson.D{{Key: "$lt", Value: q.Before}}})
	}
	ts := bson.D{}
	if !q.From.IsZero() {
		ts = append(ts, bson.E{Key: "$gte", Value: q.From})
	}
	if !q.To.IsZero() {
		ts = append(ts, bson.E{Key: "$lt", Value: q.To})
	}
	if len(ts) > 0 {
		filter = append(filter, bson.E{Key: "ts", Value: ts})
	}
	cur, err := b.recs(ctx, coll).Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	now := b.now()
	out := []store.Record{}
	for cur.Next(ctx) {
		var d recDoc
		if err := cur.Decode(&d); err != nil {
			return nil, err
		}
		r := store.Record{ID: d.ID, TS: d.TS.UTC(), Fields: d.F, Data: d.D}
		if d.E != nil {
			r.ExpireAt = *d.E
		}
		// TTL deletion is lazy in MongoDB; re-check expiry and filters.
		if !q.Matches(r, now) {
			continue
		}
		out = append(out, r)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, cur.Err()
}

type lockDoc struct {
	ID    string    `bson:"_id"`
	Owner string    `bson:"owner"`
	Until time.Time `bson:"until"`
}

func (b *Backend) Lock(ctx context.Context, key string, ttl time.Duration) (func(), error) {
	c := b.db.Collection("gbx_locks")
	owner := b.owner + "/" + auth.RandomString(8)
	for {
		until := b.now().Add(ttl)
		_, err := c.InsertOne(ctx, lockDoc{ID: key, Owner: owner, Until: until})
		if err == nil {
			break
		}
		if !mongo.IsDuplicateKeyError(err) {
			return nil, err
		}
		res, err := c.UpdateOne(ctx, bson.D{{Key: "_id", Value: key}, {Key: "until", Value: bson.D{{Key: "$lt", Value: b.now()}}}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "owner", Value: owner}, {Key: "until", Value: until}}}})
		if err != nil {
			return nil, err
		}
		if res.MatchedCount == 1 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = c.DeleteOne(ctx, bson.D{{Key: "_id", Value: key}, {Key: "owner", Value: owner}})
		})
	}, nil
}
