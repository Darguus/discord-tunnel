// Package config defines the user-facing configuration of Discord Tunnel.
//
// This schema is intentionally small and stable: it describes *what the user
// has* (a VLESS/Reality server and a list of apps to tunnel), not *how sing-box
// is wired*. The translation into a sing-box configuration lives in
// internal/singbox, so a sing-box schema change never leaks into a user's file.
package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// HealthProxyPort is the loopback SOCKS5 port that the tunnel exposes purely so
// the app can measure latency through the proxy path. Discord itself does not
// use it — it is captured by the TUN interface instead.
const HealthProxyPort = 39657

// Config is the whole on-disk configuration (config.json).
type Config struct {
	Server Server `json:"server"`
	Tunnel Tunnel `json:"tunnel"`
	App    App    `json:"app"`
}

// Server describes the remote VLESS + Reality endpoint.
type Server struct {
	Address string  `json:"address"`
	Port    uint16  `json:"port"`
	UUID    string  `json:"uuid"`
	Flow    string  `json:"flow"`
	Reality Reality `json:"reality"`
}

// Reality holds the REALITY handshake parameters. These must match the server
// exactly; a mismatch is what makes a client fingerprintable.
type Reality struct {
	ServerName  string `json:"server_name"`
	PublicKey   string `json:"public_key"`
	ShortID     string `json:"short_id"`
	Fingerprint string `json:"fingerprint"`
}

// Tunnel decides what traffic is sent through the server. Everything not
// matched here goes out the normal interface, untouched.
type Tunnel struct {
	// Processes are matched by executable name, case-insensitively.
	Processes []string `json:"processes"`
	// Domains is an optional escape hatch for traffic that originates from a
	// process you do not control (a browser tab, an overlay). Matched by suffix.
	Domains  []string `json:"domains"`
	LogLevel string   `json:"log_level"`
	// MTU of the virtual adapter. 1420 leaves room for the VLESS/TLS overhead.
	MTU uint32 `json:"mtu"`
}

// App holds preferences for the tray application itself.
type App struct {
	Autostart            bool `json:"autostart"`
	ConnectOnLaunch      bool `json:"connect_on_launch"`
	LaunchDiscordOnStart bool `json:"launch_discord_on_start"`
}

// Default returns a configuration with everything filled in except the secrets,
// which have no sane default and must come from the user's server.
func Default() Config {
	return Config{
		Server: Server{
			Port: 443,
			Flow: "xtls-rprx-vision",
			Reality: Reality{
				Fingerprint: "chrome",
			},
		},
		Tunnel: Tunnel{
			Processes: []string{"Discord.exe", "DiscordCanary.exe", "DiscordPTB.exe", "Update.exe"},
			Domains:   []string{},
			LogLevel:  "warn",
			MTU:       1420,
		},
		App: App{
			Autostart:       false,
			ConnectOnLaunch: true,
		},
	}
}

// Dir is where the app keeps its config and log: %APPDATA%\DiscordTunnel.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate config dir: %w", err)
	}
	return filepath.Join(base, "DiscordTunnel"), nil
}

// Path is the full path of config.json.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// LogPath is the full path of the tunnel log.
func LogPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tunnel.log"), nil
}

// Load reads config.json, applies defaults for omitted fields and validates the
// result. A config that fails validation is never returned: a half-valid tunnel
// is worse than no tunnel, because it silently leaks.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, &NotConfiguredError{Path: path}
		}
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}

	cfg := Default()
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes the config back, creating the directory if needed.
func Save(cfg Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	// 0600: the file holds the server UUID, which is a credential.
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// NotConfiguredError signals a first run: no config.json exists yet.
type NotConfiguredError struct{ Path string }

func (e *NotConfiguredError) Error() string {
	return "no configuration at " + e.Path
}

func (c *Config) applyDefaults() {
	d := Default()
	if c.Server.Flow == "" {
		c.Server.Flow = d.Server.Flow
	}
	if c.Server.Reality.Fingerprint == "" {
		c.Server.Reality.Fingerprint = d.Server.Reality.Fingerprint
	}
	if len(c.Tunnel.Processes) == 0 {
		c.Tunnel.Processes = d.Tunnel.Processes
	}
	if c.Tunnel.LogLevel == "" {
		c.Tunnel.LogLevel = d.Tunnel.LogLevel
	}
	if c.Tunnel.MTU == 0 {
		c.Tunnel.MTU = d.Tunnel.MTU
	}
}

var (
	uuidRe    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	shortIDRe = regexp.MustCompile(`^[0-9a-fA-F]{0,16}$`)
	// A REALITY public key is 32 bytes, base64-url encoded without padding.
	pubKeyRe = regexp.MustCompile(`^[0-9a-zA-Z_-]{43}$`)
)

// Validate reports every problem it can find before the tunnel is started,
// with messages aimed at whoever is editing config.json by hand.
func (c Config) Validate() error {
	var problems []string

	if c.Server.Address == "" {
		problems = append(problems, "server.address is empty")
	} else if net.ParseIP(c.Server.Address) == nil && !isHostname(c.Server.Address) {
		problems = append(problems, "server.address is neither an IP nor a hostname: "+c.Server.Address)
	}
	if c.Server.Port == 0 {
		problems = append(problems, "server.port is 0")
	}
	if !uuidRe.MatchString(c.Server.UUID) {
		problems = append(problems, "server.uuid is not a UUID")
	}
	if c.Server.Flow != "" && c.Server.Flow != "xtls-rprx-vision" {
		problems = append(problems, "server.flow must be empty or \"xtls-rprx-vision\", got "+strconv.Quote(c.Server.Flow))
	}

	r := c.Server.Reality
	if r.ServerName == "" {
		problems = append(problems, "server.reality.server_name is empty")
	}
	if !pubKeyRe.MatchString(r.PublicKey) {
		problems = append(problems, "server.reality.public_key is not a 43-char base64url key")
	}
	if !shortIDRe.MatchString(r.ShortID) {
		problems = append(problems, "server.reality.short_id must be up to 16 hex characters")
	}
	if len(r.ShortID)%2 != 0 {
		problems = append(problems, "server.reality.short_id must have an even number of hex characters")
	}

	if len(c.Tunnel.Processes) == 0 && len(c.Tunnel.Domains) == 0 {
		problems = append(problems, "tunnel.processes and tunnel.domains are both empty: nothing would be tunnelled")
	}
	for _, p := range c.Tunnel.Processes {
		if !strings.HasSuffix(strings.ToLower(p), ".exe") {
			problems = append(problems, "tunnel.processes entry should be an executable name like Discord.exe, got "+strconv.Quote(p))
		}
	}
	switch c.Tunnel.LogLevel {
	case "trace", "debug", "info", "warn", "error", "fatal", "panic":
	default:
		problems = append(problems, "tunnel.log_level must be one of trace/debug/info/warn/error, got "+strconv.Quote(c.Tunnel.LogLevel))
	}
	if c.Tunnel.MTU < 576 || c.Tunnel.MTU > 9000 {
		problems = append(problems, "tunnel.mtu must be between 576 and 9000, got "+strconv.Itoa(int(c.Tunnel.MTU)))
	}

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

func isHostname(s string) bool {
	if len(s) > 253 || !strings.Contains(s, ".") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, ch := range label {
			isAlnum := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')
			if !isAlnum && ch != '-' {
				return false
			}
		}
	}
	return true
}
