// Package tray is the whole user interface: an icon, a colour, and a short
// menu. There is no window, because the tunnel has nothing to say most of the
// time — it should be a thing you stop thinking about.
package tray

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/Darguus/discord-tunnel/internal/config"
	"github.com/Darguus/discord-tunnel/internal/tunnel"
	"github.com/Darguus/discord-tunnel/internal/winenv"

	"github.com/getlantern/systray"
)

var (
	//go:embed assets/off.ico
	iconOff []byte
	//go:embed assets/connecting.ico
	iconConnecting []byte
	//go:embed assets/on.ico
	iconOn []byte
	//go:embed assets/error.ico
	iconError []byte
)

// App wires the tunnel manager to the tray menu.
type App struct {
	cfg     config.Config
	manager *tunnel.Manager

	mConnect   *systray.MenuItem
	mStatus    *systray.MenuItem
	mDiscord   *systray.MenuItem
	mAutostart *systray.MenuItem
	mLog       *systray.MenuItem
	mConfig    *systray.MenuItem
	mQuit      *systray.MenuItem

	// status arrives from the supervisor goroutine; the tray may only be touched
	// from its own event loop, so transitions are funnelled through this channel.
	statusCh chan tunnel.Status
}

// Run takes over the calling goroutine and returns when the user quits.
func Run(cfg config.Config) {
	app := &App{
		cfg:      cfg,
		statusCh: make(chan tunnel.Status, 8),
	}
	app.manager = tunnel.New(func(s tunnel.Status) {
		// Never block the supervisor on a UI that is busy: a dropped redraw is
		// harmless, a stalled watchdog is not.
		select {
		case app.statusCh <- s:
		default:
		}
	})
	systray.Run(app.onReady, app.onExit)
}

func (a *App) onReady() {
	systray.SetIcon(iconOff)
	systray.SetTitle("Discord Tunnel")
	systray.SetTooltip("Discord Tunnel — off")

	a.mConnect = systray.AddMenuItem("Connect", "Bring the tunnel up")
	a.mStatus = systray.AddMenuItem("Off", "Current state")
	a.mStatus.Disable()
	systray.AddSeparator()

	a.mDiscord = systray.AddMenuItem("Launch Discord", "Start Discord")
	systray.AddSeparator()

	a.mAutostart = systray.AddMenuItemCheckbox("Start with Windows", "Connect automatically at logon", winenv.AutostartEnabled())
	a.mLog = systray.AddMenuItem("Open log", "Show the tunnel log")
	a.mConfig = systray.AddMenuItem("Open config", "Edit config.json")
	systray.AddSeparator()
	a.mQuit = systray.AddMenuItem("Quit", "Stop the tunnel and exit")

	go a.loop()

	if a.cfg.App.ConnectOnLaunch {
		go a.connect()
	}
}

func (a *App) loop() {
	for {
		select {
		case s := <-a.statusCh:
			a.render(s)

		case <-a.mConnect.ClickedCh:
			if a.manager.Running() {
				go a.disconnect()
			} else {
				go a.connect()
			}

		case <-a.mDiscord.ClickedCh:
			go a.launchDiscord()

		case <-a.mAutostart.ClickedCh:
			go a.toggleAutostart()

		case <-a.mLog.ClickedCh:
			if path, err := config.LogPath(); err == nil {
				openInShell(path)
			}

		case <-a.mConfig.ClickedCh:
			if path, err := config.Path(); err == nil {
				openInShell(path)
			}

		case <-a.mQuit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

func (a *App) connect() {
	if err := a.manager.Start(a.cfg); err != nil {
		// Start already published StateError; the supervisor is not running, so
		// nothing else will report this.
		return
	}
}

func (a *App) disconnect() {
	a.manager.Stop()
}

func (a *App) launchDiscord() {
	if winenv.DiscordRunning() {
		return
	}
	if err := winenv.LaunchDiscord(); err != nil {
		a.mStatus.SetTitle("Discord not found")
	}
}

func (a *App) toggleAutostart() {
	enable := !a.mAutostart.Checked()
	if err := winenv.SetAutostart(enable); err != nil {
		return
	}
	if enable {
		a.mAutostart.Check()
	} else {
		a.mAutostart.Uncheck()
	}
}

// render maps a tunnel status onto the icon, tooltip and menu.
func (a *App) render(s tunnel.Status) {
	switch s.State {
	case tunnel.StateUp:
		systray.SetIcon(iconOn)
		label := fmt.Sprintf("Connected — %d ms", s.Latency.Milliseconds())
		a.mStatus.SetTitle(label)
		systray.SetTooltip("Discord Tunnel — " + label)
		a.mConnect.SetTitle("Disconnect")

	case tunnel.StateConnecting:
		systray.SetIcon(iconConnecting)
		label := "Connecting…"
		if s.Err != nil {
			// A drop the watchdog is already working through. Say so, rather than
			// showing a spinner that implies a first connection.
			label = "Reconnecting…"
		}
		a.mStatus.SetTitle(label)
		systray.SetTooltip("Discord Tunnel — " + label)
		a.mConnect.SetTitle("Disconnect")

	case tunnel.StateError:
		systray.SetIcon(iconError)
		a.mStatus.SetTitle(truncate(errText(s.Err), 60))
		systray.SetTooltip("Discord Tunnel — error: " + truncate(errText(s.Err), 80))
		a.mConnect.SetTitle("Connect")

	default:
		systray.SetIcon(iconOff)
		a.mStatus.SetTitle("Off")
		systray.SetTooltip("Discord Tunnel — off")
		a.mConnect.SetTitle("Connect")
	}
}

func (a *App) onExit() {
	// Leaving a virtual adapter and a rewritten routing table behind would take
	// the machine's networking down with us.
	done := make(chan struct{})
	go func() {
		a.manager.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}
}

func errText(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func openInShell(path string) {
	if _, err := os.Stat(path); err != nil {
		return
	}
	cmd := exec.Command("explorer.exe", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Run()
}
