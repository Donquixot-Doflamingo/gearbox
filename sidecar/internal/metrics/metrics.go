// SPDX-License-Identifier: MIT

// Package metrics is a small, dependency-free Prometheus registry: counters,
// gauges and histograms with fixed label names, exposed in the text format
// (version 0.0.4). Recording is lock-free after a label set's first use
// (sync.Map lookup plus atomics).
package metrics

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Registry holds metric families.
type Registry struct {
	mu       sync.Mutex
	families []family
}

type family interface {
	write(w io.Writer)
	name() string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) register(f family) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.families = append(r.families, f)
}

// ServeHTTP writes every family in the text format.
func (r *Registry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	r.Write(w)
}

// Write writes the exposition.
func (r *Registry) Write(w io.Writer) {
	r.mu.Lock()
	fams := append([]family(nil), r.families...)
	r.mu.Unlock()
	sort.Slice(fams, func(i, j int) bool { return fams[i].name() < fams[j].name() })
	for _, f := range fams {
		f.write(w)
	}
}

type base struct {
	n, help, typ string
	labels       []string
	series       sync.Map // label key → *series
}

func (b *base) name() string { return b.n }

func (b *base) header(w io.Writer) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", b.n, escapeHelp(b.help), b.n, b.typ)
}

func labelKey(values []string) string { return strings.Join(values, "\xff") }

func (b *base) labelText(values []string, extra ...string) string {
	if len(b.labels) == 0 && len(extra) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteByte('{')
	n := 0
	for i, l := range b.labels {
		if n > 0 {
			sb.WriteByte(',')
		}
		v := ""
		if i < len(values) {
			v = values[i]
		}
		sb.WriteString(l)
		sb.WriteString(`="`)
		sb.WriteString(escapeValue(v))
		sb.WriteByte('"')
		n++
	}
	for i := 0; i+1 < len(extra); i += 2 {
		if n > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(extra[i])
		sb.WriteString(`="`)
		sb.WriteString(escapeValue(extra[i+1]))
		sb.WriteByte('"')
		n++
	}
	sb.WriteByte('}')
	return sb.String()
}

func (b *base) sortedSeries() []*series {
	var out []*series
	b.series.Range(func(_, v any) bool {
		out = append(out, v.(*series))
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

type series struct {
	key     string
	values  []string
	bits    atomic.Uint64 // float64 bits (counter/gauge) or sum (histogram)
	count   atomic.Uint64
	buckets []atomic.Uint64
}

func (s *series) add(f float64) {
	for {
		old := s.bits.Load()
		nv := math.Float64bits(math.Float64frombits(old) + f)
		if s.bits.CompareAndSwap(old, nv) {
			return
		}
	}
}

func (b *base) get(values []string, nb int) *series {
	k := labelKey(values)
	if v, ok := b.series.Load(k); ok {
		return v.(*series)
	}
	s := &series{key: k, values: append([]string(nil), values...)}
	if nb > 0 {
		s.buckets = make([]atomic.Uint64, nb)
	}
	v, _ := b.series.LoadOrStore(k, s)
	return v.(*series)
}

// ---- counter

// Counter is a monotonically increasing value per label set.
type Counter struct{ base }

// NewCounter registers a counter.
func (r *Registry) NewCounter(name, help string, labels ...string) *Counter {
	c := &Counter{base{n: name, help: help, typ: "counter", labels: labels}}
	r.register(c)
	return c
}

// Add adds v (>= 0).
func (c *Counter) Add(v float64, labels ...string) { c.get(labels, 0).add(v) }

// Inc adds 1.
func (c *Counter) Inc(labels ...string) { c.Add(1, labels...) }

// Value returns the current value (tests).
func (c *Counter) Value(labels ...string) float64 {
	return math.Float64frombits(c.get(labels, 0).bits.Load())
}

func (c *Counter) write(w io.Writer) {
	c.header(w)
	for _, s := range c.sortedSeries() {
		fmt.Fprintf(w, "%s%s %s\n", c.n, c.labelText(s.values), fmtFloat(math.Float64frombits(s.bits.Load())))
	}
}

// ---- gauge

// Gauge is a value that can go up and down.
type Gauge struct {
	base
	fn func() float64
}

// NewGauge registers a gauge.
func (r *Registry) NewGauge(name, help string, labels ...string) *Gauge {
	g := &Gauge{base: base{n: name, help: help, typ: "gauge", labels: labels}}
	r.register(g)
	return g
}

// NewGaugeFunc registers an unlabelled gauge computed at scrape time.
func (r *Registry) NewGaugeFunc(name, help string, fn func() float64) *Gauge {
	g := &Gauge{base: base{n: name, help: help, typ: "gauge"}, fn: fn}
	r.register(g)
	return g
}

// Set sets the value.
func (g *Gauge) Set(v float64, labels ...string) { g.get(labels, 0).bits.Store(math.Float64bits(v)) }

// Add adds v (may be negative).
func (g *Gauge) Add(v float64, labels ...string) { g.get(labels, 0).add(v) }

// Value returns the current value.
func (g *Gauge) Value(labels ...string) float64 {
	if g.fn != nil {
		return g.fn()
	}
	return math.Float64frombits(g.get(labels, 0).bits.Load())
}

func (g *Gauge) write(w io.Writer) {
	g.header(w)
	if g.fn != nil {
		fmt.Fprintf(w, "%s %s\n", g.n, fmtFloat(g.fn()))
		return
	}
	for _, s := range g.sortedSeries() {
		fmt.Fprintf(w, "%s%s %s\n", g.n, g.labelText(s.values), fmtFloat(math.Float64frombits(s.bits.Load())))
	}
}

// ---- histogram

// Histogram counts observations into cumulative buckets.
type Histogram struct {
	base
	bounds []float64
}

// DefaultLatencyBuckets suit sidecar evaluation times (seconds).
var DefaultLatencyBuckets = []float64{5e-6, 10e-6, 25e-6, 50e-6, 100e-6, 250e-6, 500e-6, 1e-3, 2.5e-3, 5e-3, 10e-3, 25e-3, 50e-3, 100e-3, 250e-3, 1}

// NewHistogram registers a histogram with upper bounds (ascending).
func (r *Registry) NewHistogram(name, help string, bounds []float64, labels ...string) *Histogram {
	h := &Histogram{base: base{n: name, help: help, typ: "histogram", labels: labels}, bounds: bounds}
	r.register(h)
	return h
}

// Observe records v.
func (h *Histogram) Observe(v float64, labels ...string) {
	s := h.get(labels, len(h.bounds))
	i := sort.SearchFloat64s(h.bounds, v)
	if i < len(h.bounds) {
		s.buckets[i].Add(1)
	}
	s.count.Add(1)
	s.add(v)
}

// Snapshot returns per-bucket (non-cumulative) counts, the overflow count
// and the total, summed over every label set.
func (h *Histogram) Snapshot() (buckets []uint64, total uint64) {
	buckets = make([]uint64, len(h.bounds)+1)
	h.series.Range(func(_, v any) bool {
		s := v.(*series)
		var inBuckets uint64
		for i := range s.buckets {
			n := s.buckets[i].Load()
			buckets[i] += n
			inBuckets += n
		}
		c := s.count.Load()
		total += c
		if c > inBuckets {
			buckets[len(h.bounds)] += c - inBuckets
		}
		return true
	})
	return buckets, total
}

// Bounds returns the bucket upper bounds.
func (h *Histogram) Bounds() []float64 { return h.bounds }

// Quantile estimates a quantile from bucket deltas (upper bound of the
// bucket that holds it).
func Quantile(bounds []float64, deltas []uint64, q float64) float64 {
	var total uint64
	for _, d := range deltas {
		total += d
	}
	if total == 0 {
		return 0
	}
	rank := uint64(math.Ceil(q * float64(total)))
	var acc uint64
	for i, d := range deltas {
		acc += d
		if acc >= rank {
			if i < len(bounds) {
				return bounds[i]
			}
			return bounds[len(bounds)-1]
		}
	}
	return bounds[len(bounds)-1]
}

func (h *Histogram) write(w io.Writer) {
	h.header(w)
	for _, s := range h.sortedSeries() {
		var acc uint64
		for i, b := range h.bounds {
			acc += s.buckets[i].Load()
			fmt.Fprintf(w, "%s_bucket%s %d\n", h.n, h.labelText(s.values, "le", fmtFloat(b)), acc)
		}
		c := s.count.Load()
		fmt.Fprintf(w, "%s_bucket%s %d\n", h.n, h.labelText(s.values, "le", "+Inf"), c)
		fmt.Fprintf(w, "%s_sum%s %s\n", h.n, h.labelText(s.values), fmtFloat(math.Float64frombits(s.bits.Load())))
		fmt.Fprintf(w, "%s_count%s %d\n", h.n, h.labelText(s.values), c)
	}
}

func fmtFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	case math.IsNaN(f):
		return "NaN"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func escapeHelp(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(s)
}

func escapeValue(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`).Replace(s)
}
