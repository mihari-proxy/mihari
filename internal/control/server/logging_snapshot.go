package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
)

const (
	maxConcurrentSnapshots     = 2
	maxSnapshotAdmitsPerMinute = 6
	snapshotRequestTimeout     = 120 * time.Second
	snapshotWriteTimeout       = 5 * time.Second
)

type snapshotGate struct {
	mu       sync.Mutex
	inflight int
	admits   []time.Time
}

func (g *snapshotGate) tryAdmit(now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	cutoff := now.Add(-time.Minute)
	kept := g.admits[:0]
	for _, ts := range g.admits {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	g.admits = kept
	if len(g.admits) >= maxSnapshotAdmitsPerMinute || g.inflight >= maxConcurrentSnapshots {
		return false
	}
	g.admits = append(g.admits, now)
	g.inflight++
	return true
}

func (g *snapshotGate) release() {
	g.mu.Lock()
	if g.inflight > 0 {
		g.inflight--
	}
	g.mu.Unlock()
}

func (s *Server) loggingSnapshot(writer http.ResponseWriter, request *http.Request) {
	if !s.beginSnapshot() {
		writeSnapshotPrestreamError(writer, protocol.APIError{Code: protocol.CodeInvalidState, Message: "machine snapshot is busy"})
		return
	}
	defer s.snapshotHandlers.Done()
	decoded, err := protocol.DecodeMachineLogRequest(request.Body, s.now())
	if err != nil {
		writeSnapshotPrestreamError(writer, err)
		return
	}
	if s.snapshotContext().Err() != nil {
		writeSnapshotPrestreamError(writer, protocol.APIError{Code: protocol.CodeInvalidState, Message: "machine snapshot is busy"})
		return
	}
	if s.snapshotSource == nil {
		writeSnapshotPrestreamError(writer, protocol.APIError{Code: protocol.CodeInvalidState, Message: "machine snapshot is unavailable"})
		return
	}
	if !s.snapshotGate.tryAdmit(s.now()) {
		writeSnapshotPrestreamError(writer, protocol.APIError{Code: protocol.CodeInvalidState, Message: "machine snapshot is busy"})
		return
	}
	defer s.snapshotGate.release()

	ctx, cancel := context.WithTimeout(s.snapshotContext(), snapshotRequestTimeout)
	defer cancel()
	stopOnDisconnect := context.AfterFunc(request.Context(), cancel)
	defer stopOnDisconnect()

	set, err := s.snapshotSource.Open(ctx, logging.SnapshotWindow{From: decoded.From, To: decoded.To})
	if err != nil {
		writeSnapshotPrestreamError(writer, snapshotError(err))
		return
	}
	defer func() { _ = set.Close() }() // Handler owns the set until its producer has joined.

	snapshotID, err := s.newSnapshotID()
	if err != nil {
		writeSnapshotPrestreamError(writer, err)
		return
	}

	frames := make(chan []byte, 1)
	errCh := make(chan error, 1)
	s.snapshotWG.Add(1)
	go func() {
		defer s.snapshotWG.Done()
		defer close(frames)
		if produceErr := produceMachineSnapshot(ctx, set, decoded, snapshotID, frames); produceErr != nil {
			errCh <- produceErr
		}
	}()
	defer func() {
		cancel()
		for range frames {
		}
	}()

	started := false
	for frame := range frames {
		if !started {
			writer.Header().Set("Content-Type", "application/x-ndjson")
			writer.WriteHeader(http.StatusOK)
			started = true
			_ = http.NewResponseController(writer).Flush()
		}
		if writeErr := writeSnapshotFrame(writer, frame); writeErr != nil {
			return
		}
	}
	select {
	case streamErr := <-errCh:
		if !started {
			writeSnapshotPrestreamError(writer, snapshotError(streamErr))
			return
		}
		frame, encodeErr := encodeSnapshotErrorFrame(streamErr)
		if encodeErr != nil {
			return
		}
		_ = writeSnapshotFrame(writer, frame)
	default:
	}
}

func produceMachineSnapshot(ctx context.Context, set logging.SnapshotSet, request protocol.MachineLogRequest, snapshotID string, frames chan []byte) error {
	header := protocol.MachineLogHeader{
		MachineLogFrameMeta: protocol.MachineLogFrameMeta{Schema: protocol.MachineLogStreamSchema, Type: "header"},
		SnapshotID:          snapshotID,
		From:                request.From,
		To:                  request.To,
		Sources:             []string{string(logging.DaemonSource), string(logging.MihomoSource)},
	}
	frame, err := encodeSnapshotFrame(header)
	if err != nil {
		return err
	}
	if err := sendSnapshotFrame(ctx, frames, frame); err != nil {
		return err
	}

	var total int64
	for _, id := range []logging.SourceID{logging.DaemonSource, logging.MihomoSource} {
		reader, err := set.Source(id)
		if err != nil {
			return snapshotError(err)
		}
		for {
			payload, redacted, err := reader.Next(ctx)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return snapshotError(err)
			}
			if len(payload) > protocol.MaxMachineLogRecordBytes {
				return protocol.APIError{Code: protocol.CodeDataFailure, Message: "machine snapshot record exceeds budget"}
			}
			record := protocol.MachineLogRecord{
				MachineLogFrameMeta: protocol.MachineLogFrameMeta{Schema: protocol.MachineLogStreamSchema, Type: "record"},
				Source:              string(id),
				PayloadB64:          base64.StdEncoding.EncodeToString(payload),
				Redacted:            redacted,
			}
			frame, err = encodeSnapshotFrame(record)
			if err != nil {
				return err
			}
			if err := sendSnapshotFrame(ctx, frames, frame); err != nil {
				return err
			}
		}
		stats, err := reader.Finish(ctx)
		if err != nil {
			return snapshotError(err)
		}
		files := stats.Files
		if files == nil {
			files = []string{}
		}
		end := protocol.MachineLogSourceEnd{
			MachineLogFrameMeta: protocol.MachineLogFrameMeta{Schema: protocol.MachineLogStreamSchema, Type: "source_end"},
			Source:              string(id),
			Lines:               stats.Lines,
			SkippedInvalid:      stats.SkippedInvalid,
			Redacted:            stats.Redacted,
			Sources:             files,
			SHA256:              stats.SHA256,
			Bytes:               stats.Bytes,
		}
		frame, err = encodeSnapshotFrame(end)
		if err != nil {
			return err
		}
		if err := sendSnapshotFrame(ctx, frames, frame); err != nil {
			return err
		}
		total += stats.Bytes
	}
	if err := set.Finish(ctx); err != nil {
		return snapshotError(err)
	}
	complete := protocol.MachineLogComplete{
		MachineLogFrameMeta: protocol.MachineLogFrameMeta{Schema: protocol.MachineLogStreamSchema, Type: "complete"},
		SnapshotID:          snapshotID,
		SourceCount:         2,
		TotalBytes:          total,
	}
	frame, err = encodeSnapshotFrame(complete)
	if err != nil {
		return err
	}
	return sendSnapshotFrame(ctx, frames, frame)
}

func sendSnapshotFrame(ctx context.Context, frames chan []byte, frame []byte) error {
	select {
	case frames <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func encodeSnapshotFrame(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "machine snapshot failed"}
	}
	raw = append(raw, '\n')
	if len(raw) > protocol.MaxMachineLogFrameBytes {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "machine snapshot frame exceeds budget"}
	}
	return raw, nil
}

func encodeSnapshotErrorFrame(err error) ([]byte, error) {
	return encodeSnapshotFrame(protocol.MachineLogFailure{
		MachineLogFrameMeta: protocol.MachineLogFrameMeta{Schema: protocol.MachineLogStreamSchema, Type: "error"},
		Error:               snapshotAPIError(err),
	})
}

func writeSnapshotFrame(writer http.ResponseWriter, frame []byte) error {
	rc := http.NewResponseController(writer)
	_ = rc.SetWriteDeadline(time.Now().Add(snapshotWriteTimeout))
	if _, err := writer.Write(frame); err != nil {
		return err
	}
	if err := rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

func writeSnapshotPrestreamError(writer http.ResponseWriter, err error) {
	apiError := snapshotAPIError(err)
	status := http.StatusInternalServerError
	switch apiError.Code {
	case protocol.CodeInvalidArgument:
		status = http.StatusBadRequest
	case protocol.CodePermissionDenied:
		status = http.StatusForbidden
	case protocol.CodeInvalidState:
		status = http.StatusConflict
	case protocol.CodeDataFailure:
		status = http.StatusInternalServerError
	}
	writeJSON(writer, status, protocol.NewError(apiError.Code, apiError.Message, apiError.Details))
}

func snapshotError(err error) error {
	return snapshotAPIError(err)
}

func snapshotAPIError(err error) protocol.APIError {
	var apiError protocol.APIError
	if errors.As(err, &apiError) {
		return apiError
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "machine snapshot is busy"}
	}
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: "machine snapshot failed"}
}

func (s *Server) newSnapshotID() (string, error) {
	if s.snapshotID != nil {
		if id := s.snapshotID(); id != "" {
			return id, nil
		}
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", protocol.APIError{Code: protocol.CodeDataFailure, Message: "machine snapshot failed"}
	}
	return hex.EncodeToString(raw[:]), nil
}

func (s *Server) snapshotContext() context.Context {
	if s.snapshotCtx != nil {
		return s.snapshotCtx
	}
	return context.Background()
}
