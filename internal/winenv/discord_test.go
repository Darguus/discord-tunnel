package winenv

import (
	"os"
	"path/filepath"
	"testing"
)

func writeBuild(t *testing.T, root, build string) {
	t.Helper()
	dir := filepath.Join(root, build)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Discord.exe"), []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVersionOrderingIsNumericNotLexical(t *testing.T) {
	// The bug this guards: a string sort puts "app-1.0.983" above
	// "app-1.0.9245" because '9' > '2' at the fifth character. A real user hit
	// exactly this — the stale build won and the app launched a copy that
	// instantly exited.
	if compareVersionParts(versionParts("app-1.0.9245"), versionParts("app-1.0.983")) <= 0 {
		t.Error("1.0.9245 must rank above 1.0.983")
	}
	if compareVersionParts(versionParts("app-1.0.9156"), versionParts("app-1.0.9245")) >= 0 {
		t.Error("1.0.9156 must rank below 1.0.9245")
	}
}

func TestNewestBuildPicksHighestWithinRoot(t *testing.T) {
	root := t.TempDir()
	writeBuild(t, root, "app-1.0.9156")
	writeBuild(t, root, "app-1.0.9245")
	writeBuild(t, root, "app-1.0.9243")

	exe, version, err := newestBuild(root)
	if err != nil {
		t.Fatalf("newestBuild: %v", err)
	}
	if filepath.Base(filepath.Dir(exe)) != "app-1.0.9245" {
		t.Errorf("picked %s, want app-1.0.9245", filepath.Dir(exe))
	}
	if compareVersionParts(version, versionParts("app-1.0.9245")) != 0 {
		t.Errorf("returned version %v, want that of 1.0.9245", version)
	}
}

func TestGlobalNewestBeatsFirstRoot(t *testing.T) {
	// Mirrors the real machine: a stale build in the first-searched root and the
	// live, newer build in a second root. The newer one must win regardless of
	// search order — the old first-root-wins logic launched the stale copy.
	stale := t.TempDir()
	live := t.TempDir()
	writeBuild(t, stale, "app-1.0.9156") // e.g. %LOCALAPPDATA%\Discord
	writeBuild(t, live, "app-1.0.9245")  // e.g. C:\ProgramData\Darguus\Discord

	// newestBuild each, then compare as FindDiscord does.
	staleExe, staleVer, err := newestBuild(stale)
	if err != nil {
		t.Fatal(err)
	}
	liveExe, liveVer, err := newestBuild(live)
	if err != nil {
		t.Fatal(err)
	}
	_ = staleExe
	if compareVersionParts(liveVer, staleVer) <= 0 {
		t.Fatalf("live build %v should outrank stale %v", liveVer, staleVer)
	}
	if filepath.Base(filepath.Dir(liveExe)) != "app-1.0.9245" {
		t.Errorf("live exe resolved wrong: %s", liveExe)
	}
}
