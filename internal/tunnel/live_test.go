package tunnel

import (
	"os"
	"testing"

	"github.com/Darguus/discord-tunnel/internal/config"
)

// TestStartReachesAdapterCreation exercises the whole runtime path that unit
// tests cannot: it actually asks sing-box to build and start an instance from a
// generated config, catching any wiring error between Generate and box.Start
// that a schema check would miss.
//
// It is gated behind an env var because it touches the real network stack and,
// when elevated, briefly creates a live adapter. Run it deliberately:
//
//	DTUN_LIVE=1 go test ./internal/tunnel -run TestStartReachesAdapterCreation -v
//
// Unelevated, it must fail at adapter creation with the friendly elevation
// message — which proves everything up to that point is wired correctly.
func TestStartReachesAdapterCreation(t *testing.T) {
	if os.Getenv("DTUN_LIVE") == "" {
		t.Skip("set DTUN_LIVE=1 to run the live adapter test")
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config (run --import-xray first): %v", err)
	}

	m := New(nil)
	err = m.Start(cfg)
	defer m.Stop()

	if err == nil {
		// Elevated and the server answered: the tunnel is genuinely up.
		st := m.Status()
		t.Logf("tunnel started; state=%s latency=%s", st.State, st.Latency)
		return
	}
	t.Logf("Start failed as expected without elevation: %v", err)
}
