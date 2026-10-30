// SPDX-License-Identifier: MIT

// Package config reads the sidecar configuration from GEARBOX_* environment
// variables and command-line flags, and validates it.
package config

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the validated sidecar configuration.
type Config struct {
	ServerURL      string
	App            string
	Env            string
	Credential     string
	TrustedKeys    string // raw "id:base64" list
	Socket         string
	TCPAddr        string
	TCPToken       string
	Reflection     bool
	CacheDir       string
	FallbackBundle string
	PollInterval   time.Duration
	Feed           bool
	MaxBundleMB    int
	MaxInputBytes  int
	LogSample      float64
	LogBuffer      int
	LogSink        string // stdout | http | both | none
	LogHMACKey     []byte
	PIIKey         []byte
	OTLPEndpoint   string
	MetricsAddr    string
	HubURL         string
	HubTokenFile   string
	ShutdownGrace  time.Duration
	HeartbeatEvery time.Duration
	FetchJitter    time.Duration
	RPCTimeout     time.Duration
	SidecarID      string
	Node           string

	// Offline modes.
	BundleFile     string
	DefinitionsDir string
	Watch          bool
	AllowUnsigned  bool
}

// Offline reports whether the sidecar runs from local files.
func (c *Config) Offline() bool { return c.BundleFile != "" || c.DefinitionsDir != "" }

// Getenv is the environment lookup (replaceable in tests).
type Getenv func(string) string

// Load parses flags (args without the program name) and the environment.
func Load(args []string, getenv Getenv) (*Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	c := &Config{}
	fs := flag.NewFlagSet("gearbox-sidecar", flag.ContinueOnError)
	fs.StringVar(&c.BundleFile, "bundle-file", "", "serve this offline bundle file (no control plane)")
	fs.StringVar(&c.DefinitionsDir, "definitions-dir", "", "compile YAML/JSON definitions from this directory (no control plane)")
	fs.BoolVar(&c.Watch, "watch", false, "with --definitions-dir: recompile when files change")
	fs.BoolVar(&c.AllowUnsigned, "allow-unsigned", false, "accept unsigned bundles (only with --bundle-file or --definitions-dir)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	var errs []string
	str := func(name, def string) string {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return v
		}
		return def
	}
	fileOr := func(fileVar, valueVar string) string {
		if p := str(fileVar, ""); p != "" {
			b, err := os.ReadFile(p)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", fileVar, err))
				return ""
			}
			return strings.TrimSpace(string(b))
		}
		return str(valueVar, "")
	}
	dur := func(name string, def time.Duration) time.Duration {
		v := str(name, "")
		if v == "" {
			return def
		}
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			errs = append(errs, fmt.Sprintf("%s: invalid duration %q", name, v))
			return def
		}
		return d
	}
	integer := func(name string, def, min int) int {
		v := str(name, "")
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < min {
			errs = append(errs, fmt.Sprintf("%s: must be an integer >= %d", name, min))
			return def
		}
		return n
	}
	boolean := func(name string, def bool) bool {
		v := str(name, "")
		if v == "" {
			return def
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: must be true or false", name))
			return def
		}
		return b
	}

	c.ServerURL = strings.TrimRight(str("GEARBOX_SERVER_URL", ""), "/")
	c.App = str("GEARBOX_APP", "")
	c.Env = str("GEARBOX_ENV", "")
	c.Credential = fileOr("GEARBOX_CREDENTIAL_FILE", "GEARBOX_CREDENTIAL")
	c.TrustedKeys = fileOr("GEARBOX_TRUSTED_KEYS_FILE", "GEARBOX_TRUSTED_KEYS")
	c.Socket = str("GEARBOX_SOCKET", "/var/run/gearbox/engine.sock")
	c.TCPAddr = str("GEARBOX_TCP_ADDR", "")
	c.TCPToken = fileOr("GEARBOX_TCP_TOKEN_FILE", "")
	c.Reflection = boolean("GEARBOX_REFLECTION", false)
	c.CacheDir = str("GEARBOX_CACHE_DIR", "/var/cache/gearbox")
	c.FallbackBundle = str("GEARBOX_FALLBACK_BUNDLE", "")
	c.PollInterval = dur("GEARBOX_POLL_INTERVAL", 30*time.Second)
	c.Feed = boolean("GEARBOX_FEED", true)
	c.MaxBundleMB = integer("GEARBOX_MAX_BUNDLE_MB", 64, 1)
	c.MaxInputBytes = integer("GEARBOX_MAX_INPUT_BYTES", 262144, 1)
	c.LogBuffer = integer("GEARBOX_LOG_BUFFER", 65536, 16)
	c.LogSink = str("GEARBOX_LOG_SINK", "stdout")
	c.OTLPEndpoint = str("GEARBOX_OTLP_ENDPOINT", "")
	c.MetricsAddr = str("GEARBOX_METRICS_ADDR", ":9464")
	c.HubURL = strings.TrimRight(str("GEARBOX_HUB_URL", ""), "/")
	c.HubTokenFile = str("GEARBOX_HUB_TOKEN_FILE", "")
	c.ShutdownGrace = dur("GEARBOX_SHUTDOWN_GRACE", 10*time.Second)
	c.HeartbeatEvery = dur("GEARBOX_HEARTBEAT_INTERVAL", 30*time.Second)
	c.FetchJitter = dur("GEARBOX_FETCH_JITTER", 2*time.Second)
	c.RPCTimeout = dur("GEARBOX_RPC_TIMEOUT", 11*time.Second)
	if k := fileOr("GEARBOX_LOG_HMAC_KEY_FILE", "GEARBOX_LOG_HMAC_KEY"); k != "" {
		c.LogHMACKey = []byte(k)
	}
	if k := fileOr("GEARBOX_PII_HMAC_KEY_FILE", "GEARBOX_PII_HMAC_KEY"); k != "" {
		c.PIIKey = []byte(k)
	}
	host, _ := os.Hostname()
	c.SidecarID = str("GEARBOX_SIDECAR_ID", str("POD_NAME", host))
	c.Node = str("GEARBOX_NODE", str("NODE_NAME", ""))
	if v := str("GEARBOX_LOG_SAMPLE", ""); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || f > 1 {
			errs = append(errs, "GEARBOX_LOG_SAMPLE: must be a number between 0 and 1")
		}
		c.LogSample = f
	} else {
		c.LogSample = 0.01
	}

	// Validation.
	switch c.LogSink {
	case "stdout", "http", "both", "none":
	default:
		errs = append(errs, "GEARBOX_LOG_SINK: must be stdout, http, both or none")
	}
	if c.BundleFile != "" && c.DefinitionsDir != "" {
		errs = append(errs, "--bundle-file and --definitions-dir are mutually exclusive")
	}
	if c.AllowUnsigned && !c.Offline() {
		errs = append(errs, "--allow-unsigned is accepted only with --bundle-file or --definitions-dir")
	}
	if c.Watch && c.DefinitionsDir == "" {
		errs = append(errs, "--watch needs --definitions-dir")
	}
	if !c.Offline() {
		if c.ServerURL == "" {
			errs = append(errs, "GEARBOX_SERVER_URL is required (or run offline with --bundle-file / --definitions-dir)")
		}
		if c.App == "" {
			errs = append(errs, "GEARBOX_APP is required")
		}
		if c.Env == "" {
			errs = append(errs, "GEARBOX_ENV is required")
		}
		if c.Credential == "" {
			errs = append(errs, "GEARBOX_CREDENTIAL_FILE or GEARBOX_CREDENTIAL is required")
		}
		if c.TrustedKeys == "" {
			errs = append(errs, "GEARBOX_TRUSTED_KEYS_FILE or GEARBOX_TRUSTED_KEYS is required")
		}
	} else if c.BundleFile != "" && !c.AllowUnsigned && c.TrustedKeys == "" {
		errs = append(errs, "--bundle-file needs GEARBOX_TRUSTED_KEYS(_FILE) or --allow-unsigned")
	}
	if c.LogSink == "http" || c.LogSink == "both" {
		if c.ServerURL == "" || c.Credential == "" {
			errs = append(errs, "GEARBOX_LOG_SINK=http needs GEARBOX_SERVER_URL and a credential")
		}
	}
	if c.TCPAddr != "" {
		host, _, err := net.SplitHostPort(c.TCPAddr)
		if err != nil {
			errs = append(errs, "GEARBOX_TCP_ADDR: "+err.Error())
		} else if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			errs = append(errs, "GEARBOX_TCP_ADDR must be a loopback address (127.0.0.1, ::1 or localhost)")
		}
		if c.TCPToken == "" {
			errs = append(errs, "GEARBOX_TCP_TOKEN_FILE is required when GEARBOX_TCP_ADDR is set")
		}
	}
	if c.PollInterval < time.Second && !c.Offline() {
		errs = append(errs, "GEARBOX_POLL_INTERVAL must be at least 1s")
	}
	if len(errs) > 0 {
		return nil, errors.New("invalid configuration:\n  " + strings.Join(errs, "\n  "))
	}
	return c, nil
}

// MaxBundleBytes is the compressed bundle limit.
func (c *Config) MaxBundleBytes() int64 { return int64(c.MaxBundleMB) << 20 }

// MaxDecompressedBytes is 4× the compressed limit.
func (c *Config) MaxDecompressedBytes() int64 { return 4 * c.MaxBundleBytes() }

// LocalMetricsURL is the URL the healthcheck subcommand calls.
func (c *Config) LocalMetricsURL(path string) string {
	host, port, err := net.SplitHostPort(c.MetricsAddr)
	if err != nil {
		return "http://127.0.0.1:9464" + path
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + path
}
