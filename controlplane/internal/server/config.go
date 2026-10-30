package server

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Version of the server; set with -ldflags "-X …/server.Version=…".
var Version = "0.1.0-dev"

// MinSidecar is the oldest sidecar version this server supports.
const MinSidecar = "0.1.0"

// Config is read from GEARBOX_* environment variables. Names follow
// docs/04-reference.md; the older names in parentheses are accepted as
// aliases.
type Config struct {
	Addr        string // GEARBOX_HTTP_ADDR (GEARBOX_ADDR), default :8080
	MetricsAddr string // GEARBOX_METRICS_ADDR, default :9464 ("off" disables)
	PublicURL   string // GEARBOX_PUBLIC_URL, default http://localhost<addr>
	Store       string // GEARBOX_STORE: memory | file:///path | mongo | mongodb://…
	MongoURI    string // GEARBOX_MONGO_URI (with GEARBOX_STORE=mongo)
	MongoDB     string // GEARBOX_MONGO_DB, default gearbox
	ObjectStore string // GEARBOX_OBJECT_STORE: file://… | s3://bucket/prefix
	S3Endpoint  string // GEARBOX_S3_ENDPOINT
	S3Region    string // GEARBOX_S3_REGION
	S3PathStyle bool   // GEARBOX_S3_FORCE_PATH_STYLE
	S3Presign   string // GEARBOX_S3_PRESIGN_ENDPOINT
	DataDir     string // GEARBOX_DATA_DIR: dev key files, default ./.data

	SigningKey   string // GEARBOX_SIGNING_KEY_FILE (GEARBOX_SIGNING_KEY): file path or base64 seed
	SigningKeyID string // GEARBOX_SIGNING_KEY_ID
	HMACKey      string // GEARBOX_HMAC_KEY_FILE (GEARBOX_SECRET): installation HMAC key, file or value
	SessionKey   string // GEARBOX_SESSION_KEY_FILE: signs OIDC state cookies

	Auth                 string   // GEARBOX_AUTH_MODE (GEARBOX_AUTH): dev | oidc
	DevUser              string   // GEARBOX_DEV_USER, default dev@localhost
	DevRole              string   // GEARBOX_DEV_ROLE, default admin
	OIDCIssuer           string   // GEARBOX_OIDC_ISSUER
	OIDCClientID         string   // GEARBOX_OIDC_CLIENT_ID
	OIDCClientSecretFile string   // GEARBOX_OIDC_CLIENT_SECRET_FILE
	AdminEmails          []string // GEARBOX_ADMIN_EMAILS: bootstrap admins on first OIDC sign-in
	SessionTTL           time.Duration

	RedisURL string // GEARBOX_REDIS_URL
	UIDir    string // GEARBOX_UI_DIR
	UIOrigin string // GEARBOX_UI_ORIGIN

	LogRetention time.Duration // GEARBOX_LOG_RETENTION, default 336h
	LogLevel     string        // GEARBOX_LOG_LEVEL
	Engine       string        // GEARBOX_ENGINE: auto | real | lite

	Seed           bool   // GEARBOX_SEED / serve --seed
	SeedDir        string // GEARBOX_SEED_DIR, default examples/definitions
	SeedCredential string // GEARBOX_SEED_CREDENTIAL

	// PublishBenchmark runs the engine benchmark at publish and enforces
	// budgetMicros × BudgetFactor (GEARBOX_PUBLISH_BENCHMARK, GEARBOX_BUDGET_FACTOR).
	PublishBenchmark bool
	BudgetFactor     float64

	PingInterval time.Duration // SSE ping, default 15s
	URLTTL       time.Duration // GEARBOX_PRESIGN_TTL, default 15m
}

// Dev reports whether the server runs with development auth.
func (c Config) Dev() bool { return c.Auth == "dev" }

// ParseDuration accepts Go durations plus a "d" (days) suffix.
func ParseDuration(s string) (time.Duration, error) {
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(d)
		if err != nil {
			return 0, err
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func env(def string, names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return def
}

// FromEnv reads the configuration.
func FromEnv() (Config, error) {
	c := Config{
		Addr:                 env(":8080", "GEARBOX_HTTP_ADDR", "GEARBOX_ADDR"),
		MetricsAddr:          env(":9464", "GEARBOX_METRICS_ADDR"),
		PublicURL:            env("", "GEARBOX_PUBLIC_URL"),
		Store:                env("memory", "GEARBOX_STORE"),
		MongoURI:             env("", "GEARBOX_MONGO_URI"),
		MongoDB:              env("gearbox", "GEARBOX_MONGO_DB"),
		ObjectStore:          env("file://./.data/objects", "GEARBOX_OBJECT_STORE"),
		S3Endpoint:           env("", "GEARBOX_S3_ENDPOINT"),
		S3Region:             env("", "GEARBOX_S3_REGION"),
		S3PathStyle:          env("", "GEARBOX_S3_FORCE_PATH_STYLE") == "true",
		S3Presign:            env("", "GEARBOX_S3_PRESIGN_ENDPOINT"),
		DataDir:              env("./.data", "GEARBOX_DATA_DIR"),
		SigningKey:           env("", "GEARBOX_SIGNING_KEY_FILE", "GEARBOX_SIGNING_KEY"),
		SigningKeyID:         env("", "GEARBOX_SIGNING_KEY_ID"),
		HMACKey:              env("", "GEARBOX_HMAC_KEY_FILE", "GEARBOX_SECRET"),
		SessionKey:           env("", "GEARBOX_SESSION_KEY_FILE"),
		Auth:                 env("", "GEARBOX_AUTH_MODE", "GEARBOX_AUTH"),
		DevUser:              env("dev@localhost", "GEARBOX_DEV_USER"),
		DevRole:              env("admin", "GEARBOX_DEV_ROLE"),
		OIDCIssuer:           env("", "GEARBOX_OIDC_ISSUER"),
		OIDCClientID:         env("", "GEARBOX_OIDC_CLIENT_ID"),
		OIDCClientSecretFile: env("", "GEARBOX_OIDC_CLIENT_SECRET_FILE"),
		RedisURL:             env("", "GEARBOX_REDIS_URL"),
		UIDir:                env("", "GEARBOX_UI_DIR"),
		UIOrigin:             env("", "GEARBOX_UI_ORIGIN"),
		LogLevel:             env("info", "GEARBOX_LOG_LEVEL"),
		Engine:               env("auto", "GEARBOX_ENGINE"),
		Seed:                 env("", "GEARBOX_SEED") == "true",
		SeedDir:              env("examples/definitions", "GEARBOX_SEED_DIR"),
		SeedCredential:       env("", "GEARBOX_SEED_CREDENTIAL"),
		PublishBenchmark:     env("true", "GEARBOX_PUBLISH_BENCHMARK") == "true",
	}
	if c.Auth == "" {
		// The reference default is oidc; without an issuer that cannot work,
		// so fall back to dev mode (loudly logged at start).
		c.Auth = "dev"
		if c.OIDCIssuer != "" {
			c.Auth = "oidc"
		}
	}
	if c.Auth != "dev" && c.Auth != "oidc" {
		return c, fmt.Errorf("GEARBOX_AUTH_MODE must be dev or oidc, got %q", c.Auth)
	}
	if v := os.Getenv("GEARBOX_ADMIN_EMAILS"); v != "" {
		for _, e := range strings.Split(v, ",") {
			if e = strings.TrimSpace(e); e != "" {
				c.AdminEmails = append(c.AdminEmails, e)
			}
		}
	}
	var err error
	if c.LogRetention, err = ParseDuration(env("336h", "GEARBOX_LOG_RETENTION")); err != nil {
		return c, fmt.Errorf("GEARBOX_LOG_RETENTION: %w", err)
	}
	if c.SessionTTL, err = ParseDuration(env("12h", "GEARBOX_SESSION_TTL")); err != nil {
		return c, fmt.Errorf("GEARBOX_SESSION_TTL: %w", err)
	}
	if c.URLTTL, err = ParseDuration(env("15m", "GEARBOX_PRESIGN_TTL")); err != nil {
		return c, fmt.Errorf("GEARBOX_PRESIGN_TTL: %w", err)
	}
	if c.BudgetFactor, err = strconv.ParseFloat(env("1", "GEARBOX_BUDGET_FACTOR"), 64); err != nil {
		return c, fmt.Errorf("GEARBOX_BUDGET_FACTOR: %w", err)
	}
	if c.PublicURL == "" {
		host := c.Addr
		if strings.HasPrefix(host, ":") {
			host = "localhost" + host
		}
		c.PublicURL = "http://" + host
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	return c, nil
}

func (c *Config) defaults() {
	if c.Auth == "" {
		c.Auth = "dev"
	}
	if c.DevUser == "" {
		c.DevUser = "dev@localhost"
	}
	if c.DevRole == "" {
		c.DevRole = "admin"
	}
	if c.PublicURL == "" {
		c.PublicURL = "http://localhost:8080"
	}
	if c.LogRetention == 0 {
		c.LogRetention = 14 * 24 * time.Hour
	}
	if c.SessionTTL == 0 {
		c.SessionTTL = 12 * time.Hour
	}
	if c.BudgetFactor == 0 {
		c.BudgetFactor = 1
	}
	if c.PingInterval == 0 {
		c.PingInterval = 15 * time.Second
	}
	if c.URLTTL == 0 {
		c.URLTTL = 15 * time.Minute
	}
	if c.SigningKeyID == "" {
		c.SigningKeyID = "dev"
	}
	if c.DataDir == "" {
		c.DataDir = "./.data"
	}
}
