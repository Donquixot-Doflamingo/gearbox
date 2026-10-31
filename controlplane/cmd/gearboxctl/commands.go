package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ORG/gearbox/controlplane/internal/auth"
	"github.com/ORG/gearbox/controlplane/internal/client"
	"github.com/ORG/gearbox/controlplane/internal/defs"
	"github.com/ORG/gearbox/controlplane/internal/engineapi"
	"github.com/ORG/gearbox/engine"
	"github.com/ORG/gearbox/engine/bundle"
)

// ---------------------------------------------------------------- session

func cmdLogin(ctx context.Context, o *opts, w io.Writer) error {
	tok := o.flags["token"]
	c := &client.Client{BaseURL: o.server}
	if tok == "" {
		// Development servers accept anonymous requests: mint a token.
		var out struct {
			Token string `json:"token"`
		}
		err := c.Do(ctx, http.MethodPost, "/tokens", map[string]any{"label": "gearboxctl " + hostname(), "expiresInDays": 90}, &out)
		switch {
		case err == nil:
			tok = out.Token
		case client.IsStatus(err, http.StatusUnauthorized):
			fmt.Fprintf(w, "Sign in at %s, create a personal access token (POST /api/v1/tokens), and paste it here: ", o.server)
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			tok = strings.TrimSpace(line)
		default:
			return err
		}
	}
	c.Token = tok
	var sess struct {
		Member struct{ Email, Role string } `json:"member"`
	}
	if err := c.Do(ctx, http.MethodGet, "/session", nil, &sess); err != nil {
		return fmt.Errorf("token rejected: %w", err)
	}
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(configDir(), "token"), []byte(tok+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(configDir(), "server"), []byte(o.server+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(w, "logged in to %s as %s (%s); token saved in %s\n", o.server, sess.Member.Email, sess.Member.Role, filepath.Join(configDir(), "token"))
	return nil
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}

func cmdWhoami(ctx context.Context, o *opts, w io.Writer) error {
	var sess map[string]any
	if err := o.client().Do(ctx, http.MethodGet, "/session", nil, &sess); err != nil {
		return err
	}
	printJSON(w, sess["member"])
	return nil
}

func cmdAppsLs(ctx context.Context, o *opts, w io.Writer) error {
	var out struct {
		Items []struct {
			Name         string   `json:"name"`
			Description  string   `json:"description"`
			Environments []string `json:"environments"`
		} `json:"items"`
	}
	if err := o.client().Do(ctx, http.MethodGet, "/apps?limit=200", nil, &out); err != nil {
		return err
	}
	if o.json {
		printJSON(w, out.Items)
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tENVIRONMENTS\tDESCRIPTION")
	for _, a := range out.Items {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", a.Name, strings.Join(a.Environments, ","), a.Description)
	}
	return tw.Flush()
}

// ---------------------------------------------------------------- definitions

func loadPaths(paths []string) ([]defs.File, error) {
	if len(paths) == 0 {
		return nil, errors.New("give at least one file or directory")
	}
	var files []defs.File
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if st.IsDir() {
			fs, err := defs.Load(p)
			if err != nil {
				return nil, err
			}
			files = append(files, fs...)
			continue
		}
		f, err := defs.ReadFile(p)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	sort.SliceStable(files, func(i, j int) bool { return kindRank(files[i].Kind) < kindRank(files[j].Kind) })
	return files, nil
}

func kindRank(k string) int {
	for i, x := range defs.KindOrder {
		if x == k {
			return i
		}
	}
	return 99
}

func cmdPush(ctx context.Context, o *opts, w io.Writer) error {
	app, err := o.needApp()
	if err != nil {
		return err
	}
	files, err := loadPaths(o.args)
	if err != nil {
		return err
	}
	c := o.client()
	for _, f := range files {
		if _, err := c.Push(ctx, app, []defs.File{f}, w); err != nil {
			return err
		}
		if o.bools["cut"] {
			v, cut, err := c.CutVersion(ctx, app, f.Kind, f.Name, o.flag("m", "message"), o.bools["allow-breaking"])
			if err != nil {
				return fmt.Errorf("cut %s/%s: %w", f.Kind, f.Name, err)
			}
			if cut {
				fmt.Fprintf(w, "%-12s %-28s cut v%d\n", f.Kind, f.Name, v.Version)
			}
		}
	}
	return nil
}

func cmdCut(ctx context.Context, o *opts, w io.Writer) error {
	app, err := o.needApp()
	if err != nil {
		return err
	}
	if len(o.args) != 1 {
		return errors.New("usage: version cut <kind>/<name> [-m MSG]")
	}
	kind, name, ok := strings.Cut(o.args[0], "/")
	if !ok {
		return errors.New("want <kind>/<name>")
	}
	v, cut, err := o.client().CutVersion(ctx, app, kind, name, o.flag("m", "message"), o.bools["allow-breaking"])
	if err != nil {
		return err
	}
	if !cut {
		fmt.Fprintf(w, "%s/%s: draft equals v%d; nothing to cut\n", kind, name, v.Version)
		return nil
	}
	fmt.Fprintf(w, "%s/%s@%d (%s)\n", kind, name, v.Version, v.ContentHash)
	return nil
}

// fetchFile pulls one resource as a definition file.
func fetchFile(ctx context.Context, c *client.Client, app, kind, name, which, env string) (defs.File, error) {
	r, err := c.GetResource(ctx, app, kind, name)
	if err != nil {
		return defs.File{}, err
	}
	f := defs.File{Kind: kind, Name: name, Description: r.Description}
	var spec json.RawMessage
	switch which {
	case "", "draft":
		spec = r.Draft.Spec
		f.Version = r.LatestVersion
	default:
		v := int64(0)
		if which == "live" {
			l, ok := r.Live[env]
			if !ok {
				return f, fmt.Errorf("%s/%s is not live in %s", kind, name, env)
			}
			v = l.Version
		} else if v, err = strconv.ParseInt(which, 10, 64); err != nil {
			return f, fmt.Errorf("--version must be live, draft or a number")
		}
		var rv struct {
			Spec json.RawMessage `json:"spec"`
		}
		if err := c.Do(ctx, http.MethodGet, client.Path("apps", app, kind, name, "versions", v), nil, &rv); err != nil {
			return f, err
		}
		spec, f.Version = rv.Spec, v
	}
	// Drop read-only counters from the file.
	var m map[string]any
	if json.Unmarshal(spec, &m) == nil {
		delete(m, "rowCount")
		if st, ok := m["static"].(map[string]any); ok {
			delete(st, "memberCount")
		}
		spec, _ = json.Marshal(m)
	}
	f.Spec = spec
	if which == "" || which == "draft" {
		switch kind {
		case "segments":
			var page struct {
				Items      []string `json:"items"`
				NextCursor string   `json:"nextCursor"`
			}
			cursor := ""
			for {
				q := "?limit=200"
				if cursor != "" {
					q += "&cursor=" + url.QueryEscape(cursor)
				}
				page.NextCursor = ""
				if err := c.Do(ctx, http.MethodGet, client.Path("apps", app, "segments", name, "members")+q, nil, &page); err != nil {
					return f, err
				}
				f.SegmentMembers = append(f.SegmentMembers, page.Items...)
				if cursor = page.NextCursor; cursor == "" {
					break
				}
			}
		case "lookups":
			f.LookupRows = map[string]json.RawMessage{}
			var page struct {
				Items []struct {
					Key   string          `json:"key"`
					Value json.RawMessage `json:"value"`
				} `json:"items"`
				NextCursor string `json:"nextCursor"`
			}
			cursor := ""
			for {
				q := "?limit=200"
				if cursor != "" {
					q += "&cursor=" + url.QueryEscape(cursor)
				}
				page.NextCursor = ""
				if err := c.Do(ctx, http.MethodGet, client.Path("apps", app, "lookups", name, "rows")+q, nil, &page); err != nil {
					return f, err
				}
				for _, r := range page.Items {
					f.LookupRows[r.Key] = r.Value
				}
				if cursor = page.NextCursor; cursor == "" {
					break
				}
			}
		}
	}
	return f, nil
}

func writeDef(f defs.File, path string, w io.Writer) error {
	asJSON := strings.HasSuffix(path, ".json")
	b, err := defs.Marshal(f, asJSON)
	if err != nil {
		return err
	}
	if path == "" || path == "-" {
		_, err = w.Write(b)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func cmdPull(ctx context.Context, o *opts, w io.Writer) error {
	app, err := o.needApp()
	if err != nil {
		return err
	}
	if len(o.args) != 1 {
		return errors.New("usage: pull <dir> [--version live|draft|N] [--env E]")
	}
	dir := o.args[0]
	which := first(o.flag("version"), "draft")
	env := first(o.flag("env"), "prod")
	c := o.client()
	kinds := defs.KindOrder
	if k := o.flag("kind"); k != "" {
		kinds = []string{k}
	}
	n := 0
	for _, kind := range kinds {
		var page struct {
			Items []struct {
				Name string         `json:"name"`
				Live map[string]any `json:"live"`
			} `json:"items"`
		}
		if err := c.Do(ctx, http.MethodGet, client.Path("apps", app, kind)+"?limit=200", nil, &page); err != nil {
			return err
		}
		for _, it := range page.Items {
			if which == "live" {
				if _, ok := it.Live[env]; !ok {
					continue
				}
			}
			f, err := fetchFile(ctx, c, app, kind, it.Name, which, env)
			if err != nil {
				return err
			}
			p := filepath.Join(dir, kind, it.Name+".yaml")
			if err := writeDef(f, p, w); err != nil {
				return err
			}
			fmt.Fprintf(w, "wrote %s\n", p)
			n++
		}
	}
	fmt.Fprintf(w, "%d files\n", n)
	return nil
}

func cmdPullOne(ctx context.Context, o *opts, kind string, w io.Writer) error {
	app, err := o.needApp()
	if err != nil {
		return err
	}
	if len(o.args) != 1 {
		return fmt.Errorf("usage: %s pull <name> [--version N] [-o FILE]", strings.TrimSuffix(kind, "s"))
	}
	f, err := fetchFile(ctx, o.client(), app, kind, o.args[0], o.flag("version"), first(o.flag("env"), "prod"))
	if err != nil {
		return err
	}
	return writeDef(f, o.flag("o", "out"), w)
}

// fileResolver resolves references among local files.
type fileResolver map[string]engine.Resource

func (r fileResolver) Get(kind engine.Kind, name string, version int64) (engine.Resource, bool) {
	if version > 0 {
		x, ok := r[fmt.Sprintf("%s/%s@%d", kind, name, version)]
		return x, ok
	}
	x, ok := r[string(kind)+"/"+name]
	return x, ok
}

func toResource(f defs.File) engine.Resource {
	v := f.Version
	if v == 0 {
		v = 1
	}
	return engine.Resource{Kind: engine.Kind(f.Kind), Name: f.Name, Version: v, Spec: f.Spec, SegmentMembers: f.SegmentMembers, LookupRows: f.LookupRows}
}

func localSet(files []defs.File) (engine.Set, fileResolver) {
	res := fileResolver{}
	set := engine.Set{App: "local", Environment: "local", Generation: 1}
	for _, f := range files {
		r := toResource(f)
		res[fmt.Sprintf("%s/%s@%d", r.Kind, r.Name, r.Version)] = r
		res[string(r.Kind)+"/"+r.Name] = r
		set.Resources = append(set.Resources, r)
	}
	return set, res
}

func cmdValidate(ctx context.Context, o *opts, w io.Writer) error {
	files, err := loadPaths(o.args)
	if err != nil {
		return err
	}
	bad := 0
	if o.bools["remote"] {
		app, err := o.needApp()
		if err != nil {
			return err
		}
		for _, f := range files {
			var v struct {
				Valid    bool                                             `json:"valid"`
				Problems []struct{ Code, Severity, Path, Message string } `json:"problems"`
			}
			err := o.client().Do(ctx, http.MethodPost, client.Path("apps", app, f.Kind, f.Name, "validate"), map[string]any{"spec": f.Spec}, &v)
			if client.IsStatus(err, http.StatusNotFound) {
				fmt.Fprintf(w, "%s/%s: not on the server yet (push it first for remote validation)\n", f.Kind, f.Name)
				continue
			}
			if err != nil {
				return err
			}
			bad += report(w, f, v.Valid, len(v.Problems), func(i int) (string, string, string, string) {
				p := v.Problems[i]
				return p.Severity, p.Code, p.Path, p.Message
			})
		}
	} else {
		_, res := localSet(files)
		for _, f := range files {
			v := engineapi.Hybrid{}.ValidateResource(toResource(f), res)
			bad += report(w, f, v.Valid, len(v.Problems), func(i int) (string, string, string, string) {
				p := v.Problems[i]
				return p.Severity, p.Code, p.Path, p.Message
			})
		}
	}
	if bad > 0 {
		return fmt.Errorf("%d of %d definitions are invalid", bad, len(files))
	}
	return nil
}

func report(w io.Writer, f defs.File, valid bool, n int, at func(int) (string, string, string, string)) int {
	state := "ok"
	if !valid {
		state = "INVALID"
	}
	fmt.Fprintf(w, "%-12s %-28s %s\n", f.Kind, f.Name, state)
	for i := 0; i < n; i++ {
		sev, code, path, msg := at(i)
		fmt.Fprintf(w, "    %-7s %-22s %s  %s\n", sev, code, path, msg)
	}
	if valid {
		return 0
	}
	return 1
}

func cmdTest(ctx context.Context, o *opts, w io.Writer) error {
	if o.bools["remote"] {
		return cmdTestRemote(ctx, o, w)
	}
	files, err := loadPaths(o.args)
	if err != nil {
		return err
	}
	set, _ := localSet(files)
	snap, err := engineapi.Hybrid{}.Compile(set)
	if err != nil {
		return err
	}
	failed := 0
	for _, d := range snap.Decisions() {
		rs, err := snap.RunTests(ctx, d.Name, 0)
		if err != nil {
			return err
		}
		failed += printTests(w, d.Name, rs)
	}
	if failed > 0 {
		return fmt.Errorf("%d test cases failed", failed)
	}
	return nil
}

func printTests(w io.Writer, name string, rs []engine.TestResult) int {
	failed := 0
	for _, r := range rs {
		state := "PASS"
		if !r.OK {
			state, failed = "FAIL", failed+1
		}
		fmt.Fprintf(w, "%s  %s / %s", state, name, r.Name)
		if r.Error != "" {
			fmt.Fprintf(w, "  (%s)", r.Error)
		}
		fmt.Fprintln(w)
		for _, m := range r.Mismatches {
			fmt.Fprintf(w, "      %s: expected %s, got %s\n", m.Path, m.Expected, m.Actual)
		}
	}
	return failed
}

func cmdTestRemote(ctx context.Context, o *opts, w io.Writer) error {
	app, err := o.needApp()
	if err != nil {
		return err
	}
	names := o.args
	for i, n := range names {
		if st, err := os.Stat(n); err == nil && !st.IsDir() {
			f, err := defs.ReadFile(n)
			if err != nil {
				return err
			}
			names[i] = f.Name
		}
	}
	failed := 0
	for _, name := range names {
		body := map[string]any{"environment": first(o.flag("env"), "dev")}
		if v := o.flag("version"); v != "" {
			n, _ := strconv.ParseInt(v, 10, 64)
			body["source"], body["version"] = "version", n
		}
		var out struct {
			Results []engine.TestResult `json:"results"`
		}
		if err := o.client().Do(ctx, http.MethodPost, client.Path("apps", app, "decisions", name, "tests", "run"), body, &out); err != nil {
			return err
		}
		failed += printTests(w, name, out.Results)
	}
	if failed > 0 {
		return fmt.Errorf("%d test cases failed", failed)
	}
	return nil
}

func readInput(path string) (json.RawMessage, error) {
	if path == "" {
		return nil, errors.New("pass -i input.json (or - for stdin)")
	}
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
	if json.Valid(b) {
		return b, nil
	}
	j, err := defs.YAMLToJSON(b)
	if err != nil {
		return nil, fmt.Errorf("input is neither JSON nor YAML: %w", err)
	}
	return j, nil
}

func cmdEval(ctx context.Context, o *opts, w io.Writer) error {
	app, err := o.needApp()
	if err != nil {
		return err
	}
	if len(o.args) != 1 {
		return errors.New("usage: decision eval <name> -i input.json [--env E] [--unit U] [--version N]")
	}
	in, err := readInput(o.flag("i", "input"))
	if err != nil {
		return err
	}
	body := map[string]any{"input": in, "environment": first(o.flag("env"), "dev"), "source": "draft"}
	if u := o.flag("unit"); u != "" {
		body["ctx"] = map[string]any{"unit": u}
	}
	if v := o.flag("version"); v != "" {
		n, _ := strconv.ParseInt(v, 10, 64)
		body["source"], body["version"] = "version", n
	}
	var out json.RawMessage
	if err := o.client().Do(ctx, http.MethodPost, client.Path("apps", app, "decisions", o.args[0], "evaluate"), body, &out); err != nil {
		return err
	}
	var v any
	_ = json.Unmarshal(out, &v)
	printJSON(w, v)
	return nil
}

func cmdDecide(ctx context.Context, o *opts, w io.Writer) error {
	app, err := o.needApp()
	if err != nil {
		return err
	}
	env, err := o.needEnv()
	if err != nil {
		return err
	}
	if len(o.args) != 1 {
		return errors.New("usage: decide <decision> --env E -i input.json [--unit U]")
	}
	in, err := readInput(o.flag("i", "input"))
	if err != nil {
		return err
	}
	body := map[string]any{"decision": o.args[0], "input": in}
	if u := o.flag("unit"); u != "" {
		body["unitId"] = u
	}
	var out any
	if err := o.client().Do(ctx, http.MethodPost, client.Path("apps", app, "environments", env, "decide"), body, &out); err != nil {
		return err
	}
	printJSON(w, out)
	return nil
}

func cmdMembersUpload(ctx context.Context, o *opts, w io.Writer) error {
	app, err := o.needApp()
	if err != nil {
		return err
	}
	file := o.flag("f", "file")
	if len(o.args) != 1 || file == "" {
		return errors.New("usage: segment members upload <segment> -f members.csv|members.json")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	ct := "text/csv"
	if strings.HasSuffix(file, ".json") {
		ct = "application/json"
	}
	resp, err := o.client().Raw(ctx, http.MethodPut, client.Path("apps", app, "segments", o.args[0], "members"), strings.NewReader(string(data)), ct, nil)
	if err != nil {
		return err
	}
	var out struct {
		Total    int   `json:"total"`
		Revision int64 `json:"revision"`
	}
	if resp.Status >= 400 {
		var p client.Problem
		_ = json.Unmarshal(resp.Body, &p)
		p.Status = resp.Status
		return &p
	}
	_ = json.Unmarshal(resp.Body, &out)
	fmt.Fprintf(w, "%s: %d members stored (draft revision %d); cut a version to release them\n", o.args[0], out.Total, out.Revision)
	return nil
}

// ---------------------------------------------------------------- change requests and releases

// parseItem parses kind/name@version[:rollout=N].
func parseItem(s string) (map[string]any, error) {
	spec, opt, _ := strings.Cut(s, ":")
	kn, ver, ok := strings.Cut(spec, "@")
	kind, name, ok2 := strings.Cut(kn, "/")
	if !ok || !ok2 {
		return nil, fmt.Errorf("item %q: want <kind>/<name>@<version>", s)
	}
	v, err := strconv.ParseInt(ver, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("item %q: bad version", s)
	}
	it := map[string]any{"kind": kind, "name": name, "version": v}
	if opt != "" {
		p, ok := strings.CutPrefix(opt, "rollout=")
		pct, err := strconv.Atoi(p)
		if !ok || err != nil {
			return nil, fmt.Errorf("item %q: option must be rollout=<percent>", s)
		}
		it["rollout"] = map[string]any{"percent": pct}
	}
	return it, nil
}

type crView struct {
	ID          string `json:"id"`
	Environment string `json:"environment"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Author      string `json:"author"`
	Approvals   []any  `json:"approvals"`
	Required    int    `json:"requiredApprovals"`
	Tests       *struct {
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	} `json:"tests"`
	PublishedGeneration *int64 `json:"publishedGeneration"`
}

func cmdCROpen(ctx context.Context, o *opts, w io.Writer) error {
	app, err := o.needApp()
	if err != nil {
		return err
	}
	env, err := o.needEnv()
	if err != nil {
		return err
	}
	var items []map[string]any
	itemArgs := o.args
	if s := o.flag("i", "item"); s != "" {
		itemArgs = append(itemArgs, strings.Split(s, ",")...)
	}
	for _, a := range itemArgs {
		it, err := parseItem(a)
		if err != nil {
			return err
		}
		items = append(items, it)
	}
	if len(items) == 0 {
		return errors.New("list at least one <kind>/<name>@<version>")
	}
	body := map[string]any{"environment": env, "title": first(o.flag("title", "t"), "Change from gearboxctl"), "items": items}
	if d := o.flag("description"); d != "" {
		body["description"] = d
	}
	var cr crView
	if err := o.client().Do(ctx, http.MethodPost, client.Path("apps", app, "change-requests"), body, &cr); err != nil {
		return err
	}
	if o.json {
		printJSON(w, cr)
		return nil
	}
	fmt.Fprintf(w, "%s opened for %s: %s (%d/%d approvals", cr.ID, cr.Environment, cr.Status, len(cr.Approvals), cr.Required)
	if cr.Tests != nil {
		fmt.Fprintf(w, ", tests %d passed", cr.Tests.Passed)
	}
	fmt.Fprintln(w, ")")
	return nil
}

func cmdCRApprove(ctx context.Context, o *opts, w io.Writer) error {
	app, err := o.needApp()
	if err != nil {
		return err
	}
	if len(o.args) != 1 {
		return errors.New("usage: change approve <id> [--comment C]")
	}
	var cr crView
	if err := o.client().Do(ctx, http.MethodPost, client.Path("apps", app, "change-requests", o.args[0], "approve"), map[string]any{"comment": o.flag("comment")}, &cr); err != nil {
		return err
	}
	fmt.Fprintf(w, "%s: %s (%d/%d approvals)\n", cr.ID, cr.Status, len(cr.Approvals), cr.Required)
	return nil
}

func cmdCRGet(ctx context.Context, o *opts, w io.Writer) error {
	app, err := o.needApp()
	if err != nil {
		return err
	}
	if len(o.args) != 1 {
		return errors.New("usage: change get <id>")
	}
	var cr any
	if err := o.client().Do(ctx, http.MethodGet, client.Path("apps", app, "change-requests", o.args[0]), nil, &cr); err != nil {
		return err
	}
	printJSON(w, cr)
	return nil
}

func cmdCRLs(ctx context.Context, o *opts, w io.Writer) error {
	app, err := o.needApp()
	if err != nil {
		return err
	}
	q := url.Values{"limit": {"200"}}
	if s := o.flag("status"); s != "" {
		q.Set("status", s)
	}
	if e := o.flag("env"); e != "" {
		q.Set("environment", e)
	}
	var out struct {
		Items []crView `json:"items"`
	}
	if err := o.client().Do(ctx, http.MethodGet, client.Path("apps", app, "change-requests")+"?"+q.Encode(), nil, &out); err != nil {
		return err
	}
	if o.json {
		printJSON(w, out.Items)
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tENV\tSTATUS\tAPPROVALS\tAUTHOR\tTITLE")
	for _, c := range out.Items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d/%d\t%s\t%s\n", c.ID, c.Environment, c.Status, len(c.Approvals), c.Required, c.Author, c.Title)
	}
	return tw.Flush()
}

func cmdRelease(ctx context.Context, o *opts, sub string, w io.Writer) error {
	app, err := o.needApp()
	if err != nil {
		return err
	}
	env, err := o.needEnv()
	if err != nil {
		return err
	}
	arg := func(i int, flags ...string) string {
		if v := o.flag(flags...); v != "" {
			return v
		}
		if i < len(o.args) {
			return o.args[i]
		}
		return ""
	}
	body := map[string]any{}
	if r := o.flag("reason"); r != "" {
		body["reason"] = r
	}
	switch sub {
	case "change-request", "publish", "cr":
		id := arg(0, "cr", "change-request")
		if id == "" {
			return errors.New("usage: release change-request <id>")
		}
		body["type"], body["changeRequestId"] = "change_request", id
	case "data":
		var items []map[string]any
		for _, a := range o.args {
			it, err := parseItem(a)
			if err != nil {
				return err
			}
			items = append(items, it)
		}
		if len(items) == 0 {
			return errors.New("usage: release data <kind>/<name>@<version> [more items]")
		}
		body["type"], body["items"] = "data", items
	case "rollout":
		d, p := arg(0, "decision"), arg(1, "percent")
		pct, err := strconv.Atoi(p)
		if d == "" || err != nil {
			return errors.New("usage: release rollout <decision> <percent>")
		}
		body["type"], body["decision"], body["percent"] = "rollout", d, pct
	case "rollback":
		g, err := strconv.ParseInt(arg(0, "to", "to-generation"), 10, 64)
		if err != nil {
			return errors.New("usage: release rollback <generation> --reason R")
		}
		body["type"], body["toGeneration"] = "rollback", g
		if c := o.flag("cr"); c != "" {
			body["changeRequestId"] = c
		}
	case "kill":
		g := arg(0, "gate")
		if g == "" {
			return errors.New("usage: release kill <gate> [--undo] --reason R")
		}
		body["type"], body["gate"], body["killed"] = "kill_switch", g, !(o.bools["undo"] || o.bools["off"])
	default:
		return errUsage
	}
	var rel struct {
		Generation   int64  `json:"generation"`
		Type         string `json:"type"`
		ManifestHash string `json:"manifestHash"`
		BundleBytes  int64  `json:"bundleBytes"`
		BreakGlass   bool   `json:"breakGlass"`
	}
	if err := o.client().Do(ctx, http.MethodPost, client.Path("apps", app, "environments", env, "releases"), body, &rel); err != nil {
		return err
	}
	if o.json {
		printJSON(w, rel)
		return nil
	}
	fmt.Fprintf(w, "published %s/%s generation %d (%s, %d bytes, %s)", app, env, rel.Generation, rel.Type, rel.BundleBytes, rel.ManifestHash)
	if rel.BreakGlass {
		fmt.Fprint(w, " [break-glass]")
	}
	fmt.Fprintln(w)
	return nil
}

// ---------------------------------------------------------------- bundles and keys

func cmdBundleExport(ctx context.Context, o *opts, w io.Writer) error {
	app, err := o.needApp()
	if err != nil {
		return err
	}
	env, err := o.needEnv()
	if err != nil {
		return err
	}
	out := o.flag("o", "out")
	if out == "" {
		return errors.New("pass -o FILE")
	}
	cred := first(o.flag("credential"), os.Getenv("GEARBOX_CREDENTIAL"))
	if cred == "" {
		// Borrow a short-lived credential (admin only) and revoke it after.
		var created struct {
			Credential struct {
				ID string `json:"id"`
			} `json:"credential"`
			Secret string `json:"secret"`
		}
		if err := o.client().Do(ctx, http.MethodPost, client.Path("apps", app, "environments", env, "credentials"), map[string]any{"label": "gearboxctl bundle export"}, &created); err != nil {
			return fmt.Errorf("no --credential and cannot create one: %w", err)
		}
		cred = created.Secret
		defer func() {
			_ = o.client().Do(context.Background(), http.MethodPost, client.Path("apps", app, "environments", env, "credentials", created.Credential.ID, "revoke"), nil, nil)
		}()
	}
	cc := &client.Client{BaseURL: o.server, Token: cred}
	var head struct {
		Generation int64 `json:"generation"`
		Envelope   struct {
			URL    string `json:"url"`
			SHA256 string `json:"sha256"`
		} `json:"envelope"`
		Resources []struct {
			URL    string `json:"url"`
			SHA256 string `json:"sha256"`
		} `json:"resources"`
	}
	if err := cc.Do(ctx, http.MethodGet, "/sidecar/v1/head", nil, &head); err != nil {
		return err
	}
	env1, err := download(ctx, o.server, head.Envelope.URL, head.Envelope.SHA256)
	if err != nil {
		return fmt.Errorf("envelope: %w", err)
	}
	built := bundle.Built{Envelope: env1, EnvelopeSHA256: head.Envelope.SHA256, TotalBytes: int64(len(env1))}
	for _, r := range head.Resources {
		b, err := download(ctx, o.server, r.URL, r.SHA256)
		if err != nil {
			return fmt.Errorf("blob %s: %w", r.SHA256, err)
		}
		built.Blobs = append(built.Blobs, bundle.Blob{SHA256: r.SHA256, Data: b})
		built.TotalBytes += int64(len(b))
	}
	data, err := engineapi.HybridBundler{}.WriteFile(built)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(w, "wrote %s: %s/%s generation %d, %d resources, %d bytes\n", out, app, env, head.Generation, len(built.Blobs), len(data))
	return nil
}

// download fetches a URL from the head and checks its hash. Filesystem blob
// URLs are rebased onto --server when the server's public URL differs.
func download(ctx context.Context, server, raw, sum string) ([]byte, error) {
	try := []string{raw}
	if u, err := url.Parse(raw); err == nil && strings.Contains(u.Path, "/sidecar/v1/blobs/") {
		if s, err := url.Parse(server); err == nil && s.Host != u.Host {
			u.Scheme, u.Host = s.Scheme, s.Host
			try = append(try, u.String())
		}
	}
	var lastErr error
	for _, t := range try {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, t, nil)
		res, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		b, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil || res.StatusCode != 200 {
			lastErr = fmt.Errorf("HTTP %d", res.StatusCode)
			continue
		}
		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) != sum {
			return nil, errors.New("hash mismatch")
		}
		return b, nil
	}
	return nil, lastErr
}

func cmdKeysGenerate(o *opts, w io.Writer) error {
	out := o.flag("out", "o")
	if out == "" {
		return errors.New("pass --out DIR")
	}
	id := first(o.flag("id"), "gbx-"+time.Now().UTC().Format("20060102"))
	path := filepath.Join(out, "signing.key")
	var key ed25519.PrivateKey
	if b, err := os.ReadFile(path); err == nil {
		if !o.bools["if-missing"] {
			return fmt.Errorf("%s exists (use --if-missing to keep it)", path)
		}
		if key, err = auth.ParsePrivateKey(b); err != nil {
			return err
		}
		if t, err := os.ReadFile(filepath.Join(out, "trusted-keys.txt")); err == nil {
			if kid, _, ok := strings.Cut(strings.TrimSpace(string(t)), ":"); ok && o.flag("id") == "" {
				id = kid
			}
		}
	} else {
		if _, key, err = ed25519.GenerateKey(rand.Reader); err != nil {
			return err
		}
		if err := os.MkdirAll(out, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key.Seed())+"\n"), 0o600); err != nil {
			return err
		}
	}
	line := auth.TrustedKeyLine(id, key)
	if err := os.WriteFile(filepath.Join(out, "trusted-keys.txt"), []byte(line+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(w, "trusted key: %s\n", line)
	return nil
}
