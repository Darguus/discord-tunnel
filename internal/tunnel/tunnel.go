// Package tunnel owns the lifecycle of the sing-box instance: bringing the
// virtual adapter up, tearing it down, and keeping it up without the user
// having to notice that it ever went down.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Darguus/discord-tunnel/internal/config"
	"github.com/Darguus/discord-tunnel/internal/singbox"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
)

// State is the tunnel's externally visible condition.
type State int

const (
	// StateDown means no virtual adapter exists and nothing is being tunnelled.
	StateDown State = iota
	// StateConnecting means the adapter is coming up or the watchdog is retrying.
	StateConnecting
	// StateUp means traffic from the configured processes is flowing through the
	// server, confirmed by a probe rather than assumed from a successful start.
	StateUp
	// StateError means the tunnel could not be brought up and will not retry
	// until the user acts (a bad config, or no administrator rights).
	StateError
)

func (s State) String() string {
	switch s {
	case StateDown:
		return "off"
	case StateConnecting:
		return "connecting"
	case StateUp:
		return "connected"
	case StateError:
		return "error"
	}
	return "unknown"
}

// Status is a snapshot of the tunnel, safe to read from the UI goroutine.
type Status struct {
	State State
	// Latency is the round-trip time to Discord measured *through* the tunnel.
	// Zero when not connected.
	Latency time.Duration
	// Err is the reason for StateError, or the last transient failure the
	// watchdog is retrying through.
	Err error
	// Since is when the tunnel last entered StateUp.
	Since time.Time
}

// Manager starts, stops and supervises one sing-box instance.
//
// Exactly one instance may run at a time: two would fight over the routing
// table. Every transition goes through the mutex, so an impatient user
// double-clicking Connect cannot produce two adapters.
type Manager struct {
	mu       sync.Mutex
	instance *box.Box
	cancel   context.CancelFunc

	// supervisor is the goroutine that re-dials after a drop; stopping the
	// tunnel cancels it, so it cannot resurrect a tunnel the user turned off.
	supervisorStop context.CancelFunc
	supervisorDone chan struct{}

	statusMu sync.RWMutex
	status   Status

	onChange func(Status)
}

// New returns an idle Manager. onChange is invoked on every status transition,
// from a background goroutine — the callback must not block.
func New(onChange func(Status)) *Manager {
	if onChange == nil {
		onChange = func(Status) {}
	}
	return &Manager{onChange: onChange}
}

// Status returns the current snapshot.
func (m *Manager) Status() Status {
	m.statusMu.RLock()
	defer m.statusMu.RUnlock()
	return m.status
}

func (m *Manager) setStatus(s Status) {
	m.statusMu.Lock()
	m.status = s
	m.statusMu.Unlock()
	m.onChange(s)
}

// Start brings the tunnel up and keeps it up: after a successful start, a
// supervisor goroutine probes the proxy path and rebuilds the instance if it
// stops answering. It returns once the first attempt has succeeded or failed.
func (m *Manager) Start(cfg config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.instance != nil {
		return errors.New("tunnel is already running")
	}

	m.setStatus(Status{State: StateConnecting})
	if err := m.startInstanceLocked(cfg); err != nil {
		m.setStatus(Status{State: StateError, Err: err})
		return err
	}

	supCtx, supCancel := context.WithCancel(context.Background())
	m.supervisorStop = supCancel
	m.supervisorDone = make(chan struct{})
	go m.supervise(supCtx, cfg, m.supervisorDone)

	return nil
}

// startInstanceLocked builds and starts a sing-box instance. Caller holds m.mu.
func (m *Manager) startInstanceLocked(cfg config.Config) error {
	logPath, err := config.LogPath()
	if err != nil {
		return err
	}
	raw, err := singbox.Generate(cfg, logPath)
	if err != nil {
		return err
	}

	// include.Context registers every protocol sing-box knows about; the option
	// decoder is context-driven and cannot parse an outbound it has not been told
	// exists.
	ctx := include.Context(context.Background())
	options, err := json.UnmarshalExtendedContext[option.Options](ctx, raw)
	if err != nil {
		return fmt.Errorf("build tunnel config: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	instance, err := box.New(box.Options{Context: ctx, Options: options})
	if err != nil {
		cancel()
		return fmt.Errorf("create tunnel: %w", translateStartError(err))
	}
	if err := instance.Start(); err != nil {
		cancel()
		_ = instance.Close()
		return fmt.Errorf("start tunnel: %w", translateStartError(err))
	}

	m.instance = instance
	m.cancel = cancel
	return nil
}

// stopInstanceLocked tears the adapter down. Caller holds m.mu.
func (m *Manager) stopInstanceLocked() {
	if m.instance == nil {
		return
	}
	// Cancel first: it unblocks anything still dialling, so Close does not sit
	// waiting on a connection to a server that is no longer reachable.
	m.cancel()
	_ = m.instance.Close()
	m.instance = nil
	m.cancel = nil
}

// Stop tears the tunnel down and stops the supervisor. It is safe to call when
// the tunnel is already down.
func (m *Manager) Stop() {
	m.mu.Lock()
	stop, done := m.supervisorStop, m.supervisorDone
	m.supervisorStop, m.supervisorDone = nil, nil
	m.mu.Unlock()

	// Stop the supervisor before killing the instance, or it will observe the
	// dead tunnel and helpfully bring it back up.
	if stop != nil {
		stop()
		<-done
	}

	m.mu.Lock()
	m.stopInstanceLocked()
	m.mu.Unlock()

	m.setStatus(Status{State: StateDown})
}

// Running reports whether an instance currently exists.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.instance != nil
}

const (
	probeInterval = 15 * time.Second
	// A tunnel is only declared dead after this many consecutive failed probes.
	// A single failure is usually a Wi-Fi hiccup, and rebuilding the adapter for
	// one of those would be worse than the problem.
	failuresBeforeRestart = 3
)

// supervise probes the proxy path and rebuilds the instance when it goes quiet.
func (m *Manager) supervise(ctx context.Context, cfg config.Config, done chan<- struct{}) {
	defer close(done)

	// Confirm the tunnel actually carries traffic before claiming it is up.
	// box.Start() only means the adapter exists, not that the server answers.
	if lat, err := Probe(ctx); err == nil {
		m.setStatus(Status{State: StateUp, Latency: lat, Since: time.Now()})
	} else if ctx.Err() == nil {
		m.setStatus(Status{State: StateConnecting, Err: err})
	}

	failures := 0
	ticker := time.NewTicker(probeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		lat, err := Probe(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			failures = 0
			prev := m.Status()
			since := prev.Since
			if prev.State != StateUp {
				since = time.Now()
			}
			m.setStatus(Status{State: StateUp, Latency: lat, Since: since})
			continue
		}

		failures++
		if failures < failuresBeforeRestart {
			continue
		}

		m.setStatus(Status{State: StateConnecting, Err: err})
		if err := m.rebuild(ctx, cfg); err != nil {
			if ctx.Err() != nil {
				return
			}
			m.setStatus(Status{State: StateConnecting, Err: err})
			continue
		}
		failures = 0
	}
}

// rebuild replaces a wedged instance with a fresh one.
func (m *Manager) rebuild(ctx context.Context, cfg config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	m.stopInstanceLocked()

	// Let Windows finish removing the adapter before we ask for a new one;
	// creating it while the old one is still being torn down fails.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Second):
	}
	return m.startInstanceLocked(cfg)
}
