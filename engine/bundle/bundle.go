// SPDX-License-Identifier: MIT

package bundle

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ORG/gearbox/engine"
	gearboxv1 "github.com/ORG/gearbox/gen/go/gearbox/v1"
)

// Errors returned by VerifyEnvelope, Open and ReadFile. Callers branch on
// them with errors.Is (the sidecar maps them to update-log fields).
var (
	ErrFormat       = errors.New("bundle: malformed")
	ErrUnsigned     = errors.New("bundle: envelope is not signed")
	ErrUnknownKey   = errors.New("bundle: signing key is not trusted")
	ErrSignature    = errors.New("bundle: signature does not verify")
	ErrHash         = errors.New("bundle: content hash mismatch")
	ErrMissingBlob  = errors.New("bundle: blob missing")
	ErrTooLarge     = errors.New("bundle: size limit exceeded")
	ErrEngineTooOld = errors.New("bundle: needs a newer engine")
)

// DefaultMaxDecompressed is the decompressed size cap used when Open is
// given a non-positive limit (4 × the default 64 MB compressed bundle limit).
const DefaultMaxDecompressed = 256 << 20

// ReleaseTypes allowed in Meta.ReleaseType.
var ReleaseTypes = map[string]bool{"change_request": true, "data": true, "rollout": true, "rollback": true, "kill_switch": true}

var (
	encoder, _ = zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.SpeedDefault), zstd.WithEncoderCRC(true))
	decoder, _ = zstd.NewReader(nil, zstd.WithDecoderConcurrency(0), zstd.WithDecoderMaxMemory(DefaultMaxDecompressed))
)

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// EncodeResource encodes one resource as a compressed ResourceBlob. The
// encoding is deterministic, so the same resource always has the same hash
// and unchanged resources are uploaded once.
func EncodeResource(r engine.Resource) (Blob, int64, error) {
	rb := &gearboxv1.ResourceBlob{Kind: string(r.Kind), Name: r.Name, Version: r.Version, SpecJson: []byte(r.Spec)}
	if len(r.SegmentMembers) > 0 {
		rb.SegmentMembers = append([]string(nil), r.SegmentMembers...)
		sort.Strings(rb.SegmentMembers)
	}
	if len(r.LookupRows) > 0 {
		keys := make([]string, 0, len(r.LookupRows))
		for k := range r.LookupRows {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			rb.LookupRows = append(rb.LookupRows, &gearboxv1.LookupRow{Key: k, ValueJson: []byte(r.LookupRows[k])})
		}
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(rb)
	if err != nil {
		return Blob{}, 0, fmt.Errorf("encode %s/%s@%d: %w", r.Kind, r.Name, r.Version, err)
	}
	data := encoder.EncodeAll(raw, make([]byte, 0, len(raw)/2+64))
	return Blob{SHA256: sha(data), Data: data}, int64(len(raw)), nil
}

// build implements Build. A zero Signer produces an unsigned envelope, accepted only by sidecars
// running from local files with --allow-unsigned.
func build(set engine.Set, meta Meta, signer Signer) (Built, error) {
	if set.App == "" || set.Environment == "" {
		return Built{}, fmt.Errorf("%w: app and environment are required", ErrFormat)
	}
	if set.Generation <= 0 {
		return Built{}, fmt.Errorf("%w: generation must be positive", ErrFormat)
	}
	if meta.ReleaseType != "" && !ReleaseTypes[meta.ReleaseType] {
		return Built{}, fmt.Errorf("%w: unknown release type %q", ErrFormat, meta.ReleaseType)
	}
	if signer.PrivateKey != nil && len(signer.PrivateKey) != ed25519.PrivateKeySize {
		return Built{}, errors.New("bundle: signer key is not an Ed25519 private key")
	}
	if signer.PrivateKey != nil && signer.KeyID == "" {
		return Built{}, errors.New("bundle: signer needs a key ID")
	}
	fromVersions := map[string]bool{}
	for _, ro := range set.Rollouts {
		fromVersions[ro.Decision+"@"+strconv.FormatInt(ro.FromVersion, 10)] = true
	}
	m := &gearboxv1.Manifest{
		App: set.App, Environment: set.Environment, Generation: set.Generation,
		CreatedAt: timestamppb.New(time.Now().UTC()), PublishedBy: meta.PublishedBy,
		ReleaseType: meta.ReleaseType, ReleaseId: meta.ReleaseID, MinEngineVersion: engine.Version,
	}
	res := append([]engine.Resource(nil), set.Resources...)
	sort.Slice(res, func(i, j int) bool {
		a, b := res[i], res[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Version < b.Version
	})
	var out Built
	seen := map[string]bool{}
	for _, r := range res {
		blob, rawSize, err := EncodeResource(r)
		if err != nil {
			return Built{}, err
		}
		live := !(r.Kind == engine.KindDecision && fromVersions[r.Name+"@"+strconv.FormatInt(r.Version, 10)])
		m.Resources = append(m.Resources, &gearboxv1.ResourceEntry{
			Kind: string(r.Kind), Name: r.Name, Version: r.Version, Sha256: blob.SHA256,
			Size: int64(len(blob.Data)), DecompressedSize: rawSize, Live: live,
		})
		if !seen[blob.SHA256] {
			seen[blob.SHA256] = true
			out.Blobs = append(out.Blobs, blob)
			out.TotalBytes += int64(len(blob.Data))
		}
	}
	for _, ro := range set.Rollouts {
		m.Rollouts = append(m.Rollouts, &gearboxv1.RolloutEntry{Decision: ro.Decision, FromVersion: ro.FromVersion, ToVersion: ro.ToVersion, Percent: int32(ro.Percent), Salt: ro.Salt})
	}
	killed := append([]string(nil), set.KilledGates...)
	sort.Strings(killed)
	for _, g := range killed {
		m.KilledGates = append(m.KilledGates, &gearboxv1.KilledGate{Gate: g})
	}
	manifest, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		return Built{}, err
	}
	sm := &gearboxv1.SignedManifest{Manifest: manifest, KeyId: signer.KeyID}
	if signer.PrivateKey != nil {
		sm.Signature = ed25519.Sign(signer.PrivateKey, manifest)
	}
	env, err := proto.MarshalOptions{Deterministic: true}.Marshal(sm)
	if err != nil {
		return Built{}, err
	}
	out.Envelope, out.EnvelopeSHA256, out.MinEngine = env, sha(env), engine.Version
	out.TotalBytes += int64(len(env))
	return out, nil
}

// decodeEnvelope verifies the signature (unless allowUnsigned) and decodes
// the manifest.
func decodeEnvelope(envelope []byte, keys TrustedKeys, allowUnsigned bool) (*gearboxv1.Manifest, error) {
	sm := &gearboxv1.SignedManifest{}
	if err := proto.Unmarshal(envelope, sm); err != nil {
		return nil, fmt.Errorf("%w: envelope: %v", ErrFormat, err)
	}
	if len(sm.Manifest) == 0 {
		return nil, fmt.Errorf("%w: envelope has no manifest", ErrFormat)
	}
	switch {
	case len(sm.Signature) == 0:
		if !allowUnsigned {
			return nil, ErrUnsigned
		}
	default:
		pub, ok := keys[sm.KeyId]
		if !ok {
			if !allowUnsigned {
				return nil, fmt.Errorf("%w: key %q", ErrUnknownKey, sm.KeyId)
			}
		} else if !ed25519.Verify(pub, sm.Manifest, sm.Signature) {
			return nil, ErrSignature
		}
	}
	m := &gearboxv1.Manifest{}
	if err := proto.Unmarshal(sm.Manifest, m); err != nil {
		return nil, fmt.Errorf("%w: manifest: %v", ErrFormat, err)
	}
	if m.Generation <= 0 || m.App == "" {
		return nil, fmt.Errorf("%w: manifest without app or generation", ErrFormat)
	}
	if CompareVersions(m.MinEngineVersion, engine.Version) > 0 {
		return nil, fmt.Errorf("%w: bundle needs engine %s, this is %s", ErrEngineTooOld, m.MinEngineVersion, engine.Version)
	}
	return m, nil
}

// SignatureKeyID returns the key ID written in an envelope ("" if unsigned).
func SignatureKeyID(envelope []byte) string {
	sm := &gearboxv1.SignedManifest{}
	if proto.Unmarshal(envelope, sm) != nil || len(sm.Signature) == 0 {
		return ""
	}
	return sm.KeyId
}

func verifyEnvelope(envelope []byte, keys TrustedKeys) (generation int64, needed []string, err error) {
	m, err := decodeEnvelope(envelope, keys, false)
	if err != nil {
		return 0, nil, err
	}
	return m.Generation, neededHashes(m), nil
}

// VerifyEnvelopeUnsigned is VerifyEnvelope for local files (--allow-unsigned).
func VerifyEnvelopeUnsigned(envelope []byte, keys TrustedKeys) (int64, []string, error) {
	m, err := decodeEnvelope(envelope, keys, true)
	if err != nil {
		return 0, nil, err
	}
	return m.Generation, neededHashes(m), nil
}

func neededHashes(m *gearboxv1.Manifest) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range m.Resources {
		if !seen[e.Sha256] {
			seen[e.Sha256] = true
			out = append(out, e.Sha256)
		}
	}
	return out
}

func open(envelope []byte, blobs map[string][]byte, keys TrustedKeys, allowUnsigned bool, maxDecompressed int64) (Opened, error) {
	if maxDecompressed <= 0 {
		maxDecompressed = DefaultMaxDecompressed
	}
	m, err := decodeEnvelope(envelope, keys, allowUnsigned)
	if err != nil {
		return Opened{}, err
	}
	var total int64
	for _, e := range m.Resources {
		if e.DecompressedSize < 0 || e.Size < 0 {
			return Opened{}, fmt.Errorf("%w: negative size for %s/%s", ErrFormat, e.Kind, e.Name)
		}
		total += e.DecompressedSize
	}
	if total > maxDecompressed {
		return Opened{}, fmt.Errorf("%w: %d bytes decompressed > limit %d", ErrTooLarge, total, maxDecompressed)
	}
	set := engine.Set{App: m.App, Environment: m.Environment, Generation: m.Generation}
	for _, e := range m.Resources {
		data, ok := blobs[e.Sha256]
		if !ok {
			return Opened{}, fmt.Errorf("%w: %s for %s/%s@%d", ErrMissingBlob, e.Sha256, e.Kind, e.Name, e.Version)
		}
		if sha(data) != e.Sha256 {
			return Opened{}, fmt.Errorf("%w: %s/%s@%d", ErrHash, e.Kind, e.Name, e.Version)
		}
		if int64(len(data)) != e.Size {
			return Opened{}, fmt.Errorf("%w: %s/%s@%d size %d, manifest says %d", ErrFormat, e.Kind, e.Name, e.Version, len(data), e.Size)
		}
		raw, err := decoder.DecodeAll(data, make([]byte, 0, e.DecompressedSize))
		if err != nil {
			return Opened{}, fmt.Errorf("%w: decompress %s/%s: %v", ErrFormat, e.Kind, e.Name, err)
		}
		if int64(len(raw)) != e.DecompressedSize {
			return Opened{}, fmt.Errorf("%w: %s/%s decompressed to %d bytes, manifest says %d", ErrTooLarge, e.Kind, e.Name, len(raw), e.DecompressedSize)
		}
		rb := &gearboxv1.ResourceBlob{}
		if err := proto.Unmarshal(raw, rb); err != nil {
			return Opened{}, fmt.Errorf("%w: resource %s/%s: %v", ErrFormat, e.Kind, e.Name, err)
		}
		if rb.Kind != e.Kind || rb.Name != e.Name || rb.Version != e.Version {
			return Opened{}, fmt.Errorf("%w: blob holds %s/%s@%d, manifest says %s/%s@%d", ErrHash, rb.Kind, rb.Name, rb.Version, e.Kind, e.Name, e.Version)
		}
		r := engine.Resource{Kind: engine.Kind(rb.Kind), Name: rb.Name, Version: rb.Version, Spec: rb.SpecJson, SegmentMembers: rb.SegmentMembers}
		if len(rb.LookupRows) > 0 {
			r.LookupRows = make(map[string]json.RawMessage, len(rb.LookupRows))
			for _, row := range rb.LookupRows {
				r.LookupRows[row.Key] = json.RawMessage(row.ValueJson)
			}
		}
		set.Resources = append(set.Resources, r)
	}
	for _, ro := range m.Rollouts {
		set.Rollouts = append(set.Rollouts, engine.Rollout{Decision: ro.Decision, FromVersion: ro.FromVersion, ToVersion: ro.ToVersion, Percent: int(ro.Percent), Salt: ro.Salt})
	}
	for _, k := range m.KilledGates {
		set.KilledGates = append(set.KilledGates, k.Gate)
	}
	return Opened{App: m.App, Environment: m.Environment, Generation: m.Generation, MinEngine: m.MinEngineVersion, Set: set}, nil
}

// ManifestInfo is release metadata read from an envelope (for update logs).
type ManifestInfo struct {
	App, Environment string
	Generation       int64
	CreatedAt        time.Time
	PublishedBy      string
	ReleaseType      string
	ReleaseID        string
	MinEngine        string
	Resources        []EntryInfo
}

// EntryInfo is one manifest resource entry.
type EntryInfo struct {
	Kind, Name string
	Version    int64
	SHA256     string
	Live       bool
}

// Inspect decodes the manifest without verifying the signature. Use it only
// after Open succeeded, or for display.
func Inspect(envelope []byte) (ManifestInfo, error) {
	m, err := decodeEnvelope(envelope, nil, true)
	if err != nil {
		return ManifestInfo{}, err
	}
	info := ManifestInfo{App: m.App, Environment: m.Environment, Generation: m.Generation, PublishedBy: m.PublishedBy, ReleaseType: m.ReleaseType, ReleaseID: m.ReleaseId, MinEngine: m.MinEngineVersion}
	if m.CreatedAt != nil {
		info.CreatedAt = m.CreatedAt.AsTime()
	}
	for _, e := range m.Resources {
		info.Resources = append(info.Resources, EntryInfo{Kind: e.Kind, Name: e.Name, Version: e.Version, SHA256: e.Sha256, Live: e.Live})
	}
	return info, nil
}

// ---- offline bundle file

var fileMagic = []byte("GBXBNDL1")

// Offline bundle file layout: "GBXBNDL1" | uint32 envelope length | envelope | uint32 blob count |
// per blob: 32-byte SHA-256 | uint32 length | bytes. Integers are big-endian.
func writeFile(b Built) ([]byte, error) {
	if len(b.Envelope) == 0 {
		return nil, fmt.Errorf("%w: no envelope", ErrFormat)
	}
	var buf bytes.Buffer
	buf.Write(fileMagic)
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(b.Envelope)))
	buf.Write(b.Envelope)
	blobs := append([]Blob(nil), b.Blobs...)
	sort.Slice(blobs, func(i, j int) bool { return blobs[i].SHA256 < blobs[j].SHA256 })
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(blobs)))
	for _, bl := range blobs {
		h, err := hex.DecodeString(bl.SHA256)
		if err != nil || len(h) != 32 || sha(bl.Data) != bl.SHA256 {
			return nil, fmt.Errorf("%w: blob %s", ErrHash, bl.SHA256)
		}
		buf.Write(h)
		_ = binary.Write(&buf, binary.BigEndian, uint32(len(bl.Data)))
		buf.Write(bl.Data)
	}
	return buf.Bytes(), nil
}

func readFile(data []byte) (envelope []byte, blobs map[string][]byte, err error) {
	if !bytes.HasPrefix(data, fileMagic) {
		return nil, nil, fmt.Errorf("%w: not a Gearbox bundle file", ErrFormat)
	}
	p := data[len(fileMagic):]
	next := func(n int) ([]byte, error) {
		if n < 0 || n > len(p) {
			return nil, fmt.Errorf("%w: truncated bundle file", ErrFormat)
		}
		out := p[:n]
		p = p[n:]
		return out, nil
	}
	u32 := func() (int, error) {
		b, err := next(4)
		if err != nil {
			return 0, err
		}
		return int(binary.BigEndian.Uint32(b)), nil
	}
	n, err := u32()
	if err != nil {
		return nil, nil, err
	}
	env, err := next(n)
	if err != nil {
		return nil, nil, err
	}
	count, err := u32()
	if err != nil {
		return nil, nil, err
	}
	if count > len(p)/36 {
		return nil, nil, fmt.Errorf("%w: blob count %d too large", ErrFormat, count)
	}
	blobs = make(map[string][]byte, count)
	for i := 0; i < count; i++ {
		h, err := next(32)
		if err != nil {
			return nil, nil, err
		}
		l, err := u32()
		if err != nil {
			return nil, nil, err
		}
		d, err := next(l)
		if err != nil {
			return nil, nil, err
		}
		hs := hex.EncodeToString(h)
		if sha(d) != hs {
			return nil, nil, fmt.Errorf("%w: blob %s", ErrHash, hs)
		}
		blobs[hs] = append([]byte(nil), d...)
	}
	if len(p) != 0 {
		return nil, nil, fmt.Errorf("%w: trailing bytes", ErrFormat)
	}
	return append([]byte(nil), env...), blobs, nil
}

// ---- trusted keys

func parseTrustedKeys(s string) (TrustedKeys, error) {
	keys := TrustedKeys{}
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' })
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" || strings.HasPrefix(f, "#") {
			continue
		}
		id, b64, ok := strings.Cut(f, ":")
		id, b64 = strings.TrimSpace(id), strings.TrimSpace(b64)
		if !ok || id == "" || b64 == "" {
			return nil, fmt.Errorf("bundle: trusted key %q must be key_id:base64", f)
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			raw, err = base64.RawStdEncoding.DecodeString(b64)
		}
		if err != nil {
			return nil, fmt.Errorf("bundle: trusted key %q: invalid base64", id)
		}
		var pub ed25519.PublicKey
		switch len(raw) {
		case ed25519.PublicKeySize:
			pub = ed25519.PublicKey(raw)
		default:
			k, err := x509.ParsePKIXPublicKey(raw)
			if err != nil {
				return nil, fmt.Errorf("bundle: trusted key %q is neither a raw Ed25519 key nor PKIX DER", id)
			}
			p, ok := k.(ed25519.PublicKey)
			if !ok {
				return nil, fmt.Errorf("bundle: trusted key %q is not Ed25519", id)
			}
			pub = p
		}
		if _, dup := keys[id]; dup {
			return nil, fmt.Errorf("bundle: duplicate trusted key id %q", id)
		}
		keys[id] = pub
	}
	if len(keys) == 0 {
		return nil, errors.New("bundle: no trusted keys")
	}
	return keys, nil
}

// CompareVersions compares two semver strings (major.minor.patch, an
// optional leading "v" and pre-release suffix ignored). Unparseable parts
// compare as 0.
func CompareVersions(a, b string) int {
	pa, pb := semver(a), semver(b)
	for i := 0; i < 3; i++ {
		switch {
		case pa[i] < pb[i]:
			return -1
		case pa[i] > pb[i]:
			return 1
		}
	}
	return 0
}

func semver(s string) [3]int {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	var out [3]int
	for i, p := range strings.SplitN(s, ".", 3) {
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
}
