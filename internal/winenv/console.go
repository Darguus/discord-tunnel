package winenv

import (
	"os"

	"golang.org/x/sys/windows"
)

// x/sys/windows does not wrap AttachConsole, so it is bound here by hand.
var (
	kernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procAttachConsole = kernel32.NewProc("AttachConsole")
)

// attachParentProcess is ATTACH_PARENT_PROCESS: "the console of the process
// that launched me", if it had one.
const attachParentProcess = ^uintptr(0) // (DWORD)-1

// AttachConsole reconnects stdout and stderr to the terminal that launched the
// app, if there is one.
//
// The binary is linked as a GUI application so that double-clicking it does not
// flash a black console window — it belongs in the tray, not on the taskbar.
// The cost of that is that a GUI process starts with no stdio at all, so
// `DiscordTunnel.exe --check` from a terminal would print into the void.
// Attaching to the parent console gives the command-line flags their output
// back, without giving the tray a window it never wanted.
func AttachConsole() {
	ret, _, _ := procAttachConsole.Call(attachParentProcess)
	if ret == 0 {
		// No parent console: the app was double-clicked. There is nothing to
		// attach to and nothing to fix.
		return
	}
	if handle, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE); err == nil {
		os.Stdout = os.NewFile(uintptr(handle), "stdout")
	}
	if handle, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE); err == nil {
		os.Stderr = os.NewFile(uintptr(handle), "stderr")
	}
}
