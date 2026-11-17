// Package sink holds pipeline sinks.
package sink

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"

	"github.com/asymptote-labs/agent-beacon/gateway/pipeline"
)

// file is what JSONL writes to: an *os.File opened for appending.
type file interface {
	io.Writer
	io.ReaderAt
	Stat() (os.FileInfo, error)
	Truncate(size int64) error
	Close() error
}

// JSONL appends each record's value as one JSON line. A record that does not marshal is skipped
// and reported; the rest of the batch is still written.
//
// A batch is written whole or not at all. When writing it fails (a full disk, a quota, an I/O
// error), the file is cut back to the size it had when the batch started, read from the file
// itself, so it never holds half a line, and the next batch starts with a fresh buffer:
// bufio.Writer keeps its first error forever, so reusing it would fail every later batch even
// once the disk has room again. The size is never remembered across batches, so a file rotated
// by copytruncate in between is cut back to its new size, never padded to its old one. The file
// must be this output's own: another writer's lines appended during a failed batch would be cut
// with it.
type JSONL struct {
	f              file
	w              *bufio.Writer
	path           string
	rotateBytes    int64
	rotateArchives int
	// unfinished reports that a failed batch could not be cut back, so the file may end in part
	// of a line, which the next batch cuts off before writing.
	unfinished bool
	// writeFrom is where what has not been handed to the disk starts (releasePages): the file's
	// pages before it are dropped from memory as they are written out.
	writeFrom int64
}

// releaseEvery is how much is written between page-cache releases. The kernel keeps a file's
// written pages in memory, charged to the container, until it runs short; written steadily, they
// filled a sidecar's memory limit and kept it there, with the kernel reclaiming on every
// allocation. Released every 8 MiB, the output holds about two of those in memory.
const (
	releaseEvery = 8 << 20

	// DefaultRotateBytes is the target size at which a JSONL file rotates before its next batch.
	DefaultRotateBytes int64 = 10 << 20
	// DefaultRotateArchives is how many completed JSONL files are retained beside the live file.
	DefaultRotateArchives = asymptoteobserve.DefaultRuntimeLogRotateArchives
)

// rotateJSONLFile is the repository's shared, gap-recovering log rotation implementation. It is
// replaceable in tests so a failed rotation can be checked without relying on filesystem faults.
var rotateJSONLFile = asymptoteobserve.RotateFilePreservingMode

// OpenJSONL opens path for appending, creating it with owner-only permissions: records carry
// prompts and tool output. It uses bounded retention defaults; OpenJSONLWithRotation customizes
// the target size and archive count.
func OpenJSONL(path string) (*JSONL, error) {
	return OpenJSONLWithRotation(path, DefaultRotateBytes, DefaultRotateArchives)
}

// OpenJSONLWithRotation opens a JSONL sink that rotates before a non-empty file plus the next
// batch would exceed rotateBytes, retaining rotateArchives completed files. A batch is never split
// across files, so one batch larger than rotateBytes remains intact in one file.
func OpenJSONLWithRotation(path string, rotateBytes int64, rotateArchives int) (*JSONL, error) {
	if rotateBytes <= 0 {
		return nil, fmt.Errorf("rotate_bytes must be positive")
	}
	if rotateArchives <= 0 {
		return nil, fmt.Errorf("rotate_archives must be positive")
	}
	s, err := openJSONL(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	s.path = path
	s.rotateBytes = rotateBytes
	s.rotateArchives = rotateArchives
	return s, nil
}

func openJSONL(path string) (*JSONL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	s, err := newJSONL(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

// newJSONL writes to f after its last complete line: a line a crash left unfinished is cut off,
// so the first record written is not joined to it.
func newJSONL(f file) (*JSONL, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	end, err := lastLineEnd(f, info.Size())
	if err != nil {
		return nil, err
	}
	s := &JSONL{f: f, w: bufio.NewWriterSize(f, 64<<10)}
	if end != info.Size() {
		if err := f.Truncate(end); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// lastLineEnd is the offset just past the last newline among f's first size bytes, read backwards
// in chunks; 0 when there is none.
func lastLineEnd(f io.ReaderAt, size int64) (int64, error) {
	var buf [32 << 10]byte
	for hi := size; hi > 0; {
		lo := max(hi-int64(len(buf)), 0)
		chunk := buf[:hi-lo]
		if _, err := f.ReadAt(chunk, lo); err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if i := bytes.LastIndexByte(chunk, '\n'); i >= 0 {
			return lo + int64(i) + 1, nil
		}
		hi = lo
	}
	return 0, nil
}

// Write implements pipeline.Sink. A batch is flushed before Write returns, so a crash loses at
// most the batch in flight. A batch that cannot be written is cut back out of the file and
// reported as a pipeline.WriteError counting its records.
func (s *JSONL) Write(_ context.Context, batch []pipeline.Record) error {
	lines := make([][]byte, 0, len(batch))
	var skipped error
	var n int64
	for _, r := range batch {
		line, err := json.Marshal(r.Value)
		if err != nil {
			skipped = fmt.Errorf("marshal %s record: %w", r.Kind, err)
			continue
		}
		lines = append(lines, line)
		n += int64(len(line)) + 1
	}
	if s.f == nil {
		if err := s.reopen(); err != nil {
			return &pipeline.WriteError{Dropped: len(batch), Err: err}
		}
	}
	start, err := s.size()
	if err == nil && s.unfinished {
		if start, err = lastLineEnd(s.f, start); err == nil {
			err = s.f.Truncate(start)
		}
		s.unfinished = err != nil
	}
	if err != nil {
		return &pipeline.WriteError{Dropped: len(batch), Err: err}
	}
	if err := s.rotate(start, n); err != nil {
		return &pipeline.WriteError{Dropped: len(batch), Err: err}
	}
	if start, err = s.size(); err != nil {
		return &pipeline.WriteError{Dropped: len(batch), Err: err}
	}
	for _, line := range lines {
		s.w.Write(line)
		s.w.WriteByte('\n')
	}
	if err := s.w.Flush(); err != nil {
		s.w.Reset(s.f)
		s.unfinished = s.cutBack(start) != nil
		return &pipeline.WriteError{Dropped: len(batch), Err: err}
	}
	s.release(start + n)
	if skipped != nil {
		return &pipeline.WriteError{Dropped: len(batch) - len(lines), Err: skipped}
	}
	return nil
}

// rotate moves a complete live file into the bounded archive set before any byte of the next
// batch is written. On failure it reopens the unchanged live file and reports the whole batch
// dropped, so a persistent rotation failure cannot turn bounded retention back into disk growth.
func (s *JSONL) rotate(start, next int64) error {
	if s.path == "" || s.rotateBytes <= 0 || next == 0 || start == 0 ||
		(start <= s.rotateBytes && next <= s.rotateBytes-start) {
		return nil
	}
	if err := s.w.Flush(); err != nil {
		return err
	}
	if err := s.f.Close(); err != nil {
		return err
	}
	s.f, s.w = nil, nil
	rotateErr := rotateJSONLFile(s.path, s.rotateBytes, s.rotateArchives, next)
	openErr := s.reopen()
	if rotateErr != nil && openErr != nil {
		return errors.Join(rotateErr, fmt.Errorf("reopen %s: %w", s.path, openErr))
	}
	if openErr != nil {
		return fmt.Errorf("reopen %s: %w", s.path, openErr)
	}
	if rotateErr != nil {
		return fmt.Errorf("rotate %s: %w", s.path, rotateErr)
	}
	return nil
}

func (s *JSONL) reopen() error {
	fresh, err := openJSONL(s.path)
	if err != nil {
		return err
	}
	s.f, s.w = fresh.f, fresh.w
	s.unfinished, s.writeFrom = fresh.unfinished, fresh.writeFrom
	return nil
}

// release hands what was written since the last release to the disk once it reaches
// releaseEvery, without waiting, and drops from memory everything handed over before, which is on
// disk by now (what is still being written is dropped by a later release). end is the file's size
// after the batch; a file that shrank (rotated by copytruncate) starts over from its beginning.
func (s *JSONL) release(end int64) {
	if end < s.writeFrom {
		s.writeFrom = 0
	}
	if end-s.writeFrom < releaseEvery {
		return
	}
	releasePages(s.f, s.writeFrom, s.writeFrom, end-s.writeFrom)
	s.writeFrom = end
}

func (s *JSONL) size() (int64, error) {
	info, err := s.f.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// cutBack removes what a failed batch wrote after start, never extending the file: if it shrank
// below start meanwhile (rotated by copytruncate during the batch), it is cut back to its last
// complete line instead.
func (s *JSONL) cutBack(start int64) error {
	cur, err := s.size()
	if err != nil {
		return err
	}
	target := start
	if cur < start {
		if target, err = lastLineEnd(s.f, cur); err != nil {
			return err
		}
	}
	if cur == target {
		return nil
	}
	return s.f.Truncate(target)
}

// Close implements pipeline.Sink.
func (s *JSONL) Close() error {
	if s.f == nil {
		return nil
	}
	if err := s.w.Flush(); err != nil {
		s.f.Close()
		return err
	}
	return s.f.Close()
}
