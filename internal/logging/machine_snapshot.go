package logging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/mihari-proxy/mihari/internal/platform"
)

// SnapshotWindow is an immutable closed UTC interval; nil From means no lower bound.
type SnapshotWindow struct {
	From *time.Time
	To   time.Time
}

// SourceID identifies a fixed diagnostic stream, never a filesystem path.
type SourceID string

const (
	// DaemonSource contains Mihari daemon diagnostics.
	DaemonSource SourceID = "daemon"
	// MihomoSource contains captured core diagnostics.
	MihomoSource SourceID = "mihomo"
	// TUISource contains only the current user's terminal diagnostics.
	TUISource SourceID = "tui"
)

// SourceStats accounts for records as emitted, with one LF counted per payload.
type SourceStats struct {
	Source                                 SourceID
	Lines, SkippedInvalid, Redacted, Bytes int64
	Files                                  []string
	SHA256                                 string
}

// SourceReader emits one JSON object at a time; Finish requires successful EOF.
// Next and Finish are called serially by the consumer.
type SourceReader interface {
	Next(context.Context) ([]byte, bool, error)
	Finish(context.Context) (SourceStats, error)
	Close() error
}

// SnapshotSource opens a bounded prefix of one fixed log sequence.
type SnapshotSource interface {
	Open(context.Context, SnapshotWindow) (SourceReader, error)
}

// NamedSource binds an export input to its fixed archive entry.
type NamedSource struct {
	ID     SourceID
	Source SnapshotSource
}

// MachineSnapshotSource opens daemon and mihomo prefixes with no client path input.
type MachineSnapshotSource interface {
	Open(context.Context, SnapshotWindow) (SnapshotSet, error)
}

// SnapshotSet owns both source readers, consumed in daemon then mihomo order.
// Finish is required before an assembler publishes any output.
type SnapshotSet interface {
	Source(SourceID) (SourceReader, error)
	Finish(context.Context) error
	Close() error
}

// MachineSnapshotOptions contains daemon-owned storage and writer synchronization.
type MachineSnapshotOptions struct {
	PrivateFS        *platform.PrivateFS
	Paths            ExportPaths
	EnterRecordMutex func(context.Context, string) (func(), error)
	Redactor         *Redactor
}

type machineSnapshotSource struct{ options MachineSnapshotOptions }

// NewMachineSnapshotSource borrows the daemon's logging filesystem and redactor.
func NewMachineSnapshotSource(options MachineSnapshotOptions) MachineSnapshotSource {
	return &machineSnapshotSource{options: options}
}

func (s *machineSnapshotSource) Open(ctx context.Context, window SnapshotWindow) (SnapshotSet, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if window.From != nil {
		from := *window.From
		window.From = &from
		if from.After(window.To) {
			return nil, ErrInvalidExportRequest
		}
	}
	retained, releaseSecrets := s.options.Redactor.snapshot()
	set := &machineSnapshotSet{releaseSecrets: releaseSecrets}
	var scanned int64
	for _, source := range []struct {
		id   SourceID
		path string
	}{{DaemonSource, s.options.Paths.DaemonLog}, {MihomoSource, s.options.Paths.MihomoLog}} {
		lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		var release func()
		var err error
		if s.options.EnterRecordMutex != nil {
			release, err = s.options.EnterRecordMutex(lockCtx, source.path)
		}
		var handles []snapshotHandle
		if err == nil {
			handles, err = snapshotSource(lockCtx, s.options.PrivateFS, source.path, nil, nil, nil)
		}
		if release != nil {
			release()
		}
		cancel()
		if err != nil {
			return nil, errors.Join(err, set.Close())
		}
		reader := &machineSourceReader{handles: handles, window: window, redactor: retained, outputBytes: &set.outputBytes, digest: sha256.New(), stats: SourceStats{Source: source.id, Files: []string{}}}
		set.readers = append(set.readers, reader)
		if len(handles) > 10 {
			return nil, errors.Join(errSnapshotBudget, set.Close())
		}
		for _, handle := range handles {
			if handle.size < 0 || handle.size > (2<<30)-scanned {
				return nil, errors.Join(errSnapshotBudget, set.Close())
			}
			scanned += handle.size
			reader.stats.Files = append(reader.stats.Files, handle.name)
		}
	}
	return set, nil
}

var errSnapshotBudget = errors.New("machine snapshot budget exceeded")

type machineSnapshotSet struct {
	readers        []*machineSourceReader
	next           int
	outputBytes    int64
	closed         atomic.Bool
	closeOnce      sync.Once
	closeErr       error
	releaseSecrets func()
}

func (s *machineSnapshotSet) Source(id SourceID) (SourceReader, error) {
	if s.closed.Load() || s.next >= len(s.readers) || s.readers[s.next].stats.Source != id || (s.next > 0 && !s.readers[s.next-1].finished) {
		return nil, errors.New("invalid machine snapshot source order")
	}
	r := s.readers[s.next]
	s.next++
	return r, nil
}

func (s *machineSnapshotSet) Finish(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closed.Load() || s.next != 2 || len(s.readers) != 2 || !s.readers[0].finished || !s.readers[1].finished {
		return errors.New("machine snapshot is incomplete")
	}
	return nil
}

func (s *machineSnapshotSet) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		for _, reader := range s.readers {
			s.closeErr = errors.Join(s.closeErr, reader.Close())
		}
		if s.releaseSecrets != nil {
			s.releaseSecrets()
		}
	})
	return s.closeErr
}

type machineSourceReader struct {
	handles     []snapshotHandle
	window      SnapshotWindow
	redactor    *Redactor
	outputBytes *int64
	digest      hash.Hash
	stats       SourceStats
	index       int
	prefix      *io.LimitedReader
	lines       *boundedLineReader
	eof         bool
	finished    bool
	readErr     error
	closed      atomic.Bool
	closeOnce   sync.Once
	closeErr    error
}

func (r *machineSourceReader) Next(ctx context.Context) ([]byte, bool, error) {
	if r.closed.Load() {
		return nil, false, errors.New("machine snapshot source closed")
	}
	if r.readErr != nil {
		return nil, false, r.readErr
	}
	for {
		if err := ctx.Err(); err != nil {
			return r.fail(err)
		}
		if r.index == len(r.handles) {
			r.eof = true
			return nil, false, io.EOF
		}
		if r.lines == nil {
			handle := r.handles[r.index]
			r.prefix = &io.LimitedReader{R: handle.file, N: handle.size}
			r.lines = newBoundedLineReader(r.prefix)
		}
		line, present, err := r.lines.Next(ctx)
		if errors.Is(err, io.EOF) {
			if r.prefix.N != 0 {
				return r.fail(io.ErrUnexpectedEOF)
			}
			r.lines = nil
			r.index++
			continue
		}
		if err != nil {
			return r.fail(err)
		}
		if !present {
			continue
		}
		record, stamp, valid := decodeExportRecord(line)
		if !valid || !utf8.Valid(line) {
			r.stats.SkippedInvalid++
			continue
		}
		if stamp.After(r.window.To) || (r.window.From != nil && stamp.Before(*r.window.From)) {
			continue
		}
		clean, changed := r.redactor.Value(record)
		payload, err := json.Marshal(clean)
		if err != nil {
			return r.fail(err)
		}
		if len(payload) > MaxExportRecordBytes || int64(len(payload)+1) > (1<<30)-*r.outputBytes {
			return r.fail(errSnapshotBudget)
		}
		if err := ctx.Err(); err != nil {
			return r.fail(err)
		}
		// hash.Hash.Write cannot fail, and payload retains the exact sent encoding.
		_, _ = r.digest.Write(payload)
		_, _ = r.digest.Write([]byte{'\n'})
		r.stats.Lines++
		if changed {
			r.stats.Redacted++
		}
		r.stats.Bytes += int64(len(payload) + 1)
		*r.outputBytes += int64(len(payload) + 1)
		return payload, changed, nil
	}
}

func (r *machineSourceReader) fail(err error) ([]byte, bool, error) {
	r.readErr = err
	return nil, false, err
}

func (r *machineSourceReader) Finish(ctx context.Context) (SourceStats, error) {
	if err := ctx.Err(); err != nil {
		return SourceStats{}, err
	}
	if !r.eof || r.readErr != nil || (r.closed.Load() && !r.finished) {
		return SourceStats{}, errors.New("machine snapshot source is incomplete")
	}
	r.finished = true
	stats := r.stats
	stats.Files = append([]string{}, stats.Files...)
	stats.SHA256 = hex.EncodeToString(r.digest.Sum(nil))
	return stats, nil
}

func (r *machineSourceReader) Close() error {
	r.closeOnce.Do(func() {
		r.closed.Store(true)
		r.closeErr = closeSnapshots(r.handles)
	})
	return r.closeErr
}
