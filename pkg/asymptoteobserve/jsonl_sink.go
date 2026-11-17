package asymptoteobserve

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// ErrSinkPathRequired is returned when a JSONL sink has no output path.
var ErrSinkPathRequired = errors.New("sink path is required")

// JSONLSink writes sanitized canonical Beacon events to an owner-only local JSONL file.
type JSONLSink struct {
	path string
	mu   sync.Mutex
}

// NewJSONLSink creates a local, non-rotating JSONL sink for canonical
// Beacon events. Newly created parent directories use mode 0700, and new or
// pre-existing destination files are kept at mode 0600. It performs no network I/O.
func NewJSONLSink(path string) *JSONLSink {
	return &JSONLSink{path: path}
}

func (s *JSONLSink) WriteBatch(ctx context.Context, events []Event) (err error) {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if s.path == "" {
		return ErrSinkPathRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			if err != nil {
				err = errors.Join(err, cerr)
			} else {
				err = cerr
			}
		}
	}()
	// Creation modes do not affect an existing file. Tighten it before appending
	// so telemetry is never written while broader filesystem permissions remain.
	if err := f.Chmod(0o600); err != nil {
		return err
	}

	for _, event := range events {
		line, err := json.Marshal(prepareEventForSink(event, DefaultMaxEventBytes))
		if err != nil {
			return err
		}
		line, err = FitEventLine(line, DefaultMaxEventBytes, nil)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func (s *JSONLSink) Flush(context.Context) error {
	return nil
}

func (s *JSONLSink) Close() error {
	return nil
}
