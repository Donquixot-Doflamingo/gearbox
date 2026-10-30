// SPDX-License-Identifier: MIT

// Package hashing implements the stable allocation hash used for every
// percentage split in Gearbox: rule rollouts, rollout segments, decision
// version rollouts, gate rollouts, experiment variants, layers and holdouts.
//
// Algorithm (pinned by golden fixtures; never change its output):
//
//	frame  = for each field: uint32 big-endian byte length, then the UTF-8 bytes
//	fields = [schemaVersion, tenant, scope, resourceID, assignmentVersion, salt, value]
//	hash   = MurmurHash3 x86 32-bit, seed 0, over frame
//	bucket = uint32(uint64(hash) * 10000 >> 32)       // 0..9999
//
// The function and fixtures are compatible with the experimentation service
// Gearbox replaces, so existing assignments do not move.
package hashing

import (
	"encoding/binary"
	"errors"
	"math/bits"
	"sort"
)

// Buckets is the size of the allocation domain.
const Buckets = 10000

// Scopes used by Gearbox.
const (
	ScopeExperiment = "experiment"
	ScopeLayer      = "layer"
	ScopeHoldout    = "holdout"
	ScopeRollout    = "rollout"
)

// Request is one allocation hash input.
type Request struct {
	SchemaVersion     string
	Tenant            string
	Scope             string
	ResourceID        string
	AssignmentVersion string
	Salt              string
	Value             string
}

// Frame returns the length-prefixed frame of the request fields.
func Frame(r Request) []byte {
	return AppendFrame(nil, r)
}

// AppendFrame appends the frame to dst.
func AppendFrame(dst []byte, r Request) []byte {
	for _, f := range [...]string{r.SchemaVersion, r.Tenant, r.Scope, r.ResourceID, r.AssignmentVersion, r.Salt, r.Value} {
		dst = binary.BigEndian.AppendUint32(dst, uint32(len(f)))
		dst = append(dst, f...)
	}
	return dst
}

// Hash returns the murmur3 hash and the bucket (0..9999) of the request.
func Hash(r Request) (hash uint32, bucket int) {
	var buf [256]byte
	frame := AppendFrame(buf[:0], r)
	h := Murmur3(frame)
	return h, int(uint64(h) * Buckets >> 32)
}

// Bucket returns only the bucket of the request.
func Bucket(r Request) int {
	_, b := Hash(r)
	return b
}

// RolloutBucket is Gearbox's bucket(salt) for a unit: rule rollouts, rollout
// segments, gate rollouts and decision version rollouts all use it, with the
// fixed fields schemaVersion "1", tenant = app name, scope "rollout",
// resourceID = salt, assignmentVersion "1", salt "" and value = unit.
func RolloutBucket(app, salt, unit string) int {
	return Bucket(Request{SchemaVersion: "1", Tenant: app, Scope: ScopeRollout, ResourceID: salt, AssignmentVersion: "1", Value: unit})
}

// Murmur3 is MurmurHash3 x86 32-bit with seed 0.
func Murmur3(input []byte) uint32 {
	const (
		c1 uint32 = 0xcc9e2d51
		c2 uint32 = 0x1b873593
	)
	var h uint32
	n := len(input) &^ 3
	for i := 0; i < n; i += 4 {
		k := binary.LittleEndian.Uint32(input[i : i+4])
		k *= c1
		k = bits.RotateLeft32(k, 15)
		k *= c2
		h ^= k
		h = bits.RotateLeft32(h, 13)
		h = h*5 + 0xe6546b64
	}
	var k uint32
	switch len(input) - n {
	case 3:
		k ^= uint32(input[n+2]) << 16
		fallthrough
	case 2:
		k ^= uint32(input[n+1]) << 8
		fallthrough
	case 1:
		k ^= uint32(input[n])
		k *= c1
		k = bits.RotateLeft32(k, 15)
		k *= c2
		h ^= k
	}
	h ^= uint32(len(input))
	h ^= h >> 16
	h *= 0x85ebca6b
	h ^= h >> 13
	h *= 0xc2b2ae35
	h ^= h >> 16
	return h
}

// Range is a half-open bucket range [Start, End) assigned to Value.
type Range struct {
	Start, End int
	Value      string
}

// Ranges is a validated, sorted list of ranges.
type Ranges []Range

// Lookup returns the value whose range contains bucket.
func (rs Ranges) Lookup(bucket int) (string, bool) {
	lo, hi := 0, len(rs)
	for lo < hi {
		m := (lo + hi) / 2
		if rs[m].End > bucket {
			hi = m
		} else {
			lo = m + 1
		}
	}
	if lo < len(rs) && rs[lo].Start <= bucket {
		return rs[lo].Value, true
	}
	return "", false
}

// Cumulative builds contiguous ranges from shares that sum to at most total.
// shares are scaled so that total maps to all 10000 buckets; the last range of
// a full allocation always ends at 10000 so no bucket is lost to rounding.
func Cumulative(values []string, shares []float64, total float64) Ranges {
	out := make(Ranges, 0, len(values))
	var acc float64
	start := 0
	sum := 0.0
	for _, s := range shares {
		sum += s
	}
	for i, v := range values {
		acc += shares[i]
		end := int(acc/total*Buckets + 0.5)
		if i == len(values)-1 && sum >= total-1e-9 {
			end = Buckets
		}
		if end > Buckets {
			end = Buckets
		}
		if end > start {
			out = append(out, Range{Start: start, End: end, Value: v})
		}
		start = end
	}
	return out
}

// FrameFields frames arbitrary fields with the same length-prefix encoding.
func FrameFields(fields ...string) []byte {
	var out []byte
	for _, f := range fields {
		out = binary.BigEndian.AppendUint32(out, uint32(len(f)))
		out = append(out, f...)
	}
	return out
}

// ErrOverlap and ErrBounds are returned by NewRanges.
var (
	ErrOverlap = errors.New("bucket ranges overlap")
	ErrBounds  = errors.New("bucket range must satisfy 0 <= start < end <= 10000")
)

// NewRanges sorts and validates explicit ranges (gaps are allowed and mean
// "not allocated").
func NewRanges(in []Range) (Ranges, error) {
	rs := append(Ranges(nil), in...)
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Start == rs[j].Start {
			return rs[i].End < rs[j].End
		}
		return rs[i].Start < rs[j].Start
	})
	for i, r := range rs {
		if r.Start < 0 || r.Start >= r.End || r.End > Buckets {
			return nil, ErrBounds
		}
		if i > 0 && rs[i-1].End > r.Start {
			return nil, ErrOverlap
		}
	}
	return rs, nil
}
