// Package smoke proves that the tunnel does what it claims, on the machine it
// was installed on, before the user finds out otherwise in the middle of a call.
//
// The trick that makes an honest test possible: for the duration of the check,
// the tunnel is configured to capture *this* program instead of Discord. Its
// packets then travel the exact path Discord's will — same adapter, same
// routing rules, same VLESS outbound — so anything measured here is a
// measurement of the real thing, not of a lookalike.
package smoke

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Darguus/discord-tunnel/internal/config"
	"github.com/Darguus/discord-tunnel/internal/tunnel"

	"golang.org/x/net/proxy"
)

// checkTimeout bounds a single reachability check so a black-holed route fails
// the check instead of hanging it.
const checkTimeout = 10 * time.Second

// Run executes the checks and reports whether all of them passed.
func Run(cfg config.Config) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate own executable: %w", err)
	}

	probeCfg := cfg
	probeCfg.Tunnel.Processes = []string{filepath.Base(exe)}
	probeCfg.Tunnel.Domains = nil

	fmt.Printf("Bringing the tunnel up (server %s:%d)...\n\n", cfg.Server.Address, cfg.Server.Port)

	manager := tunnel.New(nil)
	if err := manager.Start(probeCfg); err != nil {
		return fmt.Errorf("the tunnel would not start: %w", err)
	}
	defer manager.Stop()

	// The adapter exists the moment Start returns, but Windows needs a moment to
	// finish installing the routes before traffic actually flows through it.
	time.Sleep(2 * time.Second)

	allPassed := true

	// Part 1 — TCP: can Discord's signalling hosts be reached through the tunnel?
	// These go through the loopback SOCKS inbound, which has no direct fallback,
	// so a success here cannot be an accidental leak: the bytes went through the
	// server or not at all.
	fmt.Println("TCP (chat, login, gateway):")
	tcpChecks := []struct{ name, host string }{
		{"Discord gateway", "gateway.discord.gg:443"},
		{"Discord API", "discord.com:443"},
		{"Discord CDN", "cdn.discordapp.com:443"},
	}
	for _, c := range tcpChecks {
		lat, err := dialThroughTunnel(c.host)
		if err != nil {
			fmt.Printf("  [FAIL] %-20s %v\n", c.name, err)
			allPassed = false
			continue
		}
		fmt.Printf("  [ OK ] %-20s %d ms\n", c.name, lat.Milliseconds())
	}

	// Part 2 — UDP: the check that actually matters. Discord voice is UDP, and a
	// UDP-blocking network is the whole reason this app exists. We ask a STUN
	// server what public address our UDP came from; if the tunnel is carrying
	// voice, that address is the server's, not the ISP's.
	fmt.Println("\nUDP (voice — the reason this app exists):")
	if err := checkVoicePath(cfg); err != nil {
		fmt.Printf("  [FAIL] %v\n", err)
		allPassed = false
	}

	fmt.Println()
	if !allPassed {
		return fmt.Errorf("some checks failed — see above")
	}
	fmt.Println("All checks passed. Discord — including voice — travels through your server.")
	return nil
}

// stunServers are queried in order until one answers. Two independent operators
// so a single outage does not fail the check.
var stunServers = []string{
	"stun.l.google.com:19302",
	"stun.cloudflare.com:3478",
}

// checkVoicePath proves UDP voice traverses the tunnel by confirming the public
// address our UDP exits from is the server's, not this machine's ISP.
func checkVoicePath(cfg config.Config) error {
	var mapped net.IP
	var lastErr error
	for _, s := range stunServers {
		ip, err := discoverPublicUDPAddr(s, checkTimeout)
		if err == nil {
			mapped = ip
			break
		}
		lastErr = err
	}
	if mapped == nil {
		return fmt.Errorf("UDP is not getting through at all (voice would not work): %v", lastErr)
	}

	serverIP := net.ParseIP(cfg.Server.Address)
	switch {
	case serverIP != nil && mapped.Equal(serverIP):
		// The exit address is the server's: UDP is being carried by the tunnel.
		fmt.Printf("  [ OK ] UDP exits via your server (%s) — voice will work\n", mapped)
		return nil
	case serverIP != nil:
		// UDP got out, but not via the server. That is a leak: voice packets are
		// escaping the tunnel and would be dropped by a UDP-blocking network.
		return fmt.Errorf("UDP is LEAKING: exits via %s, but your server is %s — voice is not tunnelled", mapped, serverIP)
	default:
		// The server is configured by hostname, so we cannot compare exactly.
		// Report the address for the user to eyeball against their server's IP.
		fmt.Printf("  [ OK ] UDP exits via %s — confirm this is your server's public IP\n", mapped)
		return nil
	}
}

// dialThroughTunnel connects to host via the loopback SOCKS inbound, which is
// the one path guaranteed to be inside the tunnel regardless of which process
// is doing the dialling.
func dialThroughTunnel(host string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(config.HealthProxyPort))
	dialer, err := proxy.SOCKS5("tcp", addr, nil, proxy.Direct)
	if err != nil {
		return 0, err
	}
	ctxDialer := dialer.(proxy.ContextDialer)

	start := time.Now()
	conn, err := ctxDialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return 0, err
	}
	elapsed := time.Since(start)
	_ = conn.Close()
	return elapsed, nil
}
