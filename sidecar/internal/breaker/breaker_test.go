// SPDX-License-Identifier: MIT

package breaker

import (
	"testing"
	"time"
)

func TestBreaker(t *testing.T) {
	now := time.Unix(1000, 0)
	opened := 0
	b := New(func(string) { opened++ })
	b.Now = func() time.Time { return now }
	for i := 0; i < 4; i++ {
		b.Record("d", 3000, 200)
	}
	b.Record("d", 100, 200) // a fast call resets the streak
	for i := 0; i < 4; i++ {
		b.Record("d", 3000, 200)
	}
	if !b.Allow("d") || opened != 0 {
		t.Fatal("opened too early")
	}
	b.Record("d", 3000, 200)
	if b.Allow("d") || opened != 1 || !b.Open("d") {
		t.Fatal("should be open")
	}
	now = now.Add(31 * time.Second)
	if !b.Allow("d") {
		t.Fatal("should be half open after 30s")
	}
	if !b.Allow("other") {
		t.Fatal("other decisions unaffected")
	}
}
