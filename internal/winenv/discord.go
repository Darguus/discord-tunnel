package winenv

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// FindDiscord returns the path of the newest installed Discord build, or an
// error naming the places that were searched.
//
// Discord installs itself as a stack of versioned app-x.y.z directories and
// leaves the old ones in place, so "the newest app-* directory that contains a
// Discord.exe" is the only reliable way to find the live one.
func FindDiscord() (string, error) {
	var searched []string
	for _, root := range discordRoots() {
		searched = append(searched, root)
		exe, err := newestBuild(root)
		if err == nil {
			return exe, nil
		}
	}
	return "", fmt.Errorf("no Discord installation found in: %s", strings.Join(searched, ", "))
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
	}
	return roots
}

// newestBuild picks the highest-versioned app-* directory holding a Discord.exe.
func newestBuild(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}

	var builds []string
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(strings.ToLower(e.Name()), "app-") {
			continue
		}
		exe := filepath.Join(root, e.Name(), "Discord.exe")
		if _, err := os.Stat(exe); err == nil {
			builds = append(builds, e.Name())
		}
	}
	if len(builds) == 0 {
		// Some installs keep the executable at the root, with no app-* layer.
		exe := filepath.Join(root, "Discord.exe")
		if _, err := os.Stat(exe); err == nil {
			return exe, nil
		}
		return "", fmt.Errorf("no Discord build under %s", root)
	}

	sort.Slice(builds, func(i, j int) bool {
		return compareVersions(builds[i], builds[j]) < 0
	})
	newest := builds[len(builds)-1]
	return filepath.Join(root, newest, "Discord.exe"), nil
}

// compareVersions orders app-1.0.9195 style names numerically, so that
// app-1.0.9195 sorts above app-1.0.983 — which a plain string sort gets wrong.
func compareVersions(a, b string) int {
	pa := versionParts(a)
	pb := versionParts(b)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(pa) < len(pb):
		return -1
	case len(pa) > len(pb):
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

// LaunchDiscord starts Discord with no command-line flags at all.
//
// The flags are gone on purpose. The old setup had to pass --proxy-server and
// --multi-instance and skip the updater; with the traffic captured at the
// adapter, Discord is just an ordinary program again.
//
// It is launched through explorer.exe rather than started as a child process,
// because this app runs elevated and a child would inherit that. Discord
// running as administrator breaks drag-and-drop from Explorer and hands a chat
// client privileges it has no business holding. explorer.exe is already running
// as the logged-in user, so anything it starts lands back at normal integrity.
func LaunchDiscord() error {
	exe, err := FindDiscord()
	if err != nil {
		return err
	}
	cmd := exec.Command("explorer.exe", exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	// explorer.exe hands the request to the running shell and exits non-zero even
	// on success, so its exit code says nothing about whether Discord started.
	_ = cmd.Run()
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
