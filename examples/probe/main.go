// gearbox-probe is a stand-alone test client for a Gearbox sidecar. It talks
// to the sidecar only through the public Go client (clients/go), exactly like
// a real service would, and prints JSON so scripts can assert on it.
//
//	gearbox-probe [-target unix:///var/run/gearbox/engine.sock] status
//	gearbox-probe decide <decision> <input.json|-> [-unit ID] [-now RFC3339]
//	gearbox-probe gate <gate> [-unit ID]
//	gearbox-probe variant <experiment> -unit ID
//	gearbox-probe bench <decision> <input.json> [-n 2000]   # client-side latency
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	gearbox "github.com/ORG/gearbox/clients/go"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gearbox-probe:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	global := flag.NewFlagSet("gearbox-probe", flag.ContinueOnError)
	target := global.String("target", envOr("GEARBOX_TARGET", "unix:///var/run/gearbox/engine.sock"), "sidecar address")
	if err := global.Parse(args); err != nil {
		return err
	}
	rest := global.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: gearbox-probe [-target T] status|decide|gate|variant|bench ...")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := gearbox.Dial(ctx, *target, gearbox.WithDefaultTimeout(2*time.Second))
	if err != nil {
		return err
	}
	defer c.Close()

	cmd, rest := rest[0], rest[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	unit := fs.String("unit", "", "unit ID for rollouts and experiments")
	now := fs.String("now", "", "ctx.now (RFC3339)")
	n := fs.Int("n", 2000, "bench: number of calls")
	pos, err := parseInterleaved(fs, rest)
	if err != nil {
		return err
	}

	switch cmd {
	case "status":
		s, err := c.Status(ctx)
		if err != nil {
			return err
		}
		return out(s)
	case "decide":
		if len(pos) != 2 {
			return fmt.Errorf("decide <decision> <input.json|->")
		}
		input, err := readInput(pos[1])
		if err != nil {
			return err
		}
		opts := []gearbox.CallOption{gearbox.WithTrace()}
		if *unit != "" {
			opts = append(opts, gearbox.WithUnit(*unit))
		}
		if *now != "" {
			t, err := time.Parse(time.RFC3339, *now)
			if err != nil {
				return err
			}
			opts = append(opts, gearbox.WithNow(t))
		}
		d, err := c.Decide(ctx, pos[0], input, opts...)
		if err != nil {
			return err
		}
		return out(map[string]any{"output": json.RawMessage(d.Raw), "version": d.Version, "dependencyHash": d.DependencyHash,
			"generation": d.Generation, "fallbackUsed": d.FallbackUsed, "evalMicros": d.EvalMicros, "path": d.Path})
	case "gate":
		if len(pos) != 1 {
			return fmt.Errorf("gate <gate>")
		}
		g, err := c.CheckGate(ctx, pos[0], *unit, nil)
		if err != nil {
			return err
		}
		return out(g)
	case "variant":
		if len(pos) != 1 || *unit == "" {
			return fmt.Errorf("variant <experiment> -unit ID")
		}
		v, err := c.GetVariant(ctx, pos[0], *unit, nil)
		if err != nil {
			return err
		}
		return out(v)
	case "bench":
		if len(pos) != 2 {
			return fmt.Errorf("bench <decision> <input.json>")
		}
		input, err := readInput(pos[1])
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(input)
		lat := make([]time.Duration, 0, *n)
		for i := 0; i < *n; i++ {
			t0 := time.Now()
			if _, err := c.Decide(ctx, pos[0], json.RawMessage(raw), gearbox.WithUnit(fmt.Sprintf("u%d", i))); err != nil {
				return err
			}
			lat = append(lat, time.Since(t0))
		}
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		q := func(p float64) float64 { return float64(lat[int(p*float64(len(lat)-1))].Microseconds()) }
		return out(map[string]any{"calls": *n, "p50Micros": q(0.50), "p95Micros": q(0.95), "p99Micros": q(0.99), "maxMicros": q(1)})
	}
	return fmt.Errorf("unknown command %q", cmd)
}

// parseInterleaved accepts flags before or after positional arguments.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}
	return pos, nil
}

func readInput(path string) (any, error) {
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(os.Stdin)
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("input is not JSON: %w", err)
	}
	return v, nil
}

func out(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
