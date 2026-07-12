package winenv

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// taskName is the Scheduled Task that starts the tunnel at logon.
const taskName = "DiscordTunnel"

// Autostart is implemented as a Scheduled Task, not as an HKCU\...\Run entry.
//
// The app needs administrator rights to create the network adapter. A Run key
// entry for an elevated program produces a UAC prompt on every single logon,
// which is precisely the papercut this project exists to remove. A scheduled
// task registered with the highest privileges starts silently instead.

// AutostartEnabled reports whether the logon task exists.
func AutostartEnabled() bool {
	cmd := exec.Command("schtasks.exe", "/Query", "/TN", taskName)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run() == nil
}

// SetAutostart creates or removes the logon task. It requires elevation.
func SetAutostart(enabled bool) error {
	if !enabled {
		return removeAutostart()
	}
	return addAutostart()
}

func addAutostart() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate own executable: %w", err)
	}

	// /RL HIGHEST is what buys the silent elevation; /F overwrites a stale task
	// left behind by an earlier install at a different path.
	cmd := exec.Command("schtasks.exe",
		"/Create",
		"/TN", taskName,
		"/TR", `"`+exe+`" --minimized`,
		"/SC", "ONLOGON",
		"/RL", "HIGHEST",
		"/F",
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("register logon task: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func removeAutostart() error {
	cmd := exec.Command("schtasks.exe", "/Delete", "/TN", taskName, "/F")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	out, err := cmd.CombinedOutput()
	if err != nil {
		// Deleting a task that was never created is the desired end state, not a
		// failure worth showing the user.
		if strings.Contains(strings.ToLower(string(out)), "cannot find") {
			return nil
		}
		return fmt.Errorf("remove logon task: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
