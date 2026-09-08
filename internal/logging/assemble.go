package logging

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

const (
	// ExportScopeMachineAndCurrentUser includes machine sources and the current user's TUI logs.
	ExportScopeMachineAndCurrentUser = "machine_and_current_user"
	// ExportScopeCurrentUserOnly is the explicit TUI-only export; it never auto-degrades.
	ExportScopeCurrentUserOnly = "current_user_only"
	// ExportSourceCollected means the named source was requested and finished.
	ExportSourceCollected = "collected"
	// ExportSourceNotRequested means the named source was omitted by the caller.
	ExportSourceNotRequested = "not_requested"
	// ExportSourceUnavailable means the named source was requested but could not be read.
	ExportSourceUnavailable = "unavailable"
)

const exportMachineNote = "machine log sources were not requested"

var (
	tuiMaxFiles        = 10
	tuiMaxBytes        = int64(1 << 30)
	assembleSpoolLimit = int64(2 << 30)
)

type snapshotSetSource struct {
	set SnapshotSet
	id  SourceID
}

func (s snapshotSetSource) Open(_ context.Context, _ SnapshotWindow) (SourceReader, error) {
	return s.set.Source(s.id)
}

// NamedSourcesFromSet borrows daemon then mihomo readers from a SnapshotSet.
func NamedSourcesFromSet(set SnapshotSet) []NamedSource {
	return []NamedSource{
		{ID: DaemonSource, Source: snapshotSetSource{set: set, id: DaemonSource}},
		{ID: MihomoSource, Source: snapshotSetSource{set: set, id: MihomoSource}},
	}
}

type fileSnapshotSource struct {
	id      SourceID
	options MachineSnapshotOptions
}

// NewFileSnapshotSource reads one local log sequence; TUI is capped at 10 files / 1GiB.
func NewFileSnapshotSource(id SourceID, options MachineSnapshotOptions) SnapshotSource {
	return &fileSnapshotSource{id: id, options: options}
}

func (s *fileSnapshotSource) Open(ctx context.Context, window SnapshotWindow) (SourceReader, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	path := s.path()
	if path == "" {
		return nil, ErrInvalidExportRequest
	}
	if window.From != nil {
		from := *window.From
		window.From = &from
	}
	lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var release func()
	var err error
	if s.options.EnterRecordMutex != nil {
		release, err = s.options.EnterRecordMutex(lockCtx, path)
	}
	var handles []snapshotHandle
	if err == nil {
		handles, err = snapshotSource(lockCtx, s.options.PrivateFS, path, nil, nil, nil)
	}
	if release != nil {
		release()
	}
	if err != nil {
		return nil, err
	}
	if len(handles) > tuiMaxFiles {
		_ = closeSnapshots(handles)
		return nil, errSnapshotBudget
	}
	var scanned int64
	files := make([]string, 0, len(handles))
	for _, handle := range handles {
		if handle.size < 0 || handle.size > tuiMaxBytes-scanned {
			_ = closeSnapshots(handles)
			return nil, errSnapshotBudget
		}
		scanned += handle.size
		files = append(files, handle.name)
	}
	redactor := s.options.Redactor
	if redactor == nil {
		redactor = NewRedactor()
	}
	var outputBytes int64
	return &machineSourceReader{
		handles:     handles,
		window:      window,
		redactor:    redactor,
		outputBytes: &outputBytes,
		digest:      sha256.New(),
		stats:       SourceStats{Source: s.id, Files: files},
	}, nil
}

func (s *fileSnapshotSource) path() string {
	switch s.id {
	case DaemonSource:
		return s.options.Paths.DaemonLog
	case TUISource:
		return s.options.Paths.TUILog
	case MihomoSource:
		return s.options.Paths.MihomoLog
	default:
		return ""
	}
}

// Assemble consumes NamedSource readers, then finish, then publishes a ZIP.
func Assemble(ctx context.Context, request ExportRequest, scope string, sources []NamedSource, finish func(context.Context) error) (ExportResult, error) {
	return assembleWithOps(ctx, request, scope, sources, finish, exportOps{})
}

type assembledSpool struct {
	id    SourceID
	spool exportSpool
}

func assembleWithOps(ctx context.Context, request ExportRequest, scope string, sources []NamedSource, finish func(context.Context) error, ops exportOps) (_ ExportResult, retErr error) {
	ctx, request, ops = exportDefaults(ctx, request, ops)
	if err := ctx.Err(); err != nil {
		return ExportResult{}, stableExportError(err)
	}
	switch scope {
	case ExportScopeMachineAndCurrentUser, ExportScopeCurrentUserOnly:
	default:
		return ExportResult{}, stableExportError(fmt.Errorf("%w: unsupported export scope", ErrInvalidExportRequest))
	}
	exportRange, err := normalizeExportRange(request.Now, request.Range)
	if err != nil {
		return ExportResult{}, stableExportError(err)
	}
	target, err := resolveExportTargetWithOps(request, ops)
	if err != nil {
		return ExportResult{}, stableExportError(err)
	}
	workspace, err := target.Dir.CreateWorkspace()
	if err != nil {
		warnExport(request.OnWarning)
		closeExportTargetWithWarning(target, request.OnWarning)
		return ExportResult{}, exportPipelineError(err)
	}
	ops.Observe(target, workspace)
	var spools []exportSpool
	var zipFile *os.File
	var zipName string
	published := false
	defer func() {
		var cleanup error
		if zipFile != nil {
			cleanup = errors.Join(cleanup, zipFile.Close())
		}
		for i := range spools {
			if spools[i].file != nil {
				cleanup = errors.Join(cleanup, spools[i].file.Close())
			}
			cleanup = errors.Join(cleanup, ops.Remove(workspace, spools[i].name))
		}
		if zipName != "" {
			removeErr := ops.Remove(workspace, zipName)
			if !errors.Is(removeErr, os.ErrNotExist) {
				cleanup = errors.Join(cleanup, removeErr)
			}
		}
		cleanup = errors.Join(cleanup, ops.CloseWorkspace(workspace), ops.ClosePublishDir(target.Dir), ops.CloseLogDir(target.LogDir))
		if cleanup != nil || errors.Is(retErr, platform.ErrPublishCleanupIncomplete) {
			warnExport(request.OnWarning)
			if !published && retErr != nil {
				retErr = errors.Join(retErr, cleanup)
			}
		}
		if retErr != nil {
			retErr = stableExportError(retErr)
		}
	}()

	window := assembleWindow(request, exportRange)
	status := exportSourceStatus{Daemon: ExportSourceNotRequested, Mihomo: ExportSourceNotRequested, TUI: ExportSourceUnavailable}
	var collected []assembledSpool
	var totalBytes int64

	for _, named := range sources {
		entry, ok := exportEntryName(named.ID)
		if !ok || named.Source == nil {
			return ExportResult{}, exportPipelineError(fmt.Errorf("%w: unsupported export source", ErrInvalidExportRequest))
		}
		if err := runCheckpoint(ctx, ops, stageEnumerate); err != nil {
			return ExportResult{}, exportPipelineError(err)
		}
		reader, err := named.Source.Open(ctx, window)
		if err != nil {
			return ExportResult{}, exportPipelineError(err)
		}
		spool, name, err := workspace.CreateTemp("spool-*")
		if err != nil {
			_ = reader.Close()
			return ExportResult{}, exportPipelineError(err)
		}
		item := exportSpool{name: name, file: spool, data: exportFile{Name: entry}}
		spools = append(spools, item)
		current := &spools[len(spools)-1]
		readErr := copySourceReader(ctx, ops, reader, spool, &totalBytes)
		stats, finishErr := sourceFinish(ctx, reader, readErr)
		closeErr := reader.Close()
		if err := errors.Join(readErr, finishErr, closeErr); err != nil {
			return ExportResult{}, exportPipelineError(err)
		}
		if named.ID == TUISource && (len(stats.Files) > tuiMaxFiles || stats.Bytes > tuiMaxBytes) {
			return ExportResult{}, exportPipelineError(errSnapshotBudget)
		}
		current.data.Lines = stats.Lines
		current.data.SkippedInvalid = stats.SkippedInvalid
		current.data.Redacted = stats.Redacted
		current.data.Sources = append([]string{}, stats.Files...)
		if current.data.Sources == nil {
			current.data.Sources = []string{}
		}
		switch named.ID {
		case DaemonSource:
			status.Daemon = ExportSourceCollected
		case MihomoSource:
			status.Mihomo = ExportSourceCollected
		case TUISource:
			status.TUI = ExportSourceCollected
		}
		if current.data.Lines == 0 {
			if err := spool.Close(); err != nil {
				return ExportResult{}, exportPipelineError(err)
			}
			current.file = nil
			if err := workspace.Remove(name); err != nil {
				return ExportResult{}, exportPipelineError(err)
			}
			spools = spools[:len(spools)-1]
			continue
		}
		collected = append(collected, assembledSpool{id: named.ID, spool: *current})
	}

	if finish != nil {
		if err := finish(ctx); err != nil {
			return ExportResult{}, exportPipelineError(err)
		}
	}
	if len(collected) == 0 {
		return ExportResult{}, ErrNoLogLines
	}
	sort.SliceStable(collected, func(i, j int) bool {
		return exportRank(collected[i].id) < exportRank(collected[j].id)
	})
	ordered := make([]exportSpool, len(collected))
	for i := range collected {
		ordered[i] = collected[i].spool
	}
	// Preserve cleanup ownership: spools still references the live files.
	for i := range spools {
		for j := range ordered {
			if spools[i].name == ordered[j].name {
				ordered[j] = spools[i]
			}
		}
	}

	zipFile, zipName, err = workspace.CreateTemp("archive-*.zip")
	if err != nil {
		return ExportResult{}, exportPipelineError(err)
	}
	zw := ops.NewZipWriter(zipFile)
	files := make([]exportFile, len(ordered))
	for i := range ordered {
		files[i] = ordered[i].data
	}
	notes := []string{exportReviewNote}
	if scope == ExportScopeCurrentUserOnly {
		notes = append(notes, exportMachineNote)
	}
	manifestWriter, err := createDeflatedEntry(zw, exportManifestEntry)
	if err == nil {
		err = json.NewEncoder(manifestWriter).Encode(newExportManifestV2(request.Now, exportRange, files, scope, status, notes))
	}
	if err != nil {
		return ExportResult{}, exportPipelineError(joinCleanupError(err, zw.Close(), request.OnWarning))
	}
	for i := range ordered {
		if _, err := ordered[i].file.Seek(0, io.SeekStart); err != nil {
			return ExportResult{}, exportPipelineError(joinCleanupError(err, zw.Close(), request.OnWarning))
		}
		writer, err := createDeflatedEntry(zw, ordered[i].data.Name)
		if err == nil {
			err = copySpool(ctx, ordered[i].file, writer, ops)
		}
		closeErr := ordered[i].file.Close()
		ordered[i].file = nil
		for j := range spools {
			if spools[j].name == ordered[i].name {
				spools[j].file = nil
			}
		}
		if err = errors.Join(err, closeErr); err != nil {
			return ExportResult{}, exportPipelineError(joinCleanupError(err, zw.Close(), request.OnWarning))
		}
	}
	if err := runCheckpoint(ctx, ops, stageBeforeZipClose); err != nil {
		return ExportResult{}, exportPipelineError(joinCleanupError(err, zw.Close(), request.OnWarning))
	}
	if err := zw.Close(); err != nil {
		return ExportResult{}, exportPipelineError(err)
	}
	if err := runCheckpoint(ctx, ops, stageBeforeSync); err != nil {
		return ExportResult{}, exportPipelineError(err)
	}
	if err := ops.Sync(zipFile); err != nil {
		return ExportResult{}, exportPipelineError(err)
	}
	if err := zipFile.Close(); err != nil {
		return ExportResult{}, exportPipelineError(err)
	}
	zipFile = nil
	if err := runCheckpoint(ctx, ops, stageBeforePublish); err != nil {
		return ExportResult{}, exportPipelineError(err)
	}
	for {
		if err := ctx.Err(); err != nil {
			return ExportResult{}, exportPipelineError(err)
		}
		inside, err := target.Dir.IsWithin(target.LogDir)
		if err != nil {
			return ExportResult{}, exportPipelineError(errors.Join(ErrExportTargetChanged, err))
		}
		if inside {
			return ExportResult{}, ErrExportTargetChanged
		}
		err = ops.Publish(target.Dir, workspace, zipName, target.Name, func(error) { warnExport(request.OnWarning) })
		if err == nil {
			published = true
			return ExportResult{Path: target.Path}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return ExportResult{}, exportPipelineError(err)
		}
		if !target.AutoNumber {
			return ExportResult{}, ErrExportTargetExists
		}
		if err := target.Advance(); err != nil {
			return ExportResult{}, exportPipelineError(err)
		}
	}
}

func copySourceReader(ctx context.Context, ops exportOps, reader SourceReader, spool io.Writer, totalBytes *int64) error {
	for {
		if err := runCheckpoint(ctx, ops, stageReadBatch); err != nil {
			return err
		}
		payload, _, err := reader.Next(ctx)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := runCheckpoint(ctx, ops, stageWriteSpool); err != nil {
			return err
		}
		n := int64(len(payload) + 1)
		if *totalBytes > assembleSpoolLimit-n {
			return errSnapshotBudget
		}
		if _, err := spool.Write(append(append([]byte(nil), payload...), '\n')); err != nil {
			return err
		}
		*totalBytes += n
	}
}

func sourceFinish(ctx context.Context, reader SourceReader, readErr error) (SourceStats, error) {
	if readErr != nil {
		_, _ = reader.Finish(ctx)
		return SourceStats{}, readErr
	}
	return reader.Finish(ctx)
}

func assembleWindow(request ExportRequest, exportRange ExportRange) SnapshotWindow {
	to := exportRange.To
	if exportRange.Kind == RangeAll {
		to = request.Now.UTC()
		if to.IsZero() {
			to = time.Now().UTC()
		}
		return SnapshotWindow{To: to}
	}
	from := exportRange.From
	return SnapshotWindow{From: &from, To: to}
}

func exportEntryName(id SourceID) (string, bool) {
	switch id {
	case DaemonSource:
		return exportDaemonEntry, true
	case TUISource:
		return exportTUIEntry, true
	case MihomoSource:
		return exportMihomoEntry, true
	default:
		return "", false
	}
}

func exportRank(id SourceID) int {
	switch id {
	case DaemonSource:
		return 0
	case TUISource:
		return 1
	case MihomoSource:
		return 2
	default:
		return 3
	}
}
