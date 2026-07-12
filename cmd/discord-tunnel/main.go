// Command discord-tunnel routes a single application through your own server,
// and leaves the rest of the machine alone.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/Darguus/discord-tunnel/internal/config"
	"github.com/Darguus/discord-tunnel/internal/smoke"
	"github.com/Darguus/discord-tunnel/internal/tray"
	"github.com/Darguus/discord-tunnel/internal/winenv"
)

func main() {
	// Linked as a GUI binary so the tray icon does not drag a console window
	// around with it; this hands stdio back to a terminal when there is one.
	winenv.AttachConsole()

	var (
		importXray = flag.String("import-xray", "", "build config.json from an existing Xray client config and exit")
		check      = flag.Bool("check", false, "validate the configuration and exit")
		smokeTest  = flag.Bool("smoke", false, "bring the tunnel up, verify Discord is reachable through it, and exit")
		_          = flag.Bool("minimized", false, "start without showing anything (used by the logon task)")
	)
	flag.Parse()

	if *importXray != "" {
		if err := runImport(*importXray); err != nil {
			exitWithError(err)
		}
		return
	}

	cfg, err := config.Load()
	if err != nil {
		var notConfigured *config.NotConfiguredError
		if errors.As(err, &notConfigured) {
			if err := writeStarterConfig(notConfigured.Path); err != nil {
				exitWithError(err)
			}
			return
		}
		exitWithError(err)
	}

	if *check {
		fmt.Println("configuration is valid")
		fmt.Printf("  server:    %s:%d\n", cfg.Server.Address, cfg.Server.Port)
		fmt.Printf("  tunnelled: %v\n", cfg.Tunnel.Processes)
		return
	}

	// The smoke test needs both elevation and a console to print to. Relaunching
	// through UAC would give it the rights but lose the console, so instead it
	// asks to be run from an administrator terminal, where its output survives.
	if *smokeTest {
		if !winenv.IsElevated() {
			exitWithError(errors.New("run --smoke from an administrator terminal (it needs to create the network adapter)"))
		}
		if err := smoke.Run(cfg); err != nil {
			exitWithError(err)
		}
		return
	}

	// Creating the virtual adapter and rewriting the routing table are privileged
	// operations. Ask for the rights once, up front, instead of failing later
	// inside a driver call with an error nobody can act on.
	if !winenv.IsElevated() {
		if err := winenv.RelaunchElevated(); err != nil {
			if errors.Is(err, winenv.ErrElevationDeclined) {
				exitWithError(errors.New("administrator rights are required to create the network adapter"))
			}
			exitWithError(err)
		}
		return
	}

	tray.Run(cfg)
}

func runImport(path string) error {
	cfg, err := config.ImportXray(path)
	if err != nil {
		return err
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	dest, _ := config.Path()
	fmt.Printf("imported %s:%d into %s\n", cfg.Server.Address, cfg.Server.Port, dest)
	fmt.Printf("tunnelling: %v\n", cfg.Tunnel.Processes)
	return nil
}

// writeStarterConfig handles the first run: rather than showing an error about
// a file that does not exist, write the file and say what to fill in.
func writeStarterConfig(path string) error {
	if err := config.Save(config.Default()); err != nil {
		return err
	}
	fmt.Printf("A starter configuration was written to:\n  %s\n\n", path)
	fmt.Println("Fill in the server section with your VLESS/REALITY details, then run this again.")
	fmt.Println("Migrating from Xray or Amnezia? Import the existing client config instead:")
	fmt.Println("  DiscordTunnel.exe --import-xray path\\to\\xray\\config.json")
	return nil
}

func exitWithError(err error) {
	fmt.Fprintln(os.Stderr, "discord-tunnel:", err)
	os.Exit(1)
}
