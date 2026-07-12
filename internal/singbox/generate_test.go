package singbox

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Darguus/discord-tunnel/internal/config"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"
)

func sample() config.Config {
	cfg := config.Default()
	cfg.Server = config.Server{
		Address: "203.0.113.10",
		Port:    443,
		UUID:    "11111111-2222-3333-4444-555555555555",
		Flow:    "xtls-rprx-vision",
		Reality: config.Reality{
			ServerName:  "www.googletagmanager.com",
			PublicKey:   "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			ShortID:     "0123456789abcdef",
			Fingerprint: "chrome",
		},
	}
	return cfg
}

// TestGeneratedConfigIsAcceptedBySingBox is the test that earns its keep.
//
// The app generates sing-box JSON at runtime, so a schema mistake would not
// surface at compile time - it would surface as a failure to connect, on the
// user's machine, with a stack trace they cannot read. This runs the generated
// document through sing-box's own context-aware decoder, which is the same code
// path the real tunnel takes.
func TestGeneratedConfigIsAcceptedBySingBox(t *testing.T) {
	raw, err := Generate(sample(), `C:\temp\tunnel.log`)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	ctx := include.Context(context.Background())
	options, err := sjson.UnmarshalExtendedContext[option.Options](ctx, raw)
	if err != nil {
		t.Fatalf("sing-box rejected the generated config: %v\n\n%s", err, raw)
	}

	if len(options.Inbounds) != 2 {
		t.Fatalf("want a TUN inbound and a health inbound, got %d inbounds", len(options.Inbounds))
	}
	if options.Inbounds[0].Type != "tun" {
		t.Errorf("first inbound must be the TUN adapter, got %q", options.Inbounds[0].Type)
	}
	if options.Route.Final != tagDirect {
		t.Errorf("untunnelled traffic must fall through to %q, got %q", tagDirect, options.Route.Final)
	}
	if !options.Route.AutoDetectInterface {
		t.Error("auto_detect_interface must be on, or the proxy connection loops back into the TUN")
	}
}

// TestOnlyListedProcessesAreTunnelled guards the core promise of the app: that
// it is a split tunnel. If a refactor ever routed everything to the proxy, the
// app would still "work" - and would silently send the user's entire machine
// through a VPS. This asserts the shape of the routing table instead.
func TestOnlyListedProcessesAreTunnelled(t *testing.T) {
	cfg := sample()
	cfg.Tunnel.Processes = []string{"Discord.exe"}
	cfg.Tunnel.Domains = nil

	raw, err := Generate(cfg, "tunnel.log")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var doc struct {
		Route struct {
			Final string           `json:"final"`
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("re-parse: %v", err)
	}

	if doc.Route.Final != "direct" {
		t.Fatalf("unmatched traffic must go direct, got %q - this would tunnel the whole machine", doc.Route.Final)
	}

	for _, rule := range doc.Route.Rules {
		if rule["outbound"] != tagProxy {
			continue
		}
		// Every rule that reaches the proxy must be narrowed by something. A bare
		// {"outbound": "proxy"} would be a catch-all.
		_, byProcess := rule["process_name"]
		_, byInbound := rule["inbound"]
		if !byProcess && !byInbound {
			t.Errorf("unconditional route to the proxy: %v", rule)
		}
	}
}

func TestGeneratedConfigCarriesRealityIdentity(t *testing.T) {
	raw, err := Generate(sample(), "tunnel.log")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	text := string(raw)

	// The uTLS fingerprint is what makes the handshake look like a browser's.
	// Dropping it does not break the connection, it just makes it detectable -
	// exactly the kind of regression no runtime error would ever catch.
	for _, want := range []string{"utls", "chrome", "reality", "xtls-rprx-vision", "xudp"} {
		if !strings.Contains(text, want) {
			t.Errorf("generated config is missing %q", want)
		}
	}
}

func TestVoiceTrafficIsNotRestrictedToTCP(t *testing.T) {
	raw, err := Generate(sample(), "tunnel.log")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var doc struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("re-parse: %v", err)
	}

	// Discord voice is UDP. Carrying it inside VLESS requires a packet encoding;
	// without one, voice is the single feature that would quietly fail while
	// everything else looked fine.
	for _, ob := range doc.Outbounds {
		if ob["tag"] == tagProxy {
			if ob["packet_encoding"] != "xudp" {
				t.Fatalf("proxy outbound must set packet_encoding for UDP voice, got %v", ob["packet_encoding"])
			}
			return
		}
	}
	t.Fatal("no proxy outbound found")
}
