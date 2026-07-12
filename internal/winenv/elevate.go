// Package winenv holds the Windows-specific plumbing the tunnel needs:
// elevation, autostart and locating Discord.
package winenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// IsElevated reports whether this process can create a network adapter.
//
// Creating a wintun adapter and rewriting the routing table are privileged
// operations. Rather than let sing-box fail deep inside a driver call with an
// opaque error, the app checks up front and asks for elevation once.
func IsElevated() bool {
	var token windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

// RelaunchElevated restarts this executable through the UAC prompt, passing the
// original arguments along, and reports whether the new process was launched.
//
// The caller is expected to exit immediately on success: two copies of the app
// would fight over the same adapter.
func RelaunchElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate own executable: %w", err)
	}

	verb, err := syscall.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	args, err := syscall.UTF16PtrFromString(quoteArgs(os.Args[1:]))
	if err != nil {
		return err
	}
	cwd, err := syscall.UTF16PtrFromString(filepath.Dir(exe))
	if err != nil {
		return err
	}

	err = windows.ShellExecute(0, verb, file, args, cwd, windows.SW_NORMAL)
	if err != nil {
		// The user clicking "No" on the UAC prompt is a decision, not a crash.
		if err == windows.ERROR_CANCELLED {
			return ErrElevationDeclined
		}
		return fmt.Errorf("request elevation: %w", err)
	}
	return nil
}

// ErrElevationDeclined means the user dismissed the UAC prompt.
var ErrElevationDeclined = fmt.Errorf("elevation was declined")

func quoteArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		if strings.ContainsAny(a, ` "`) {
			a = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		}
		quoted = append(quoted, a)
	}
	return strings.Join(quoted, " ")
}
