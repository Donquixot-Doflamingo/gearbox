// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) Getenv { return func(k string) string { return m[k] } }

func TestOnlineConfig(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "hmac")
	_ = os.WriteFile(key, []byte("secret\n"), 0o600)
	c, err := Load(nil, env(map[string]string{
		"GEARBOX_SERVER_URL": "https://cp/", "GEARBOX_APP": "a1", "GEARBOX_ENV": "prod", "GEARBOX_CREDENTIAL": "c",
		"GEARBOX_TRUSTED_KEYS": "k:x", "GEARBOX_POLL_INTERVAL": "5s", "GEARBOX_HMAC_KEY_FILE": key,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.ServerURL != "https://cp" || c.PollInterval != 5*time.Second || c.LogSink != "server" || string(c.PIIKey) != "secret" || string(c.LogHMACKey) != "secret" || c.MaxInputBytes != 262144 || c.Socket != "/var/run/gearbox/engine.sock" {
		t.Fatalf("config = %+v", c)
	}
	if c.LocalMetricsURL("/readyz") != "http://127.0.0.1:9464/readyz" {
		t.Fatal(c.LocalMetricsURL("/readyz"))
	}
}

func TestConfigErrors(t *testing.T) {
	cases := []struct {
		args []string
		env  map[string]string
		want string
	}{
		{nil, map[string]string{}, "GEARBOX_SERVER_URL is required"},
		{[]string{"--allow-unsigned"}, map[string]string{"GEARBOX_SERVER_URL": "x", "GEARBOX_APP": "a", "GEARBOX_ENV": "e", "GEARBOX_CREDENTIAL": "c", "GEARBOX_TRUSTED_KEYS": "k"}, "--allow-unsigned is accepted only"},
		{[]string{"--bundle-file", "f"}, map[string]string{}, "needs GEARBOX_TRUSTED_KEYS"},
		{[]string{"--definitions-dir", "d", "--allow-unsigned"}, map[string]string{"GEARBOX_TCP_ADDR": "0.0.0.0:7443", "GEARBOX_TCP_TOKEN_FILE": ""}, "loopback"},
		{[]string{"--definitions-dir", "d"}, map[string]string{"GEARBOX_LOG_SAMPLE": "2"}, "GEARBOX_LOG_SAMPLE"},
		{[]string{"--watch"}, map[string]string{}, "--watch needs --definitions-dir"},
	}
	for _, c := range cases {
		_, err := Load(c.args, env(c.env))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v %v: err = %v, want %q", c.args, c.env, err, c.want)
		}
	}
	c, err := Load([]string{"--definitions-dir", "d", "--watch", "--allow-unsigned"}, env(nil))
	if err != nil || c.App != "local" || c.Env != "dev" || c.LogSink != "stdout" {
		t.Fatalf("offline = %+v %v", c, err)
	}
}
