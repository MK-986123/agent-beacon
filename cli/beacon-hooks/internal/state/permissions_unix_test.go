//go:build unix

package state

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/asymptote-labs/agent-beacon/cli/beacon-hooks/internal/config"
)

func TestStateAndLockArePrivateAndRemainUsable(t *testing.T) {
	dir, cleanup := setupTestStateDir(t)
	defer cleanup()
	oldUmask := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(oldUmask) })

	state := NewSessionState("session-private", "cursor")
	if err := state.SetPromptEmitted(); err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(dir, "state.json"), filepath.Join(dir, "state.json.lock")}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != config.HookFileMode {
			t.Fatalf("%s mode = %o, want %o", path, got, config.HookFileMode)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if !state.HasPromptEmitted() {
		t.Fatal("private state no longer preserved the prompt marker")
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != config.HookFileMode {
			t.Fatalf("legacy %s mode = %o after reuse, want %o", path, got, config.HookFileMode)
		}
	}
}
