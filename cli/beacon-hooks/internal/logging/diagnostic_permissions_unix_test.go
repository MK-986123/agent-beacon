//go:build unix

package logging

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/asymptote-labs/agent-beacon/cli/beacon-hooks/internal/config"
)

func TestDiagnosticLogsArePrivateAndRemainWritable(t *testing.T) {
	home := t.TempDir()
	oldBeaconDir, oldCursorDir := config.BeaconDir, config.CursorDir
	config.BeaconDir = filepath.Join(home, ".beacon")
	config.CursorDir = filepath.Join(config.BeaconDir, "cursor")
	t.Cleanup(func() { config.BeaconDir, config.CursorDir = oldBeaconDir, oldCursorDir })
	oldUmask := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(oldUmask) })

	platform := NewLoggerForPlatform("pre-tool", "cursor")
	platform.Info("platform diagnostic", "tool_name", "Write")
	session := NewSessionLogger("post-tool", "cursor", "session-1")
	session.Info("session diagnostic", "file_path", "/project/private.go")

	for _, path := range []string{
		config.GetLogFile("cursor"),
		config.GetSessionLogFile("cursor", "session-1"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != config.HookFileMode {
			t.Fatalf("%s mode = %o, want %o", path, got, config.HookFileMode)
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 {
			t.Fatalf("diagnostic %s was not preserved: %q, %v", path, data, err)
		}
	}
}
