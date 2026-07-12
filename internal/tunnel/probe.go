package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Darguus/discord-tunnel/internal/config"

	"golang.org/x/net/proxy"
)

// probeTarget is what we dial to decide whether the tunnel is alive.
//
// It is a Discord host on purpose. "Can I reach 1.1.1.1?" answers a question
// nobody asked; the only thing that matters is whether Discord is reachable
// through this server right now.
const probeTarget = "gateway.discord.gg:443"

const probeTimeout = 8 * time.Second

// Probe measures the round-trip to Discord through the tunnel and reports the
// latency of a full TCP handshake.
//
// It dials via the loopback SOCKS inbound rather than directly, because a
// direct dial from this process would not match the process rules and would
// therefore leave the machine untunnelled — measuring nothing at all.
func Probe(ctx context.Context) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(config.HealthProxyPort))
	dialer, err := proxy.SOCKS5("tcp", addr, nil, proxy.Direct)
	if err != nil {
		return 0, fmt.Errorf("create probe dialer: %w", err)
	}
	ctxDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return 0, errors.New("probe dialer does not support cancellation")
	}

	start := time.Now()
	conn, err := ctxDialer.DialContext(ctx, "tcp", probeTarget)
	if err != nil {
		return 0, fmt.Errorf("tunnel is not carrying traffic: %w", err)
	}
	latency := time.Since(start)
	_ = conn.Close()
	return latency, nil
}

// translateStartError turns sing-box's internal failures into something a user
// can act on. The two that actually happen in the field are a missing wintun
// driver and a non-elevated process, and both produce errors that explain
// nothing to someone who just wants Discord to work.
func translateStartError(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())

	switch {
	case strings.Contains(msg, "wintun"):
		return fmt.Errorf("wintun.dll is missing or unloadable — it must sit next to DiscordTunnel.exe (%w)", err)
	case strings.Contains(msg, "access is denied"), strings.Contains(msg, "permission denied"):
		return fmt.Errorf("administrator rights are required to create the network adapter (%w)", err)
	case strings.Contains(msg, "reality"), strings.Contains(msg, "handshake"):
		return fmt.Errorf("the server rejected the REALITY handshake — check uuid, public_key and short_id against the server (%w)", err)
	}
	return err
}
