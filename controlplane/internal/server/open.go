package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/ORG/gearbox/controlplane/internal/objstore"
	"github.com/ORG/gearbox/controlplane/internal/store"
	"github.com/ORG/gearbox/controlplane/internal/store/mongostore"
)

// OpenBackend opens the store named by GEARBOX_STORE:
//
//	memory             in-memory, lost on restart
//	file:///path       in-memory with JSON snapshots in /path
//	mongo              MongoDB at GEARBOX_MONGO_URI / GEARBOX_MONGO_DB
//	mongodb://…        MongoDB at that URI (database from the path or GEARBOX_MONGO_DB)
func OpenBackend(ctx context.Context, cfg Config) (store.Backend, error) {
	v := cfg.Store
	db := cfg.MongoDB
	if db == "" {
		db = "gearbox"
	}
	switch {
	case v == "" || v == "memory":
		return store.NewMemory("")
	case strings.HasPrefix(v, "file://"):
		return store.NewMemory(strings.TrimPrefix(v, "file://"))
	case v == "mongo":
		if cfg.MongoURI == "" {
			return nil, fmt.Errorf("GEARBOX_STORE=mongo needs GEARBOX_MONGO_URI")
		}
		return mongostore.Open(ctx, cfg.MongoURI, mongostore.DBName(cfg.MongoURI, db))
	case strings.HasPrefix(v, "mongodb://"), strings.HasPrefix(v, "mongodb+srv://"):
		return mongostore.Open(ctx, v, mongostore.DBName(v, db))
	}
	return nil, fmt.Errorf("unsupported GEARBOX_STORE %q (memory, file:///path, mongo, mongodb://…)", v)
}

// openObjectStore applies the GEARBOX_S3_* settings to an s3:// URL.
func openObjectStore(cfg Config, sig objstore.Signer) (objstore.Store, error) {
	if !strings.HasPrefix(cfg.ObjectStore, "s3://") {
		return objstore.Open(cfg.ObjectStore, sig)
	}
	c, err := objstore.ParseS3URL(cfg.ObjectStore)
	if err != nil {
		return nil, err
	}
	if cfg.S3Endpoint != "" {
		c.Endpoint, c.Secure = splitEP(cfg.S3Endpoint)
	}
	if cfg.S3Region != "" {
		c.Region = cfg.S3Region
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	if cfg.S3PathStyle {
		c.PathStyle = true
	}
	if cfg.S3Presign != "" {
		c.PresignEndpoint = cfg.S3Presign
	}
	return objstore.NewS3(c)
}

func splitEP(ep string) (string, bool) {
	switch {
	case strings.HasPrefix(ep, "http://"):
		return strings.TrimRight(strings.TrimPrefix(ep, "http://"), "/"), false
	case strings.HasPrefix(ep, "https://"):
		return strings.TrimRight(strings.TrimPrefix(ep, "https://"), "/"), true
	}
	return ep, true
}
