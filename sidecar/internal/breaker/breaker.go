// SPDX-License-Identifier: MIT

// Package breaker is the per-decision circuit breaker: when a decision's
// evaluation takes more than Factor × its budget on Threshold consecutive
// calls, the breaker opens and the sidecar answers with the decision's
// fallback for OpenFor; then the next call is evaluated again (half open).
package breaker

import (
	"sync"
	"sync/atomic"
	"time"
)

// Defaults.
const (
	Factor    = 10
	Threshold = 5
	OpenFor   = 30 * time.Second
)

// Set holds one breaker per decision.
type Set struct {
	m         sync.Map // decision → *state
	Threshold int32
	OpenFor   time.Duration
	Now       func() time.Time
	OnOpen    func(decision string)
}

type state struct {
	slow      atomic.Int32
	openUntil atomic.Int64
}

// New returns a breaker set with the default policy.
func New(onOpen func(string)) *Set {
	return &Set{Threshold: Threshold, OpenFor: OpenFor, Now: time.Now, OnOpen: onOpen}
}

func (s *Set) get(decision string) *state {
	if v, ok := s.m.Load(decision); ok {
		return v.(*state)
	}
	v, _ := s.m.LoadOrStore(decision, &state{})
	return v.(*state)
}

// Allow reports whether the decision may be evaluated (breaker closed or
// half open).
func (s *Set) Allow(decision string) bool {
	v, ok := s.m.Load(decision)
	if !ok {
		return true
	}
	return s.Now().UnixNano() >= v.(*state).openUntil.Load()
}

// Record notes one evaluation time against the budget.
func (s *Set) Record(decision string, evalMicros, budgetMicros int64) {
	if budgetMicros <= 0 {
		return
	}
	st := s.get(decision)
	if evalMicros <= Factor*budgetMicros {
		if st.slow.Load() != 0 {
			st.slow.Store(0)
		}
		return
	}
	if st.slow.Add(1) >= s.Threshold {
		st.slow.Store(0)
		st.openUntil.Store(s.Now().Add(s.OpenFor).UnixNano())
		if s.OnOpen != nil {
			s.OnOpen(decision)
		}
	}
}

// Open reports whether the breaker is currently open (status, tests).
func (s *Set) Open(decision string) bool { return !s.Allow(decision) }
