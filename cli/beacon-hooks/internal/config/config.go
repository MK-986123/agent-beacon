package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// HookDirMode and HookFileMode keep per-user hook diagnostics and state private. They are
	// exported so every hook writer uses the same contract.
	HookDirMode  os.FileMode = 0o700
	HookFileMode os.FileMode = 0o600
)

// Directories
var (
	BeaconDir      = getBeaconDir()
	ClaudeDir      = filepath.Join(BeaconDir, "claude")
	AntigravityDir = filepath.Join(BeaconDir, "antigravity")
	CopilotDir     = filepath.Join(BeaconDir, "copilot")
	CursorDir      = filepath.Join(BeaconDir, "cursor")
	VSCodeDir      = filepath.Join(BeaconDir, "vscode")
	DevinDir       = filepath.Join(BeaconDir, "devin")
	FactoryDir     = filepath.Join(BeaconDir, "factory")
	GrokDir        = filepath.Join(BeaconDir, "grok")
	HermesDir      = filepath.Join(BeaconDir, "hermes")
	OpenCodeDir    = filepath.Join(BeaconDir, "opencode")
	QwenDir        = filepath.Join(BeaconDir, "qwen")
	MuseDir        = filepath.Join(BeaconDir, "muse")
	OpenHandsDir   = filepath.Join(BeaconDir, "openhands")
	KiroDir        = filepath.Join(BeaconDir, "kiro")
	DshDir         = filepath.Join(BeaconDir, "dsh")
	KimiDir        = filepath.Join(BeaconDir, "kimi")
	ClineDir       = filepath.Join(BeaconDir, "cline")
)

// Log rotation
const (
	LogMaxSizeBytes   = 10 * 1024 * 1024 // 10 MB
	LogRotateArchives = 5
)

// Scannable extensions
var scannableExtensions = map[string]bool{
	// JavaScript/TypeScript
	".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".mjs": true, ".cjs": true,
	// Python
	".py": true, ".pyw": true,
	// Java/Kotlin
	".java": true, ".kt": true, ".kts": true,
	// Go
	".go": true,
	// Rust
	".rs": true,
	// C/C++
	".c": true, ".h": true, ".cpp": true, ".cc": true, ".cxx": true, ".hpp": true, ".hxx": true,
	// C#
	".cs": true,
	// Ruby
	".rb": true,
	// PHP
	".php": true,
	// Swift
	".swift": true,
	// Solidity
	".sol": true,
	// Shell
	".sh": true, ".bash": true, ".zsh": true,
	// SQL
	".sql": true,
	// YAML (for IaC)
	".yaml": true, ".yml": true,
}

func getBeaconDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ".beacon"
	}
	return filepath.Join(homeDir, ".beacon")
}

// IsScannableFile checks if a file should be scanned based on its extension
func IsScannableFile(filePath string) bool {
	if filePath == "" {
		return false
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	return scannableExtensions[ext]
}

// GetStateDir returns the state directory for the given platform
func GetStateDir(platform string) string {
	switch platform {
	case "antigravity":
		return AntigravityDir
	case "copilot":
		return CopilotDir
	case "cursor":
		return CursorDir
	case "vscode":
		return VSCodeDir
	case "devin", "devin-cli", "devin-desktop":
		return DevinDir
	case "factory":
		return FactoryDir
	case "grok":
		return GrokDir
	case "hermes":
		return HermesDir
	case "opencode":
		return OpenCodeDir
	case "qwen":
		return QwenDir
	case "muse":
		return MuseDir
	case "openhands":
		return OpenHandsDir
	case "kiro":
		return KiroDir
	case "dsh":
		return DshDir
	case "kimi":
		return KimiDir
	case "cline":
		return ClineDir
	default:
		return ClaudeDir
	}
}

// GetLogFile returns the log file path for the given platform
func GetLogFile(platform string) string {
	return filepath.Join(GetStateDir(platform), "hooks.log")
}

// GetSessionLogDir returns the session logs directory for the given platform
func GetSessionLogDir(platform string) string {
	return filepath.Join(GetStateDir(platform), "logs")
}

// GetSessionLogFile returns the per-session log file path
func GetSessionLogFile(platform, sessionID string) string {
	return filepath.Join(GetSessionLogDir(platform), sessionID+".log")
}

// EnsureSessionLogDir creates the session logs directory if it doesn't exist
func EnsureSessionLogDir(platform string) error {
	if err := EnsureStateDir(platform); err != nil {
		return err
	}
	if err := mkdirInHome(GetSessionLogDir(platform)); err != nil {
		return err
	}
	settleSessionLogs(GetSessionLogDir(platform))
	return nil
}

// RotateLogIfNeededForPlatform rotates the platform-specific log file if it exceeds LogMaxSizeBytes.
func RotateLogIfNeededForPlatform(platform string) bool {
	if err := EnsureStateDir(platform); err != nil {
		return false
	}
	logFile := GetLogFile(platform)
	f, err := OpenPrivateFile(logFile, os.O_RDONLY)
	if err != nil {
		return false
	}
	info, err := f.Stat()
	_ = f.Close()
	if err != nil {
		return false
	}

	if info.Size() > LogMaxSizeBytes {
		return rotateLog(logFile, LogRotateArchives) == nil
	}

	return false
}

func rotateLog(path string, archives int) error {
	if archives < 1 {
		archives = LogRotateArchives
	}
	if err := removeOverflowArchives(path, archives); err != nil {
		return err
	}
	for i := archives - 1; i >= 1; i-- {
		from := path + fmt.Sprintf(".%d", i)
		to := path + fmt.Sprintf(".%d", i+1)
		if err := os.Rename(from, to); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Rename(path, path+".1")
}

func removeOverflowArchives(path string, archives int) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	prefix := base + "."
	for _, entry := range entries {
		suffix, ok := strings.CutPrefix(entry.Name(), prefix)
		if !ok {
			continue
		}
		index, err := strconv.Atoi(suffix)
		if err != nil || index < archives {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// EnsureStateDir ensures the state directory for the given platform exists
func EnsureStateDir(platform string) error {
	dir := GetStateDir(platform)
	if err := mkdirInHome(dir); err != nil {
		return err
	}
	settlePlatformFiles(dir)
	return nil
}

// mkdirInHome creates dir, which lies under ~/.beacon, but never the home directory itself. A
// sandbox that assembles its own root filesystem, which the account may write, often leaves the
// home out; a home a hook created there would vanish with the sandbox, taking whatever was written
// into it, and would make the account's event spool look available when it is not. State in a
// home that does exist there -- the mount point of a project bound beneath it -- is kept: it is
// per-session dedupe state, and refusing it would record the session's prompt again on every
// tool call. The account spool and the log check OnSandboxRoot themselves.
func mkdirInHome(dir string) error {
	if _, err := os.Stat(filepath.Dir(BeaconDir)); err != nil {
		return err
	}
	for _, path := range privateDirChain(dir) {
		if err := ensurePrivateDir(path); err != nil {
			return err
		}
	}
	return nil
}

// privateDirChain returns every hook-owned directory from ~/.beacon through dir. Tests replace a
// platform directory with a temporary directory outside BeaconDir; in that case only that exact
// directory is managed.
func privateDirChain(dir string) []string {
	base := filepath.Clean(BeaconDir)
	target := filepath.Clean(dir)
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return []string{target}
	}
	paths := []string{base}
	if rel == "." {
		return paths
	}
	current := base
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		paths = append(paths, current)
	}
	return paths
}

// ensurePrivateDir creates or settles one hook-owned directory. Lstat and the descriptor identity
// check keep a symlink (including one exchanged during the open) from steering chmod elsewhere.
// Existing directories owned by another account are refused rather than changed or traversed.
func ensurePrivateDir(path string) error {
	if err := os.Mkdir(path, HookDirMode); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("hook state path %s is not a directory", path)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|privateOpenFlags(), 0)
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !opened.IsDir() || !os.SameFile(info, opened) {
		return fmt.Errorf("hook state directory %s changed while opening", path)
	}
	if !ownedByCurrentUser(opened) {
		return fmt.Errorf("hook state directory %s is not owned by the current user", path)
	}
	return f.Chmod(HookDirMode)
}

// OpenPrivateFile opens a hook diagnostic or state file without following its final path component,
// verifies that the opened descriptor still names an owner-controlled regular file, and settles it
// to 0600. A descriptor is used for chmod so a path swap cannot redirect the permission change.
func OpenPrivateFile(path string, flag int) (*os.File, error) {
	named, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil && (!named.Mode().IsRegular() || !ownedByCurrentUser(named) || linkedElsewhere(named)) {
		return nil, fmt.Errorf("hook state file %s is not a private regular file", path)
	}

	f, err := os.OpenFile(path, flag|privateOpenFlags(), HookFileMode)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !ownedByCurrentUser(opened) || linkedElsewhere(opened) {
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("hook state file %s is not an owner-controlled regular file", path)
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(current, opened) {
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("hook state file %s changed while opening", path)
	}
	if err := f.Chmod(HookFileMode); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// ReadPrivateFile reads a hook state file through the same no-link, owner-only boundary used by
// writers.
func ReadPrivateFile(path string) ([]byte, error) {
	f, err := OpenPrivateFile(path, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func settlePrivateFile(path string) {
	f, err := OpenPrivateFile(path, os.O_RDONLY)
	if err == nil {
		_ = f.Close()
	}
}

// settlePlatformFiles tightens files left by older hook releases. Unsafe or foreign entries are
// left untouched; the normal open path will refuse them if a hook later tries to use one.
func settlePlatformFiles(dir string) {
	for _, name := range []string{"hooks.log", "state.json", "state.json.lock"} {
		settlePrivateFile(filepath.Join(dir, name))
	}
	entries, err := os.ReadDir(dir)
	if err == nil {
		const prefix = "hooks.log."
		for _, entry := range entries {
			suffix, ok := strings.CutPrefix(entry.Name(), prefix)
			if !ok {
				continue
			}
			if index, err := strconv.Atoi(suffix); err == nil && index > 0 {
				settlePrivateFile(filepath.Join(dir, entry.Name()))
			}
		}
	}
	settleSessionLogs(filepath.Join(dir, "logs"))
}

func settleSessionLogs(dir string) {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return
	}
	if err != nil || !info.IsDir() || ensurePrivateDir(dir) != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".log") {
			settlePrivateFile(filepath.Join(dir, entry.Name()))
		}
	}
}
