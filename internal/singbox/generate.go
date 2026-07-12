// Package singbox translates a Discord Tunnel configuration into a sing-box
// configuration.
//
// Keeping this translation in one place is deliberate. sing-box's schema is a
// moving target across minor releases; the user's config.json is not. When
// sing-box changes, only this file changes.
package singbox

import (
	"encoding/json"
	"fmt"

	"github.com/Darguus/discord-tunnel/internal/config"
)

// tunAddress is the /30 assigned to the virtual adapter. It is private, small,
// and unlikely to collide with a home LAN or a Docker bridge.
const tunAddress = "172.19.0.1/30"

const (
	tagProxy  = "proxy"
	tagDirect = "direct"
	tagTUN    = "tun-in"
	tagHealth = "health-in"

	dnsProxy = "dns-proxy"
	dnsLocal = "dns-local"
)

// Generate renders the sing-box JSON document for cfg.
//
// The routing story, in the order sing-box evaluates it:
//
//  1. The TUN adapter captures all traffic on the machine. This is the only way
//     Windows lets us see a process's UDP, which is what Discord voice is made
//     of and what a Chromium --proxy-server flag can never reach.
//  2. DNS queries are hijacked so they cannot leak to the ISP resolver.
//  3. Traffic from the tunnelled processes (and the health probe) is sent to the
//     VLESS outbound.
//  4. Everything else falls through to `direct` and leaves via the real
//     interface, unchanged. That is the split: capture everything, forward only
//     what was asked for.
func Generate(cfg config.Config, logPath string) ([]byte, error) {
	doc := map[string]any{
		"log":       logSection(cfg, logPath),
		"dns":       dnsSection(cfg),
		"inbounds":  inbounds(cfg),
		"outbounds": outbounds(cfg),
		"route":     routeSection(cfg),
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode sing-box config: %w", err)
	}
	return raw, nil
}

func logSection(cfg config.Config, logPath string) map[string]any {
	return map[string]any{
		"level":     cfg.Tunnel.LogLevel,
		"output":    logPath,
		"timestamp": true,
	}
}

// dnsSection resolves the tunnelled processes' names through the server, over
// DNS-over-TLS, and everything else through the system resolver.
//
// This matters as much as the traffic itself: if Discord's names were resolved
// locally, a censoring resolver could hand back a poisoned address and the
// tunnel would faithfully carry us to it.
func dnsSection(cfg config.Config) map[string]any {
	rules := []map[string]any{}
	if len(cfg.Tunnel.Processes) > 0 {
		rules = append(rules, map[string]any{
			"process_name": cfg.Tunnel.Processes,
			"server":       dnsProxy,
		})
	}
	if len(cfg.Tunnel.Domains) > 0 {
		rules = append(rules, map[string]any{
			"domain_suffix": cfg.Tunnel.Domains,
			"server":        dnsProxy,
		})
	}
	rules = append(rules, map[string]any{
		"inbound": []string{tagHealth},
		"server":  dnsProxy,
	})

	return map[string]any{
		"servers": []map[string]any{
			// Resolved inside the tunnel, so the query is invisible to the ISP.
			{"tag": dnsProxy, "type": "tls", "server": "1.1.1.1", "detour": tagProxy},
			// The machine's own resolver, for everything not being tunnelled.
			{"tag": dnsLocal, "type": "local"},
		},
		"rules": rules,
		"final": dnsLocal,
		// Discord is reachable over IPv4 everywhere; asking for AAAA records on a
		// v4-only tunnel just adds a round-trip that always fails.
		"strategy":       "ipv4_only",
		"disable_cache":  false,
		"cache_capacity": 4096,
	}
}

func inbounds(cfg config.Config) []map[string]any {
	return []map[string]any{
		{
			"type":         "tun",
			"tag":          tagTUN,
			"address":      []string{tunAddress},
			"mtu":          cfg.Tunnel.MTU,
			"auto_route":   true,
			"strict_route": true,
			// gVisor is a userspace network stack: no kernel driver to install
			// beyond wintun, and it cannot blue-screen the machine on a bad packet.
			"stack": "gvisor",
		},
		{
			// Not for Discord — Discord is captured by the TUN above. This exists
			// so the app can measure real latency through the proxy path, which it
			// otherwise could not: its own traffic does not match a process rule.
			"type":        "socks",
			"tag":         tagHealth,
			"listen":      "127.0.0.1",
			"listen_port": config.HealthProxyPort,
		},
	}
}

func outbounds(cfg config.Config) []map[string]any {
	proxy := map[string]any{
		"type":            "vless",
		"tag":             tagProxy,
		"server":          cfg.Server.Address,
		"server_port":     cfg.Server.Port,
		"uuid":            cfg.Server.UUID,
		"packet_encoding": "xudp",
		"tls": map[string]any{
			"enabled":     true,
			"server_name": cfg.Server.Reality.ServerName,
			"utls": map[string]any{
				"enabled": true,
				// The whole point of REALITY is that our handshake is
				// byte-indistinguishable from a real browser's. That is what this
				// fingerprint provides; do not "simplify" it away.
				"fingerprint": cfg.Server.Reality.Fingerprint,
			},
			"reality": map[string]any{
				"enabled":    true,
				"public_key": cfg.Server.Reality.PublicKey,
				"short_id":   cfg.Server.Reality.ShortID,
			},
		},
	}
	if cfg.Server.Flow != "" {
		proxy["flow"] = cfg.Server.Flow
	}

	return []map[string]any{
		proxy,
		{"type": "direct", "tag": tagDirect},
	}
}

func routeSection(cfg config.Config) map[string]any {
	rules := []map[string]any{
		// Read the protocol off the first packets so domain rules can match, and
		// so the logs name a host instead of a bare IP.
		{"action": "sniff"},
		// Answer DNS ourselves rather than letting queries escape to the ISP.
		{"protocol": "dns", "action": "hijack-dns"},
	}
	if len(cfg.Tunnel.Processes) > 0 {
		rules = append(rules, map[string]any{
			"process_name": cfg.Tunnel.Processes,
			"outbound":     tagProxy,
		})
	}
	if len(cfg.Tunnel.Domains) > 0 {
		rules = append(rules, map[string]any{
			"domain_suffix": cfg.Tunnel.Domains,
			"outbound":      tagProxy,
		})
	}
	rules = append(rules, map[string]any{
		"inbound":  []string{tagHealth},
		"outbound": tagProxy,
	})

	return map[string]any{
		"rules": rules,
		// Anything that matched no rule leaves the machine normally. This one line
		// is what makes the VPN a split tunnel rather than a whole-machine one.
		"final":                   tagDirect,
		"auto_detect_interface":   true,
		"default_domain_resolver": map[string]any{"server": dnsLocal},
	}
}
