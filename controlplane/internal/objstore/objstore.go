// Package objstore stores bundle bytes and large definition data.
//
// Layout (proto/gearbox/v1/bundle.proto):
//
//	blobs/sha256/<hex>                    resource blobs (and a copy of each envelope)
//	envelopes/<app>/<env>/<generation>.pb SignedManifest
//	heads/<app>/<env>.json                head pointer
//
// plus data/sha256/<hex> for segment members and lookup rows referenced by
// drafts and versions (control-plane only).
package objstore

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ErrNotFound is returned by Get for a missing key.
var ErrNotFound = errors.New("objstore: not found")

// Store is implemented by the filesystem and S3 stores.
type Store interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	Exists(ctx context.Context, key string) (bool, error)
	// URL returns a short-lived download URL for key.
	URL(ctx context.Context, key string, ttl time.Duration) (string, error)
	// Kind is "file" or "s3".
	Kind() string
}

// BlobKey returns blobs/sha256/<hex>.
func BlobKey(sha string) string { return "blobs/sha256/" + sha }

// DataKey returns data/sha256/<hex>.
func DataKey(sha string) string { return "data/sha256/" + sha }

// EnvelopeKey returns envelopes/<app>/<env>/<gen>.pb.
func EnvelopeKey(app, env string, gen int64) string {
	return fmt.Sprintf("envelopes/%s/%s/%d.pb", app, env, gen)
}

// HeadKey returns heads/<app>/<env>.json.
func HeadKey(app, env string) string { return "heads/" + app + "/" + env + ".json" }

// SHA256Hex hashes data.
func SHA256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// ValidKey rejects keys that could escape the store root.
func ValidKey(key string) bool {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return false
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// Signer signs filesystem blob URLs served by /sidecar/v1/blobs/{sha256}.
type Signer struct {
	Key     []byte
	BaseURL string // e.g. http://localhost:8080/api/v1
	Now     func() time.Time
}

func (s Signer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s Signer) mac(sha string, exp int64) string {
	m := hmac.New(sha256.New, s.Key)
	fmt.Fprintf(m, "blob:%s:%d", sha, exp)
	return hex.EncodeToString(m.Sum(nil))
}

// BlobURL returns the signed URL of a blob.
func (s Signer) BlobURL(sha string, ttl time.Duration) string {
	exp := s.now().Add(ttl).Unix()
	q := url.Values{"exp": {fmt.Sprint(exp)}, "sig": {s.mac(sha, exp)}}
	return strings.TrimRight(s.BaseURL, "/") + "/sidecar/v1/blobs/" + sha + "?" + q.Encode()
}

// Verify checks a signed blob URL's parameters.
func (s Signer) Verify(sha, sig string, exp int64) bool {
	if exp < s.now().Unix() {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(s.mac(sha, exp)))
}

// Open builds a Store from a URL: file:///abs/path, file://./rel/path or
// s3://bucket/prefix?region=…&endpoint=…
func Open(raw string, signer Signer) (Store, error) {
	if raw == "" {
		raw = "file://./.data/objects"
	}
	switch {
	case strings.HasPrefix(raw, "file://"):
		return NewFS(strings.TrimPrefix(raw, "file://"), signer)
	case strings.HasPrefix(raw, "s3://"):
		cfg, err := ParseS3URL(raw)
		if err != nil {
			return nil, err
		}
		return NewS3(cfg)
	}
	return nil, fmt.Errorf("objstore: unsupported GEARBOX_OBJECT_STORE %q (want file://… or s3://…)", raw)
}
