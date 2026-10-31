package engineapi

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/ORG/gearbox/engine"
	"github.com/ORG/gearbox/engine/bundle"
)

// LiteBundler writes bundles in the bundle.proto wire format by hand
// (protowire), so the control plane can publish before the data-plane
// bundle package lands. Envelopes it writes are meant to be readable by
// bundle.Open; WriteFile uses a simple container of its own ("GBXB1").
type LiteBundler struct {
	// Now overrides the manifest timestamp (tests).
	Now func() time.Time
}

func (LiteBundler) Name() string { return "lite" }

var zenc, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))

type msg []byte

func (m msg) str(n protowire.Number, s string) msg {
	if s == "" {
		return m
	}
	m = protowire.AppendTag(m, n, protowire.BytesType)
	return protowire.AppendString(m, s)
}

func (m msg) bytes(n protowire.Number, b []byte) msg {
	if len(b) == 0 {
		return m
	}
	m = protowire.AppendTag(m, n, protowire.BytesType)
	return protowire.AppendBytes(m, b)
}

func (m msg) varint(n protowire.Number, v uint64) msg {
	if v == 0 {
		return m
	}
	m = protowire.AppendTag(m, n, protowire.VarintType)
	return protowire.AppendVarint(m, v)
}

func (m msg) boolean(n protowire.Number, b bool) msg {
	if !b {
		return m
	}
	return m.varint(n, 1)
}

func (m msg) sub(n protowire.Number, b msg) msg {
	m = protowire.AppendTag(m, n, protowire.BytesType)
	return protowire.AppendBytes(m, b)
}

// EncodeResourceBlob encodes one ResourceBlob (before compression).
func EncodeResourceBlob(r engine.Resource) []byte {
	var b msg
	b = b.str(1, string(r.Kind)).str(2, r.Name).varint(3, uint64(r.Version)).bytes(4, r.Spec)
	for _, m := range r.SegmentMembers {
		b = protowire.AppendTag(b, 5, protowire.BytesType)
		b = protowire.AppendString(b, m)
	}
	keys := make([]string, 0, len(r.LookupRows))
	for k := range r.LookupRows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var row msg
		row = row.str(1, k).bytes(2, r.LookupRows[k])
		b = b.sub(6, row)
	}
	return b
}

func (lb LiteBundler) Build(set engine.Set, meta bundle.Meta, signer bundle.Signer) (bundle.Built, error) {
	if len(signer.PrivateKey) != ed25519.PrivateKeySize {
		return bundle.Built{}, errors.New("bundle: signer has no Ed25519 private key")
	}
	now := time.Now()
	if lb.Now != nil {
		now = lb.Now()
	}
	fromVersions := map[string]int64{}
	for _, ro := range set.Rollouts {
		fromVersions[ro.Decision] = ro.FromVersion
	}
	var out bundle.Built
	out.MinEngine = engine.Version
	seen := map[string]bool{}
	var man msg
	man = man.str(1, set.App).str(2, set.Environment).varint(3, uint64(set.Generation))
	var ts msg
	ts = ts.varint(1, uint64(now.Unix())).varint(2, uint64(now.Nanosecond()))
	man = man.sub(4, ts)
	man = man.str(5, meta.PublishedBy).str(6, meta.ReleaseType).str(7, meta.ReleaseID).str(8, out.MinEngine)
	res := append([]engine.Resource(nil), set.Resources...)
	sort.SliceStable(res, func(i, j int) bool {
		if res[i].Kind != res[j].Kind {
			return res[i].Kind < res[j].Kind
		}
		if res[i].Name != res[j].Name {
			return res[i].Name < res[j].Name
		}
		return res[i].Version < res[j].Version
	})
	for _, r := range res {
		raw := EncodeResourceBlob(r)
		comp := zenc.EncodeAll(raw, nil)
		sum := sha256.Sum256(comp)
		hexSum := hex.EncodeToString(sum[:])
		if !seen[hexSum] {
			seen[hexSum] = true
			out.Blobs = append(out.Blobs, bundle.Blob{SHA256: hexSum, Data: comp})
			out.TotalBytes += int64(len(comp))
		}
		live := !(r.Kind == engine.KindDecision && fromVersions[r.Name] == r.Version && r.Version != 0)
		var e msg
		e = e.str(1, string(r.Kind)).str(2, r.Name).varint(3, uint64(r.Version)).str(4, hexSum).
			varint(5, uint64(len(comp))).varint(6, uint64(len(raw))).boolean(7, live)
		man = man.sub(9, e)
	}
	for _, ro := range set.Rollouts {
		var e msg
		e = e.str(1, ro.Decision).varint(2, uint64(ro.FromVersion)).varint(3, uint64(ro.ToVersion)).varint(4, uint64(ro.Percent)).str(5, ro.Salt)
		man = man.sub(10, e)
	}
	for _, g := range set.KilledGates {
		var e msg
		e = e.str(1, g)
		man = man.sub(11, e)
	}
	sig := ed25519.Sign(signer.PrivateKey, man)
	var env msg
	env = env.bytes(1, man).bytes(2, sig).str(3, signer.KeyID)
	out.Envelope = env
	es := sha256.Sum256(env)
	out.EnvelopeSHA256 = hex.EncodeToString(es[:])
	out.TotalBytes += int64(len(env))
	return out, nil
}

// fields decodes one level of a protobuf message.
func fields(b []byte, fn func(n protowire.Number, t protowire.Type, v []byte, u uint64) error) error {
	for len(b) > 0 {
		n, t, l := protowire.ConsumeTag(b)
		if l < 0 {
			return protowire.ParseError(l)
		}
		b = b[l:]
		switch t {
		case protowire.BytesType:
			v, l := protowire.ConsumeBytes(b)
			if l < 0 {
				return protowire.ParseError(l)
			}
			if err := fn(n, t, v, 0); err != nil {
				return err
			}
			b = b[l:]
		case protowire.VarintType:
			v, l := protowire.ConsumeVarint(b)
			if l < 0 {
				return protowire.ParseError(l)
			}
			if err := fn(n, t, nil, v); err != nil {
				return err
			}
			b = b[l:]
		default:
			l := protowire.ConsumeFieldValue(n, t, b)
			if l < 0 {
				return protowire.ParseError(l)
			}
			b = b[l:]
		}
	}
	return nil
}

func (LiteBundler) VerifyEnvelope(envelope []byte, keys bundle.TrustedKeys) (int64, []string, error) {
	var man, sig []byte
	var keyID string
	if err := fields(envelope, func(n protowire.Number, _ protowire.Type, v []byte, _ uint64) error {
		switch n {
		case 1:
			man = v
		case 2:
			sig = v
		case 3:
			keyID = string(v)
		}
		return nil
	}); err != nil {
		return 0, nil, fmt.Errorf("bundle: decode envelope: %w", err)
	}
	pub, ok := keys[keyID]
	if !ok {
		return 0, nil, fmt.Errorf("bundle: untrusted key id %q", keyID)
	}
	if !ed25519.Verify(pub, man, sig) {
		return 0, nil, errors.New("bundle: bad signature")
	}
	var gen int64
	var needed []string
	seen := map[string]bool{}
	err := fields(man, func(n protowire.Number, _ protowire.Type, v []byte, u uint64) error {
		switch n {
		case 3:
			gen = int64(u)
		case 9:
			return fields(v, func(n protowire.Number, _ protowire.Type, v []byte, _ uint64) error {
				if n == 4 && !seen[string(v)] {
					seen[string(v)] = true
					needed = append(needed, string(v))
				}
				return nil
			})
		}
		return nil
	})
	if err != nil {
		return 0, nil, fmt.Errorf("bundle: decode manifest: %w", err)
	}
	return gen, needed, nil
}

// liteFileMagic starts a lite offline bundle file.
const liteFileMagic = "GBXB1\n"

func (LiteBundler) WriteFile(b bundle.Built) ([]byte, error) {
	out := msg(liteFileMagic)
	out = out.bytes(1, b.Envelope)
	for _, bl := range b.Blobs {
		var e msg
		e = e.str(1, bl.SHA256).bytes(2, bl.Data)
		out = out.sub(2, e)
	}
	return out, nil
}

// ReadLiteFile is the inverse of LiteBundler.WriteFile (tests and tools).
func ReadLiteFile(data []byte) (envelope []byte, blobs map[string][]byte, err error) {
	if len(data) < len(liteFileMagic) || string(data[:len(liteFileMagic)]) != liteFileMagic {
		return nil, nil, errors.New("bundle: not a lite bundle file")
	}
	blobs = map[string][]byte{}
	err = fields(data[len(liteFileMagic):], func(n protowire.Number, _ protowire.Type, v []byte, _ uint64) error {
		switch n {
		case 1:
			envelope = v
		case 2:
			var sha string
			var d []byte
			_ = fields(v, func(n protowire.Number, _ protowire.Type, v []byte, _ uint64) error {
				if n == 1 {
					sha = string(v)
				} else if n == 2 {
					d = v
				}
				return nil
			})
			blobs[sha] = d
		}
		return nil
	})
	return envelope, blobs, err
}
