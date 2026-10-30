package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"

	"github.com/ORG/gearbox/controlplane/internal/defs"
)

// PushResult reports what Push did to one file.
type PushResult struct {
	Kind, Name string
	Changed    bool
	Data       bool // members or rows uploaded
}

// Push creates or updates the draft of each file (in dependency order) and
// replaces static segment members and lookup rows when given. Versions are
// not cut. log may be nil.
func (c *Client) Push(ctx context.Context, app string, files []defs.File, log io.Writer) ([]PushResult, error) {
	var out []PushResult
	for _, f := range files {
		r, changed, err := c.UpsertDraft(ctx, app, f.Kind, f.Name, f.Description, f.Spec)
		if err != nil {
			return out, fmt.Errorf("%s/%s: %w", f.Kind, f.Name, err)
		}
		res := PushResult{Kind: f.Kind, Name: f.Name, Changed: changed}
		if f.Kind == "segments" && f.SegmentMembers != nil {
			if err := c.Do(ctx, http.MethodPut, Path("apps", app, "segments", f.Name, "members"), map[string]any{"members": f.SegmentMembers}, nil); err != nil {
				return out, fmt.Errorf("%s/%s members: %w", f.Kind, f.Name, err)
			}
			res.Data = true
		}
		if f.Kind == "lookups" && f.LookupRows != nil {
			keys := make([]string, 0, len(f.LookupRows))
			for k := range f.LookupRows {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			rows := make([]map[string]any, 0, len(keys))
			for _, k := range keys {
				rows = append(rows, map[string]any{"key": k, "value": json.RawMessage(f.LookupRows[k])})
			}
			if err := c.Do(ctx, http.MethodPut, Path("apps", app, "lookups", f.Name, "rows"), map[string]any{"rows": rows}, nil); err != nil {
				return out, fmt.Errorf("%s/%s rows: %w", f.Kind, f.Name, err)
			}
			res.Data = true
		}
		if log != nil {
			state := "unchanged"
			if changed {
				state = "draft saved"
			}
			if r.LatestVersion == 0 && changed {
				state = "created"
			}
			if res.Data {
				state += " (data uploaded)"
			}
			fmt.Fprintf(log, "%-12s %-28s %s\n", f.Kind, f.Name, state)
		}
		out = append(out, res)
	}
	return out, nil
}
