// SPDX-License-Identifier: MIT

package bundle

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ORG/gearbox/engine"
	gearboxv1 "github.com/ORG/gearbox/gen/go/gearbox/v1"
)

func testSet(t testing.TB) engine.Set {
	t.Helper()
	b, err := os.ReadFile("../testdata/lead_routing/set.json")
	if err != nil {
		t.Fatal(err)
	}
	var s engine.Set
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func newSigner(t testing.TB, id string) (Signer, TrustedKeys) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return Signer{KeyID: id, PrivateKey: priv}, TrustedKeys{id: pub}
}

func blobMap(b Built) map[string][]byte {
	m := map[string][]byte{}
	for _, bl := range b.Blobs {
		m[bl.SHA256] = bl.Data
	}
	return m
}

func TestBuildOpenRoundTrip(t *testing.T) {
	set := testSet(t)
	set.KilledGates = []string{"g2", "g1"}
	signer, keys := newSigner(t, "k1")
	built, err := Build(set, Meta{ReleaseType: "change_request", ReleaseID: "rel_1", PublishedBy: "asha@example.com"}, signer)
	if err != nil {
		t.Fatal(err)
	}
	if built.MinEngine != engine.Version || built.EnvelopeSHA256 == "" || len(built.Blobs) != len(set.Resources) || built.TotalBytes <= 0 {
		t.Fatalf("built = %+v", built)
	}
	gen, needed, err := VerifyEnvelope(built.Envelope, keys)
	if err != nil || gen != 42 || len(needed) != len(set.Resources) {
		t.Fatalf("verify = %d %v %v", gen, needed, err)
	}
	opened, err := Open(built.Envelope, blobMap(built), keys, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if opened.App != "lead_service" || opened.Environment != "prod" || opened.Generation != 42 || len(opened.Set.Resources) != len(set.Resources) {
		t.Fatalf("opened = %+v", opened)
	}
	if len(opened.Set.KilledGates) != 2 || opened.Set.KilledGates[0] != "g1" {
		t.Fatalf("killed = %v", opened.Set.KilledGates)
	}
	// Members survive.
	for _, r := range opened.Set.Resources {
		if r.Name == "blocked_cities" && len(r.SegmentMembers) != 3 {
			t.Fatalf("members = %v", r.SegmentMembers)
		}
	}
	snap, err := engine.Compile(opened.Set)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := snap.RunTests(t.Context(), "", 0); err != nil || len(res) != 6 {
		t.Fatalf("tests = %v %v", res, err)
	}
	info, err := Inspect(built.Envelope)
	if err != nil || info.ReleaseID != "rel_1" || info.PublishedBy != "asha@example.com" || info.CreatedAt.IsZero() {
		t.Fatalf("inspect = %+v %v", info, err)
	}
	if SignatureKeyID(built.Envelope) != "k1" {
		t.Fatal("key id")
	}
}

func TestDeterministicBlobs(t *testing.T) {
	set := testSet(t)
	signer, _ := newSigner(t, "k1")
	a, _ := Build(set, Meta{}, signer)
	set.Generation = 43
	b, _ := Build(set, Meta{}, signer)
	for i := range a.Blobs {
		if a.Blobs[i].SHA256 != b.Blobs[i].SHA256 {
			t.Fatal("blob hashes must not depend on generation")
		}
	}
	// Lookup rows in any map order encode identically.
	r := engine.Resource{Kind: engine.KindLookup, Name: "l1", Version: 1, Spec: json.RawMessage(`{}`), LookupRows: map[string]json.RawMessage{"a": json.RawMessage(`1`), "b": json.RawMessage(`2`), "c": json.RawMessage(`3`)}}
	first, _, _ := EncodeResource(r)
	for i := 0; i < 20; i++ {
		again, _, _ := EncodeResource(r)
		if again.SHA256 != first.SHA256 {
			t.Fatal("lookup encoding is not deterministic")
		}
	}
}

func TestRejections(t *testing.T) {
	set := testSet(t)
	signer, keys := newSigner(t, "k1")
	built, err := Build(set, Meta{ReleaseType: "data"}, signer)
	if err != nil {
		t.Fatal(err)
	}
	blobs := blobMap(built)

	tampered := append([]byte(nil), built.Envelope...)
	sm := &gearboxv1.SignedManifest{}
	_ = proto.Unmarshal(tampered, sm)
	m := &gearboxv1.Manifest{}
	_ = proto.Unmarshal(sm.Manifest, m)
	m.Generation = 99
	sm.Manifest, _ = proto.Marshal(m)
	tampered, _ = proto.Marshal(sm)
	if _, err := Open(tampered, blobs, keys, false, 0); !errors.Is(err, ErrSignature) {
		t.Fatalf("tampered = %v", err)
	}
	_, other := newSigner(t, "k2")
	if _, _, err := VerifyEnvelope(built.Envelope, other); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unknown key = %v", err)
	}
	wrong := TrustedKeys{"k1": other["k2"]}
	if _, err := Open(built.Envelope, blobs, wrong, false, 0); !errors.Is(err, ErrSignature) {
		t.Fatalf("wrong key = %v", err)
	}
	unsigned, _ := Build(set, Meta{}, Signer{})
	if _, err := Open(unsigned.Envelope, blobMap(unsigned), keys, false, 0); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("unsigned = %v", err)
	}
	if _, err := Open(unsigned.Envelope, blobMap(unsigned), nil, true, 0); err != nil {
		t.Fatalf("allowUnsigned = %v", err)
	}
	// Hash mismatch.
	bad := map[string][]byte{}
	for k, v := range blobs {
		bad[k] = v
	}
	for k, v := range bad {
		c := append([]byte(nil), v...)
		c[len(c)-1] ^= 0xff
		bad[k] = c
		break
	}
	if _, err := Open(built.Envelope, bad, keys, false, 0); !errors.Is(err, ErrHash) {
		t.Fatalf("hash = %v", err)
	}
	// Missing blob.
	missing := map[string][]byte{}
	if _, err := Open(built.Envelope, missing, keys, false, 0); !errors.Is(err, ErrMissingBlob) {
		t.Fatalf("missing = %v", err)
	}
	// Size limit.
	if _, err := Open(built.Envelope, blobs, keys, false, 100); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too large = %v", err)
	}
	// Newer engine required.
	m2 := &gearboxv1.Manifest{}
	sm2 := &gearboxv1.SignedManifest{}
	_ = proto.Unmarshal(built.Envelope, sm2)
	_ = proto.Unmarshal(sm2.Manifest, m2)
	m2.MinEngineVersion = "9.0.0"
	sm2.Manifest, _ = proto.Marshal(m2)
	sm2.Signature = ed25519.Sign(signer.PrivateKey, sm2.Manifest)
	env2, _ := proto.Marshal(sm2)
	if _, err := Open(env2, blobs, keys, false, 0); !errors.Is(err, ErrEngineTooOld) {
		t.Fatalf("engine version = %v", err)
	}
	if _, err := Open([]byte("garbage"), blobs, keys, false, 0); !errors.Is(err, ErrFormat) {
		t.Fatalf("garbage = %v", err)
	}
	if _, err := Build(set, Meta{ReleaseType: "nope"}, signer); err == nil {
		t.Fatal("bad release type accepted")
	}
	set.Generation = 0
	if _, err := Build(set, Meta{}, signer); err == nil {
		t.Fatal("generation 0 accepted")
	}
}

func TestRolloutLiveFlag(t *testing.T) {
	set := testSet(t)
	var dec engine.Resource
	for _, r := range set.Resources {
		if r.Kind == engine.KindDecision {
			dec = r
		}
	}
	v16 := dec
	v16.Version = 16
	set.Resources = append(set.Resources, v16)
	set.Rollouts = []engine.Rollout{{Decision: "lead_routing", FromVersion: 16, ToVersion: 17, Percent: 5, Salt: "lr-17"}}
	signer, keys := newSigner(t, "k1")
	built, err := Build(set, Meta{ReleaseType: "rollout"}, signer)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := Inspect(built.Envelope)
	for _, e := range info.Resources {
		if e.Kind == "decisions" && (e.Version == 16) == e.Live {
			t.Fatalf("entry %+v has wrong live flag", e)
		}
	}
	// Identical specs share one blob? No: version differs, so two blobs.
	opened, err := Open(built.Envelope, blobMap(built), keys, false, 0)
	if err != nil || len(opened.Set.Rollouts) != 1 || opened.Set.Rollouts[0].Salt != "lr-17" || opened.Set.Rollouts[0].Percent != 5 {
		t.Fatalf("rollouts = %+v %v", opened.Set.Rollouts, err)
	}
	if _, err := engine.Compile(opened.Set); err != nil {
		t.Fatal(err)
	}
}

func TestBundleFile(t *testing.T) {
	signer, keys := newSigner(t, "k1")
	built, _ := Build(testSet(t), Meta{}, signer)
	data, err := WriteFile(built)
	if err != nil {
		t.Fatal(err)
	}
	env, blobs, err := ReadFile(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(env, blobs, keys, false, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadFile(data[:len(data)-3]); !errors.Is(err, ErrFormat) {
		t.Fatalf("truncated = %v", err)
	}
	corrupt := append([]byte(nil), data...)
	corrupt[len(corrupt)-1] ^= 1
	if _, _, err := ReadFile(corrupt); !errors.Is(err, ErrHash) {
		t.Fatalf("corrupt = %v", err)
	}
	if _, _, err := ReadFile([]byte("PK\x03\x04")); !errors.Is(err, ErrFormat) {
		t.Fatalf("magic = %v", err)
	}
}

func TestParseTrustedKeys(t *testing.T) {
	pub1, _, _ := ed25519.GenerateKey(rand.Reader)
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub2)
	raw := base64.StdEncoding.EncodeToString(pub1)
	spki := base64.StdEncoding.EncodeToString(der)
	for _, in := range []string{
		"a:" + raw + ",b:" + spki,
		"# keys\na:" + raw + "\n\nb:" + spki + "\n",
		" a : " + raw + " ,\r\nb:" + spki,
	} {
		k, err := ParseTrustedKeys(in)
		if err != nil || len(k) != 2 || !k["a"].Equal(pub1) || !k["b"].Equal(pub2) {
			t.Fatalf("%q: %v %v", in, k, err)
		}
	}
	for _, bad := range []string{"", "a", "a:!!!", "a:" + base64.StdEncoding.EncodeToString([]byte("short")), "a:" + raw + ",a:" + raw} {
		if _, err := ParseTrustedKeys(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{{"1.0.0", "1.0.0", 0}, {"1.2.0", "1.10.0", -1}, {"v2.0.0", "1.9.9", 1}, {"1.0.0-rc1", "1.0.0", 0}, {"", "1.0.0", -1}}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("%s vs %s = %d", c.a, c.b, got)
		}
	}
}

func FuzzReadFile(f *testing.F) {
	signer, _ := newSigner(f, "k1")
	built, _ := Build(testSet(f), Meta{}, signer)
	data, _ := WriteFile(built)
	f.Add(data)
	f.Add([]byte("GBXBNDL1\x00\x00\x00\x01x\xff\xff\xff\xff"))
	f.Fuzz(func(t *testing.T, b []byte) {
		env, blobs, err := ReadFile(b)
		if err == nil {
			_, _ = Open(env, blobs, nil, true, 1<<20)
		}
	})
}
