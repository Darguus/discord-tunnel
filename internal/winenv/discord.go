package winenv

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// FindDiscord returns the path of the newest installed Discord build, or an
// error naming the places that were searched.
//
// Discord installs itself as a stack of versioned app-x.y.z directories and
// leaves the old ones in place, so "the newest app-* directory that contains a
// Discord.exe" is the only reliable way to find the live one.
//
// The build is chosen globally, across every root, not per-root. A machine can
// carry a stale copy in one location and the live one in another — picking the
// first root that happens to contain any build would launch the stale copy,
// which quietly exits on start and looks for all the world like a broken app.
func FindDiscord() (string, error) {
	var (
		bestExe     string
		bestVersion []int
		searched    []string
	)
	for _, root := range discordRoots() {
		searched = append(searched, root)
		exe, version, err := newestBuild(root)
		if err != nil {
			continue
		}
		if bestExe == "" || compareVersionParts(version, bestVersion) > 0 {
			bestExe, bestVersion = exe, version
		}
	}
	if bestExe == "" {
		return "", fmt.Errorf("no Discord installation found in: %s", strings.Join(searched, ", "))
	}
	return bestExe, nil
}

// discordRoots lists the directories Discord is known to install into: the
// standard per-user location, the machine-wide one, and whatever DISCORD_HOME
// points at for a relocated install.
func discordRoots() []string {
	var roots []string
	if custom := os.Getenv("DISCORD_HOME"); custom != "" {
		roots = append(roots, custom)
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		for _, flavour := range []string{"Discord", "DiscordCanary", "DiscordPTB"} {
			roots = append(roots, filepath.Join(local, flavour))
		}
	}
	if programData := os.Getenv("ProgramData"); programData != "" {
		roots = append(roots, filepath.Join(programData, "Discord"))
		// Some installs relocate under a per-user folder, e.g.
		// C:\ProgramData\<name>\Discord. Scan one level down so a relocated
		// install is found without the user having to set DISCORD_HOME.
		if entries, err := os.ReadDir(programData); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					roots = append(roots, filepath.Join(programData, e.Name(), "Discord"))
				}
			}
		}
	}
	return roots
}

// newestBuild picks the highest-versioned app-* directory holding a Discord.exe
// and returns its path together with the parsed version, so callers can compare
// builds across different roots.
func newestBuild(root string) (string, []int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", nil, err
	}

	var (
		bestName    string
		bestVersion []int
	)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(strings.ToLower(e.Name()), "app-") {
			continue
		}
		exe := filepath.Join(root, e.Name(), "Discord.exe")
		if _, err := os.Stat(exe); err != nil {
			continue
		}
		v := versionParts(e.Name())
		if bestName == "" || compareVersionParts(v, bestVersion) > 0 {
			bestName, bestVersion = e.Name(), v
		}
	}
	if bestName == "" {
		// Some installs keep the executable at the root, with no app-* layer.
		exe := filepath.Join(root, "Discord.exe")
		if _, err := os.Stat(exe); err == nil {
			return exe, nil, nil
		}
		return "", nil, fmt.Errorf("no Discord build under %s", root)
	}
	return filepath.Join(root, bestName, "Discord.exe"), bestVersion, nil
}

// compareVersionParts orders 1.0.9195 above 1.0.983 numerically, which a plain
// string sort gets wrong. A nil (rootless) version sorts below any real one.
func compareVersionParts(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

func versionParts(name string) []int {
	name = strings.TrimPrefix(strings.ToLower(name), "app-")
	var parts []int
	for _, field := range strings.Split(name, ".") {
		n := 0
		ok := len(field) > 0
		for _, ch := range field {
			if ch < '0' || ch > '9' {
				ok = false
				break
			}
			n = n*10 + int(ch-'0')
		}
		if !ok {
			return parts
		}
		parts = append(parts, n)
	}
	return parts
}

// LaunchDiscord starts Discord directly, with --multi-instance.
//
// --multi-instance is not optional, learned the hard way: launching the inner
// app-*/Discord.exe without it makes the process hand off and exit immediately,
// leaving no window — indistinguishable from a broken install. The flag keeps
// this instance alive instead of deferring to a single-instance lock.
//
// We do NOT pass --proxy-server as the old setup did: traffic is captured at the
// TUN adapter now, so Discord needs no proxy awareness of its own.
//
// Caveat: this app runs elevated to manage the adapter, so Discord inherits
// administrator integrity. It works, but drag-and-drop from a normal Explorer
// window into Discord will not. Launching Discord de-elevated (by duplicating
// the shell's token) is tracked as a follow-up; explorer.exe cannot be used for
// it because it does not forward --multi-instance to the target.
func LaunchDiscord() error {
	exe, err := FindDiscord()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "--multi-instance")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start Discord: %w", err)
	}
	// Do not Wait: Discord is a long-lived GUI app, not a subprocess we manage.
	return nil
}

// DiscordRunning reports whether a Discord process already exists, so the tray
// does not start a second copy.
func DiscordRunning() bool {
	cmd := exec.Command("tasklist.exe", "/FI", "IMAGENAME eq Discord.exe", "/NH")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "discord.exe")
}
