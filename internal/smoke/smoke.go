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

	checks := []struct {
		name string
		host string
	}{
		{"Discord gateway (websocket)", "gateway.discord.gg:443"},
		{"Discord API", "discord.com:443"},
		{"Discord voice region", "russell.discord.media:443"},
	}

	allPassed := true
	for _, c := range checks {
		lat, err := dialThroughTunnel(c.host)
		if err != nil {
			fmt.Printf("  [FAIL] %-28s %v\n", c.name, err)
			allPassed = false
			continue
		}
		fmt.Printf("  [ OK ] %-28s %d ms\n", c.name, lat.Milliseconds())
	}

	fmt.Println()
	if !allPassed {
		return fmt.Errorf("some checks failed — Discord may not work fully through the tunnel")
	}
	fmt.Println("All checks passed. Discord traffic reaches the server through the tunnel.")
	return nil
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
