// SPDX-License-Identifier: MIT

package metrics

import (
	"strings"
	"testing"
)

func TestExposition(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter("x_total", "Things.", "a")
	c.Inc(`q"v`)
	c.Add(2, `q"v`)
	g := r.NewGauge("g", "G.")
	g.Set(4)
	h := r.NewHistogram("h_seconds", "H.", []float64{0.1, 1}, "d")
	h.Observe(0.05, "x")
	h.Observe(5, "x")
	r.NewGaugeFunc("f", "F.", func() float64 { return 1.5 })
	var sb strings.Builder
	r.Write(&sb)
	out := sb.String()
	for _, want := range []string{`x_total{a="q\"v"} 3`, "g 4", `h_seconds_bucket{d="x",le="0.1"} 1`, `h_seconds_bucket{d="x",le="+Inf"} 2`, `h_seconds_count{d="x"} 2`, "f 1.5", "# TYPE h_seconds histogram"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	b, total := h.Snapshot()
	if total != 2 || b[0] != 1 || b[2] != 1 {
		t.Fatalf("snapshot %v %d", b, total)
	}
	if q := Quantile(h.Bounds(), b, 0.5); q != 0.1 {
		t.Fatalf("p50 = %v", q)
	}
}
