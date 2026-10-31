// Package bundle builds, signs, verifies and decodes Gearbox bundles
// (proto/gearbox/v1/bundle.proto). Pure functions, no I/O: the control plane
// uploads what Build returns; the sidecar downloads bytes and calls Open.
//
// CONTRACT between the control plane (writer) and the sidecar (reader).
// Signatures are fixed; the bodies delegate to bundle.go.
package bundle

import (
	"crypto/ed25519"
	"errors"

	"github.com/ORG/gearbox/engine"
)

// ErrNotImplemented is kept for compatibility; nothing returns it.
var ErrNotImplemented = errors.New("bundle: not implemented")

// Signer signs manifests. KeyID is written into the envelope.
type Signer struct {
	KeyID      string
	PrivateKey ed25519.PrivateKey
}

// TrustedKeys maps key IDs to public keys (GEARBOX_TRUSTED_KEYS).
type TrustedKeys map[string]ed25519.PublicKey

// ParseTrustedKeys parses "id:base64[,id:base64...]" or one per line.
func ParseTrustedKeys(s string) (TrustedKeys, error) { return parseTrustedKeys(s) }

// Meta is the release information written into the manifest.
type Meta struct {
	ReleaseType string // change_request | data | rollout | rollback | kill_switch
	ReleaseID   string
	PublishedBy string
}

// Blob is one content-addressed object to upload to blobs/sha256/<SHA256>.
type Blob struct {
	SHA256 string // hex of Data
	Data   []byte // zstd-compressed ResourceBlob
}

// Built is everything to upload for one generation.
type Built struct {
	Envelope       []byte // SignedManifest bytes → envelopes/<app>/<env>/<generation>.pb
	EnvelopeSHA256 string
	Blobs          []Blob // only blobs whose hash is new need uploading
	MinEngine      string
	TotalBytes     int64
}

// Build encodes every resource of the set, compresses it, computes hashes,
// writes the manifest (including rollouts and killed gates) and signs it.
func Build(set engine.Set, meta Meta, signer Signer) (Built, error) {
	return build(set, meta, signer)
}

// Opened is a verified manifest with its decoded resources.
type Opened struct {
	App, Environment string
	Generation       int64
	MinEngine        string
	Set              engine.Set
}

// VerifyEnvelope checks the signature against trusted keys and returns the
// manifest's generation and the resource hashes it needs, without decoding
// resources (so the sidecar can fetch only missing blobs).
func VerifyEnvelope(envelope []byte, keys TrustedKeys) (generation int64, needed []string, err error) {
	return verifyEnvelope(envelope, keys)
}

// Open verifies the envelope, checks every blob hash and size limits, and
// returns the decoded Set. blobs maps sha256 → bytes. allowUnsigned is only
// for local files.
func Open(envelope []byte, blobs map[string][]byte, keys TrustedKeys, allowUnsigned bool, maxDecompressed int64) (Opened, error) {
	return open(envelope, blobs, keys, allowUnsigned, maxDecompressed)
}

// WriteFile / ReadFile pack an envelope and its blobs into one offline bundle
// file (used by `gearboxctl bundle export` and `gearbox-sidecar --bundle-file`).
func WriteFile(b Built) ([]byte, error) { return writeFile(b) }

func ReadFile(data []byte) (envelope []byte, blobs map[string][]byte, err error) {
	return readFile(data)
}
