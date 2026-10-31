package server

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Change is one structured difference (JSON pointer path).
type Change struct {
	Op   string `json:"op"`
	Path string `json:"path"`
	From any    `json:"from,omitempty"`
	To   any    `json:"to,omitempty"`
}

func toYAML(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return string(raw)
	}
	_ = enc.Close()
	return buf.String()
}

// diffSpecs returns a unified diff of the YAML forms and structured changes.
func diffSpecs(a, b json.RawMessage, aName, bName string) (string, []Change) {
	unified := unifiedDiff(toYAML(a), toYAML(b), aName, bName)
	var av, bv any
	_ = json.Unmarshal(a, &av)
	_ = json.Unmarshal(b, &bv)
	changes := []Change{}
	jsonChanges(av, bv, "", &changes)
	return unified, changes
}

func jsonChanges(a, b any, path string, out *[]Change) {
	switch at := a.(type) {
	case map[string]any:
		bt, ok := b.(map[string]any)
		if !ok {
			*out = append(*out, Change{Op: "replace", Path: orRoot(path), From: a, To: b})
			return
		}
		keys := map[string]bool{}
		for k := range at {
			keys[k] = true
		}
		for k := range bt {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			p := path + "/" + strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
			av, aok := at[k]
			bv, bok := bt[k]
			switch {
			case aok && !bok:
				*out = append(*out, Change{Op: "remove", Path: p, From: av})
			case !aok && bok:
				*out = append(*out, Change{Op: "add", Path: p, To: bv})
			default:
				jsonChanges(av, bv, p, out)
			}
		}
	case []any:
		bt, ok := b.([]any)
		if !ok {
			*out = append(*out, Change{Op: "replace", Path: orRoot(path), From: a, To: b})
			return
		}
		n := len(at)
		if len(bt) > n {
			n = len(bt)
		}
		for i := 0; i < n; i++ {
			p := fmt.Sprintf("%s/%d", path, i)
			switch {
			case i >= len(bt):
				*out = append(*out, Change{Op: "remove", Path: p, From: at[i]})
			case i >= len(at):
				*out = append(*out, Change{Op: "add", Path: p, To: bt[i]})
			default:
				jsonChanges(at[i], bt[i], p, out)
			}
		}
	default:
		ab, _ := json.Marshal(a)
		bb, _ := json.Marshal(b)
		if string(ab) != string(bb) {
			*out = append(*out, Change{Op: "replace", Path: orRoot(path), From: a, To: b})
		}
	}
}

// unifiedDiff is a line diff (LCS) in unified format with 3 lines of context.
func unifiedDiff(a, b, aName, bName string) string {
	if a == b {
		return ""
	}
	al, bl := splitLines(a), splitLines(b)
	n, m := len(al), len(bl)
	// LCS table; specs are small (a few thousand lines at most).
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if al[i] == bl[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	type op struct {
		kind byte // ' ', '-', '+'
		text string
		ai   int
		bi   int
	}
	var ops []op
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && al[i] == bl[j]:
			ops = append(ops, op{' ', al[i], i, j})
			i++
			j++
		case i < n && (j >= m || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, op{'-', al[i], i, j})
			i++
		default:
			ops = append(ops, op{'+', bl[j], i, j})
			j++
		}
	}
	const ctx = 3
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", aName, bName)
	for k := 0; k < len(ops); {
		if ops[k].kind == ' ' {
			k++
			continue
		}
		start := k - ctx
		if start < 0 {
			start = 0
		}
		end := k
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run-end > 2*ctx || run == len(ops) {
				end += min(ctx, run-end)
				break
			}
			end = run
		}
		aStart, bStart, aCount, bCount := ops[start].ai, ops[start].bi, 0, 0
		for _, o := range ops[start:end] {
			if o.kind != '+' {
				aCount++
			}
			if o.kind != '-' {
				bCount++
			}
		}
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", aStart+1, aCount, bStart+1, bCount)
		for _, o := range ops[start:end] {
			sb.WriteByte(o.kind)
			sb.WriteString(o.text)
			sb.WriteByte('\n')
		}
		k = end
	}
	return sb.String()
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
