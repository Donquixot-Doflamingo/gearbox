// SPDX-License-Identifier: MIT

package hashing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

type fixtures struct {
	AllocationCases []struct {
		Name, SchemaVersion, TenantID, Scope, ResourceID, AssignmentVersion, Salt, RandomizationValue string
		InputHex                                                                                      string `json:"inputHex"`
		Hash                                                                                          string `json:"hash"`
		Bucket                                                                                        int    `json:"bucket"`
	} `json:"allocationCases"`
	RangeCases []struct {
		Name   string `json:"name"`
		Ranges []struct {
			Start, End int
			Value      string
		} `json:"ranges"`
		Lookups []struct {
			Bucket int     `json:"bucket"`
			Value  *string `json:"value"`
		} `json:"lookups"`
	} `json:"rangeCases"`
	InvalidRanges []struct {
		Name   string `json:"name"`
		Ranges []struct {
			Start, End int
			Value      string
		} `json:"ranges"`
		Error string `json:"error"`
	} `json:"invalidRanges"`
	OverrideDigestCases []struct {
		Name, SchemaVersion, TenantID, ResourceID, AssignmentVersion, Salt, RandomizationValue string
		Unit                                                                                   struct{ Kind, Property string }
		InputHex                                                                               string `json:"inputHex"`
		Digest                                                                                 string `json:"digest"`
	} `json:"overrideDigestCases"`
}

func load(t *testing.T) fixtures {
	t.Helper()
	b, err := os.ReadFile("testdata/allocation-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var f fixtures
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestAllocationParity(t *testing.T) {
	f := load(t)
	if len(f.AllocationCases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.AllocationCases {
		t.Run(c.Name, func(t *testing.T) {
			r := Request{SchemaVersion: c.SchemaVersion, Tenant: c.TenantID, Scope: c.Scope, ResourceID: c.ResourceID, AssignmentVersion: c.AssignmentVersion, Salt: c.Salt, Value: c.RandomizationValue}
			if got := hex.EncodeToString(Frame(r)); got != c.InputHex {
				t.Fatalf("frame = %s, want %s", got, c.InputHex)
			}
			h, b := Hash(r)
			if got := fmt.Sprintf("%08x", h); got != c.Hash {
				t.Fatalf("hash = %s, want %s", got, c.Hash)
			}
			if b != c.Bucket {
				t.Fatalf("bucket = %d, want %d", b, c.Bucket)
			}
		})
	}
}

func TestOverrideDigestFrameParity(t *testing.T) {
	for _, c := range load(t).OverrideDigestCases {
		t.Run(c.Name, func(t *testing.T) {
			frame := FrameFields(c.SchemaVersion, c.TenantID, c.ResourceID, c.AssignmentVersion, c.Salt, c.Unit.Kind, c.Unit.Property, c.RandomizationValue)
			if got := hex.EncodeToString(frame); got != c.InputHex {
				t.Fatalf("frame = %s, want %s", got, c.InputHex)
			}
			sum := sha256.Sum256(frame)
			if got := hex.EncodeToString(sum[:]); got != c.Digest {
				t.Fatalf("digest = %s, want %s", got, c.Digest)
			}
		})
	}
}

func TestRanges(t *testing.T) {
	f := load(t)
	for _, c := range f.RangeCases {
		t.Run(c.Name, func(t *testing.T) {
			var in []Range
			for _, r := range c.Ranges {
				in = append(in, Range{Start: r.Start, End: r.End, Value: r.Value})
			}
			rs, err := NewRanges(in)
			if err != nil {
				t.Fatal(err)
			}
			for _, l := range c.Lookups {
				v, ok := rs.Lookup(l.Bucket)
				if l.Value == nil {
					if ok {
						t.Fatalf("bucket %d: got %q, want unallocated", l.Bucket, v)
					}
					continue
				}
				if !ok || v != *l.Value {
					t.Fatalf("bucket %d: got %q/%v, want %q", l.Bucket, v, ok, *l.Value)
				}
			}
		})
	}
	for _, c := range f.InvalidRanges {
		t.Run(c.Name, func(t *testing.T) {
			var in []Range
			for _, r := range c.Ranges {
				in = append(in, Range{Start: r.Start, End: r.End, Value: r.Value})
			}
			_, err := NewRanges(in)
			if err == nil || err.Error() != c.Error {
				t.Fatalf("err = %v, want %s", err, c.Error)
			}
		})
	}
}

func TestCumulative(t *testing.T) {
	rs := Cumulative([]string{"a", "b", "c"}, []float64{1, 1, 1}, 3)
	if len(rs) != 3 || rs[0].End != 3333 || rs[1].End != 6667 || rs[2].End != Buckets {
		t.Fatalf("ranges = %+v", rs)
	}
	part := Cumulative([]string{"x", "y"}, []float64{10, 20}, 100)
	if part[0].End != 1000 || part[1].Start != 1000 || part[1].End != 3000 {
		t.Fatalf("partial = %+v", part)
	}
	if _, ok := part.Lookup(3000); ok {
		t.Fatal("bucket 3000 should be unallocated")
	}
	zero := Cumulative([]string{"x", "y"}, []float64{0, 5}, 5)
	if len(zero) != 1 || zero[0].Value != "y" {
		t.Fatalf("zero weight = %+v", zero)
	}
}

func TestRolloutBucketStableAndSpread(t *testing.T) {
	first, second := RolloutBucket("app", "s", "u1"), RolloutBucket("app", "s", "u1")
	if first != second {
		t.Fatalf("not stable: %d then %d", first, second)
	}
	under := 0
	for i := 0; i < 20000; i++ {
		if RolloutBucket("app", "salt", fmt.Sprintf("unit-%d", i)) < 2500 {
			under++
		}
	}
	if under < 4600 || under > 5400 {
		t.Fatalf("25%% split got %d of 20000", under)
	}
}

func BenchmarkRolloutBucket(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = RolloutBucket("lead_service", "eligibility.r4", "L-1029384")
	}
}
