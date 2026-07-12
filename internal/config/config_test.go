package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func valid() Config {
	cfg := Default()
	cfg.Server = Server{
		Address: "203.0.113.10",
		Port:    443,
		UUID:    "11111111-2222-3333-4444-555555555555",
		Flow:    "xtls-rprx-vision",
		Reality: Reality{
			ServerName:  "www.googletagmanager.com",
			PublicKey:   "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			ShortID:     "0123456789abcdef",
			Fingerprint: "chrome",
		},
	}
	return cfg
}

func TestValidateAcceptsAGoodConfig(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("a valid config was rejected: %v", err)
	}
}

// Validation exists to fail loudly at startup instead of quietly at runtime: a
// tunnel built from a half-correct config does not refuse to start, it starts
// and leaks.
func TestValidateRejectsBadConfigs(t *testing.T) {
	tests := map[string]struct {
		mutate func(*Config)
		want   string
	}{
		"empty address":      {func(c *Config) { c.Server.Address = "" }, "address"},
		"zero port":          {func(c *Config) { c.Server.Port = 0 }, "port"},
		"malformed uuid":     {func(c *Config) { c.Server.UUID = "not-a-uuid" }, "uuid"},
		"truncated key":      {func(c *Config) { c.Server.Reality.PublicKey = "tooshort" }, "public_key"},
		"odd short id":       {func(c *Config) { c.Server.Reality.ShortID = "abc" }, "short_id"},
		"non-hex short id":   {func(c *Config) { c.Server.Reality.ShortID = "zzzz" }, "short_id"},
		"no server name":     {func(c *Config) { c.Server.Reality.ServerName = "" }, "server_name"},
		"unknown flow":       {func(c *Config) { c.Server.Flow = "made-up-flow" }, "flow"},
		"nothing to tunnel":  {func(c *Config) { c.Tunnel.Processes = nil; c.Tunnel.Domains = nil }, "nothing would be tunnelled"},
		"process is not exe": {func(c *Config) { c.Tunnel.Processes = []string{"Discord"} }, "executable name"},
		"absurd mtu":         {func(c *Config) { c.Tunnel.MTU = 70000 }, "mtu"},
		"unknown log level":  {func(c *Config) { c.Tunnel.LogLevel = "loud" }, "log_level"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := valid()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("expected %s to be rejected, but it passed validation", name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should mention %q so the user knows what to fix, got: %v", tc.want, err)
			}
		})
	}
}

func TestImportXrayReadsAVlessRealityClient(t *testing.T) {
	// A realistic Xray client config, of the shape Amnezia and the various
	// panels hand out. All identifiers here are placeholders.
	const xray = `{
	  "inbounds": [{ "port": 10808, "protocol": "socks" }],
	  "outbounds": [
	    {
	      "tag": "proxy",
	      "protocol": "vless",
	      "settings": {
	        "vnext": [{
	          "address": "203.0.113.10",
	          "port": 32533,
	          "users": [{
	            "id": "11111111-2222-3333-4444-555555555555",
	            "encryption": "none",
	            "flow": "xtls-rprx-vision"
	          }]
	        }]
	      },
	      "streamSettings": {
	        "network": "tcp",
	        "security": "reality",
	        "realitySettings": {
	          "serverName": "www.googletagmanager.com",
	          "fingerprint": "chrome",
	          "publicKey": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	          "shortId": "0123456789abcdef"
	        }
	      }
	    }
	  ]
	}`

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(xray), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := ImportXray(path)
	if err != nil {
		t.Fatalf("ImportXray: %v", err)
	}

	if cfg.Server.Address != "203.0.113.10" || cfg.Server.Port != 32533 {
		t.Errorf("server not carried over: %s:%d", cfg.Server.Address, cfg.Server.Port)
	}
	if cfg.Server.Reality.PublicKey != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" {
		t.Errorf("reality key not carried over: %q", cfg.Server.Reality.PublicKey)
	}
	if cfg.Server.Reality.ShortID != "0123456789abcdef" {
		t.Errorf("short id not carried over: %q", cfg.Server.Reality.ShortID)
	}
	// The import must arrive ready to run, not merely parsed.
	if err := cfg.Validate(); err != nil {
		t.Errorf("imported config does not validate: %v", err)
	}
}

func TestImportXrayRejectsAConfigWithNoVlessOutbound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"outbounds":[{"protocol":"freedom"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportXray(path); err == nil {
		t.Fatal("expected an error for a config with nothing to import")
	}
}
