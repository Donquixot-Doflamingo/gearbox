// Command gearboxctl is the Gearbox CLI for people and CI.
//
// It reads GEARBOX_SERVER_URL (default http://localhost:8080), GEARBOX_TOKEN
// (or the token saved by `login` in ~/.config/gearbox/token) and
// GEARBOX_APP; --server, --token and --app override them. Run
// `gearboxctl help` for the command list.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ORG/gearbox/controlplane/internal/client"
)

const usageText = `gearboxctl — Gearbox command line

Global flags (anywhere): --server URL  --token TOKEN  --app APP  --json

Session
  login [--token gbx_pat_…]            save a personal access token (dev servers mint one)
  logout | whoami

Apps
  apps ls

Definitions (YAML or JSON files shaped like API resources; see docs/04-reference.md §10)
  push <dir|file>... [--cut -m MSG]    create/update drafts (and members/rows); --cut cuts versions
  pull <dir> [--version live|draft|N] [--env E] [--kind K]
  validate <dir|file>... [--remote]    local engine by default
  test <dir|file>... [--remote] [--env E]
  version cut <kind>/<name> [-m MSG] [--allow-breaking]
  schema   push <file>... | pull <name> [--version N] [-o FILE]
  decision push <file>... | pull <name> [-o FILE] | validate <file> | test <name> | eval <name> -i input.json [--env E] [--unit U] [--version N]
  segment members upload <segment> -f members.csv|members.json

Change requests
  change open --env E --title T <kind>/<name>@<v>[:rollout=5]... [--description D]
  change approve <id> [--comment C] | change ls [--status S] [--env E] | change get <id>
  cr open|approve|ls ...               (aliases)

Releases (environment from --env)
  release change-request <id>          (alias: release publish --cr <id>)
  release data <kind>/<name>@<v>...
  release rollout <decision> <percent>  (alias: --decision D --percent P)
  release rollback <generation> --reason R   (alias: --to G)
  release kill <gate> [--undo] --reason R    (alias: --gate G [--off])

Runtime and bundles
  decide <decision> --env E -i input.json [--unit U]
  bundle export --env E -o FILE [--credential gbx_cred_…]
  keys generate --out DIR [--id ID] [--if-missing]
`

// global options
type opts struct {
	server, token, app string
	json               bool
	flags              map[string]string
	bools              map[string]bool
	args               []string
}

// boolFlags never take a value.
var boolFlags = map[string]bool{"json": true, "cut": true, "remote": true, "allow-breaking": true, "undo": true, "off": true, "if-missing": true, "demo": true}

func parseArgs(argv []string) (*opts, error) {
	o := &opts{flags: map[string]string{}, bools: map[string]bool{}}
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			o.args = append(o.args, argv[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			o.args = append(o.args, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		val, hasVal := "", false
		if k, v, ok := strings.Cut(name, "="); ok {
			name, val, hasVal = k, v, true
		}
		if boolFlags[name] {
			o.bools[name] = !hasVal || val == "true"
			continue
		}
		if !hasVal {
			if i+1 >= len(argv) {
				return nil, fmt.Errorf("flag --%s needs a value", name)
			}
			i++
			val = argv[i]
		}
		o.flags[name] = val
	}
	o.server = first(o.flags["server"], os.Getenv("GEARBOX_SERVER_URL"), savedServer(), "http://localhost:8080")
	o.token = first(o.flags["token"], os.Getenv("GEARBOX_TOKEN"), savedToken())
	o.app = first(o.flags["app"], os.Getenv("GEARBOX_APP"))
	o.json = o.bools["json"]
	return o, nil
}

func first(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func (o *opts) flag(names ...string) string {
	for _, n := range names {
		if v := o.flags[n]; v != "" {
			return v
		}
	}
	return ""
}

func (o *opts) client() *client.Client {
	return &client.Client{BaseURL: o.server, Token: o.token}
}

func (o *opts) needApp() (string, error) {
	if o.app == "" {
		return "", errors.New("no app: pass --app or set GEARBOX_APP")
	}
	return o.app, nil
}

func (o *opts) needEnv() (string, error) {
	if e := o.flag("env", "e"); e != "" {
		return e, nil
	}
	return "", errors.New("pass --env")
}

func configDir() string {
	if d := os.Getenv("GEARBOX_CONFIG_DIR"); d != "" {
		return d
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "gearbox")
	}
	return filepath.Join(os.Getenv("HOME"), ".config", "gearbox")
}

func savedToken() string {
	b, _ := os.ReadFile(filepath.Join(configDir(), "token"))
	return strings.TrimSpace(string(b))
}

func savedServer() string {
	b, _ := os.ReadFile(filepath.Join(configDir(), "server"))
	return strings.TrimSpace(string(b))
}

func printJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func main() {
	o, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "gearboxctl:", err)
		os.Exit(2)
	}
	if len(o.args) == 0 || o.args[0] == "help" || o.bools["help"] || o.flags["h"] != "" {
		fmt.Print(usageText)
		return
	}
	if err := run(context.Background(), o, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "gearboxctl:", err)
		var p *client.Problem
		if errors.As(err, &p) && p.Status < 500 {
			os.Exit(3)
		}
		os.Exit(1)
	}
}

var errUsage = errors.New("unknown command; see `gearboxctl help`")

func run(ctx context.Context, o *opts, w io.Writer) error {
	cmd, rest := o.args[0], o.args[1:]
	sub := ""
	if len(rest) > 0 {
		sub = rest[0]
	}
	shift := func() *opts { c := *o; c.args = rest[1:]; return &c }
	o.args = rest
	switch cmd {
	case "login":
		return cmdLogin(ctx, o, w)
	case "logout":
		_ = os.Remove(filepath.Join(configDir(), "token"))
		fmt.Fprintln(w, "logged out")
		return nil
	case "whoami":
		return cmdWhoami(ctx, o, w)
	case "apps":
		if sub == "ls" || sub == "list" || sub == "" {
			return cmdAppsLs(ctx, o, w)
		}
	case "push":
		return cmdPush(ctx, o, w)
	case "pull":
		return cmdPull(ctx, o, w)
	case "validate":
		return cmdValidate(ctx, o, w)
	case "test":
		return cmdTest(ctx, o, w)
	case "version":
		if sub == "cut" {
			return cmdCut(ctx, shift(), w)
		}
	case "schema", "decision":
		kind := cmd + "s"
		switch sub {
		case "push":
			return cmdPush(ctx, shift(), w)
		case "pull":
			return cmdPullOne(ctx, shift(), kind, w)
		case "validate":
			return cmdValidate(ctx, shift(), w)
		case "test":
			return cmdTestRemote(ctx, shift(), w)
		case "eval":
			return cmdEval(ctx, shift(), w)
		}
	case "segment":
		if sub == "members" && len(rest) > 1 && rest[1] == "upload" {
			c := *o
			c.args = rest[2:]
			return cmdMembersUpload(ctx, &c, w)
		}
	case "change", "cr":
		switch sub {
		case "open":
			return cmdCROpen(ctx, shift(), w)
		case "approve":
			return cmdCRApprove(ctx, shift(), w)
		case "ls", "list":
			return cmdCRLs(ctx, shift(), w)
		case "get":
			return cmdCRGet(ctx, shift(), w)
		}
	case "release":
		return cmdRelease(ctx, shift(), sub, w)
	case "decide":
		return cmdDecide(ctx, o, w)
	case "bundle":
		if sub == "export" {
			return cmdBundleExport(ctx, shift(), w)
		}
	case "keys":
		if sub == "generate" {
			return cmdKeysGenerate(shift(), w)
		}
	}
	return errUsage
}

var _ = flag.ErrHelp
