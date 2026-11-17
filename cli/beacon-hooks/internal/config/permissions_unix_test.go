//go:build unix

package config

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func usePrivateTestDirs(t *testing.T) (home, stateDir string) {
	t.Helper()
	home = t.TempDir()
	oldBeaconDir, oldCursorDir := BeaconDir, CursorDir
	BeaconDir = filepath.Join(home, ".beacon")
	CursorDir = filepath.Join(BeaconDir, "cursor")
	t.Cleanup(func() { BeaconDir, CursorDir = oldBeaconDir, oldCursorDir })
	return home, CursorDir
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func TestHookDirectoriesArePrivateAndTightened(t *testing.T) {
	_, stateDir := usePrivateTestDirs(t)
	oldUmask := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(oldUmask) })

	if err := EnsureSessionLogDir("cursor"); err != nil {
		t.Fatal(err)
	}
	dirs := []string{BeaconDir, stateDir, filepath.Join(stateDir, "logs")}
	for _, dir := range dirs {
		assertMode(t, dir, HookDirMode)
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnsureSessionLogDir("cursor"); err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		assertMode(t, dir, HookDirMode)
	}
}

func TestLegacyHookFilesAreTightened(t *testing.T) {
	_, stateDir := usePrivateTestDirs(t)
	if err := EnsureSessionLogDir("cursor"); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		filepath.Join(stateDir, "hooks.log"),
		filepath.Join(stateDir, "hooks.log.1"),
		filepath.Join(stateDir, "state.json"),
		filepath.Join(stateDir, "state.json.lock"),
		filepath.Join(stateDir, "logs", "session.log"),
	}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("legacy\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnsureStateDir("cursor"); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		assertMode(t, path, HookFileMode)
	}
	if err := os.Truncate(filepath.Join(stateDir, "hooks.log"), LogMaxSizeBytes+1); err != nil {
		t.Fatal(err)
	}
	if !RotateLogIfNeededForPlatform("cursor") {
		t.Fatal("private hook log was not rotated")
	}
	assertMode(t, filepath.Join(stateDir, "hooks.log.1"), HookFileMode)
	assertMode(t, filepath.Join(stateDir, "hooks.log.2"), HookFileMode)
}

func TestHookStorageDoesNotFollowLinks(t *testing.T) {
	home, _ := usePrivateTestDirs(t)
	victimDir := filepath.Join(home, "victim-dir")
	if err := os.Mkdir(victimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(victimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victimDir, BeaconDir); err != nil {
		t.Fatal(err)
	}
	if err := EnsureStateDir("cursor"); err == nil {
		t.Fatal("EnsureStateDir followed a symlinked Beacon directory")
	}
	assertMode(t, victimDir, 0o755)

	if err := os.Remove(BeaconDir); err != nil {
		t.Fatal(err)
	}
	if err := EnsureStateDir("cursor"); err != nil {
		t.Fatal(err)
	}
	victimFile := filepath.Join(home, "victim-file")
	if err := os.WriteFile(victimFile, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(victimFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victimFile, GetLogFile("cursor")); err != nil {
		t.Fatal(err)
	}
	if f, err := OpenPrivateFile(GetLogFile("cursor"), os.O_WRONLY); err == nil {
		_ = f.Close()
		t.Fatal("OpenPrivateFile followed a symlinked hook log")
	}
	assertMode(t, victimFile, 0o644)
}

func TestHookStorageDoesNotChangeForeignOwnership(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to create a foreign-owned hook path")
	}
	_, stateDir := usePrivateTestDirs(t)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(stateDir, 54321, 54321); err != nil {
		t.Fatal(err)
	}
	if err := EnsureStateDir("cursor"); err == nil {
		t.Fatal("EnsureStateDir accepted a directory owned by another account")
	}
	assertMode(t, stateDir, 0o755)
}
