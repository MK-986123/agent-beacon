package asymptoteobserve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestJSONLSinkCreatesOwnerOnlyStorage(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "private", "nested")
	path := filepath.Join(parent, "events.jsonl")
	sink := NewJSONLSink(path)
	event := NewEvent(NewEventOptions{Action: "tool.invoked", Harness: HarnessInfo{Name: "cursor"}})

	if err := sink.WriteBatch(context.Background(), []Event{event}); err != nil {
		t.Fatalf("WriteBatch returned error: %v", err)
	}
	if os.PathSeparator != '/' {
		return
	}
	for _, check := range []struct {
		path string
		mode os.FileMode
	}{
		{filepath.Join(root, "private"), 0o700},
		{parent, 0o700},
		{path, 0o600},
	} {
		info, err := os.Stat(check.path)
		if err != nil {
			t.Fatalf("stat %s: %v", check.path, err)
		}
		if got := info.Mode().Perm(); got != check.mode {
			t.Fatalf("mode of %s = %o, want %o", check.path, got, check.mode)
		}
	}
}

func TestJSONLSinkTightensExistingFileBeforeAppending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	existing := []byte("{\"existing\":true}\n")
	if err := os.WriteFile(path, existing, 0o644); err != nil {
		t.Fatalf("write existing JSONL: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("set existing JSONL mode: %v", err)
	}

	sink := NewJSONLSink(path)
	event := NewEvent(NewEventOptions{Action: "tool.invoked", Harness: HarnessInfo{Name: "cursor"}})
	if err := sink.WriteBatch(context.Background(), []Event{event}); err != nil {
		t.Fatalf("WriteBatch returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read JSONL: %v", err)
	}
	if !bytes.HasPrefix(data, existing) {
		t.Fatalf("existing JSONL was not preserved: %q", data)
	}
	if os.PathSeparator == '/' {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat JSONL: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("existing JSONL mode = %o, want 600", got)
		}
	}
}

func TestJSONLSinkRedactsSecretsAtPersistenceBoundary(t *testing.T) {
	const secret = "visible-without-privacy"
	path := filepath.Join(t.TempDir(), "events.jsonl")
	sink := NewJSONLSink(path)
	event := NewEvent(NewEventOptions{
		Action:  "command.executed",
		Harness: HarnessInfo{Name: "cursor"},
		Message: "token=" + secret,
	})
	event.Raw = map[string]interface{}{
		"authorization": "Bearer " + secret,
		"task":          "summarize this",
	}

	if err := sink.WriteBatch(context.Background(), []Event{event}); err != nil {
		t.Fatalf("WriteBatch returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read JSONL: %v", err)
	}
	if bytes.Contains(data, []byte(secret)) {
		t.Fatalf("JSONL contains unredacted secret: %s", data)
	}
	var written Event
	if err := json.Unmarshal(bytes.TrimSpace(data), &written); err != nil {
		t.Fatalf("unmarshal JSONL: %v", err)
	}
	if written.Event.Action != event.Event.Action || written.Raw["task"] != "summarize this" {
		t.Fatalf("legitimate event data changed: %#v", written)
	}
}

func TestJSONLSinkFitsFinalRecordSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	sink := NewJSONLSink(path)
	event := NewEvent(NewEventOptions{Action: "tool.invoked", Harness: HarnessInfo{Name: "cursor"}})
	values := make([]interface{}, 40)
	for i := range values {
		values[i] = strings.Repeat("x", DefaultRawStringLimit)
	}
	event.Raw = map[string]interface{}{"values": values}

	if err := sink.WriteBatch(context.Background(), []Event{event}); err != nil {
		t.Fatalf("WriteBatch returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read JSONL: %v", err)
	}
	if got := bytes.Count(data, []byte{'\n'}); got != 1 {
		t.Fatalf("JSONL newline count = %d, want 1", got)
	}
	line := bytes.TrimSuffix(data, []byte{'\n'})
	if len(line) > DefaultMaxEventBytes {
		t.Fatalf("JSONL line = %d bytes, want at most %d", len(line), DefaultMaxEventBytes)
	}
	var written Event
	if err := json.Unmarshal(line, &written); err != nil {
		t.Fatalf("unmarshal JSONL: %v", err)
	}
	if written.Event.Action != event.Event.Action || !written.Truncated {
		t.Fatalf("fitted event lost identity or truncation marker: %#v", written)
	}
}

func TestJSONLSinkWritesCanonicalEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "events.jsonl")
	sink := NewJSONLSink(path)

	events := []Event{
		NewEvent(NewEventOptions{Action: "tool.invoked", Harness: HarnessInfo{Name: "cursor"}}),
		NewEvent(NewEventOptions{Action: "command.executed", Harness: HarnessInfo{Name: "codex"}}),
	}
	if err := sink.WriteBatch(context.Background(), events); err != nil {
		t.Fatalf("WriteBatch returned error: %v", err)
	}

	lines := readJSONLLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	for _, line := range lines {
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("line is not canonical event JSON: %v line=%q", err, line)
		}
		if event.Vendor != Vendor || event.Product != Product || event.SchemaVersion != SchemaVersion {
			t.Fatalf("unexpected event identity: %#v", event)
		}
	}
}

func TestJSONLSinkRejectsDirectoryPath(t *testing.T) {
	sink := NewJSONLSink(t.TempDir())
	if err := sink.WriteBatch(context.Background(), []Event{NewEvent(NewEventOptions{Action: "tool.invoked", Harness: HarnessInfo{Name: "cursor"}})}); err == nil {
		t.Fatal("WriteBatch returned nil for directory path")
	}
}

func TestJSONLSinkRejectsEmptyPath(t *testing.T) {
	sink := NewJSONLSink("")
	err := sink.WriteBatch(context.Background(), []Event{NewEvent(NewEventOptions{Action: "tool.invoked", Harness: HarnessInfo{Name: "cursor"}})})
	if !errors.Is(err, ErrSinkPathRequired) {
		t.Fatalf("WriteBatch error = %v, want ErrSinkPathRequired", err)
	}
}

func TestJSONLSinkSerializesConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	sink := NewJSONLSink(path)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := sink.WriteBatch(context.Background(), []Event{NewEvent(NewEventOptions{Action: "tool.invoked", Harness: HarnessInfo{Name: "cursor"}})})
			if err != nil {
				t.Errorf("WriteBatch returned error: %v", err)
			}
		}()
	}
	wg.Wait()

	lines := readJSONLLines(t, path)
	if len(lines) != 20 {
		t.Fatalf("lines = %d, want 20", len(lines))
	}
	for _, line := range lines {
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("line is not JSON: %v line=%q", err, line)
		}
	}
}

func readJSONLLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open JSONL: %v", err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan JSONL: %v", err)
	}
	return lines
}
