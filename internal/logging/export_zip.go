package logging

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mihari-proxy/mihari/internal/platform"
)

const (
	exportManifestEntry = "manifest.json"
	exportDaemonEntry   = "daemon/mihari-daemon.log"
	exportTUIEntry      = "tui/mihari-tui.log"
	exportMihomoEntry   = "mihomo/mihomo.log"
)

type exportStage uint8

const (
	stageEnumerate exportStage = iota
	stageReadBatch
	stageDecodeLine
	stageWriteSpool
	stageWriteZip
	stageBeforeZipClose
	stageBeforeSync
	stageBeforePublish
)

type zipWriter interface {
	CreateHeader(*zip.FileHeader) (io.Writer, error)
	Close() error
}

type exportOps struct {
	Checkpoint      func(exportStage) error
	NewZipWriter    func(io.Writer) zipWriter
	Sync            func(*os.File) error
	Publish         func(*platform.PublishDir, *platform.PublishWorkspace, string, string, func(error)) error
	Observe         func(*exportTarget, *platform.PublishWorkspace)
	Snapshots       func([]snapshotHandle)
	Remove          func(*platform.PublishWorkspace, string) error
	CloseWorkspace  func(*platform.PublishWorkspace) error
	ClosePublishDir func(*platform.PublishDir) error
	CloseLogDir     func(*platform.DirectoryIdentity) error
}

type exportSpool struct {
	name string
	file *os.File
	data exportFile
}

func exportWithOps(ctx context.Context, request ExportRequest, ops exportOps) (_ ExportResult, retErr error) {
	ctx, request, ops = exportDefaults(ctx, request, ops)
	exportRange, err := normalizeExportRange(request.Now, request.Range)
	if err != nil {
		return ExportResult{}, stableExportError(err)
	}
	target, err := resolveExportTarget(request)
	if err != nil {
		return ExportResult{}, stableExportError(err)
	}
	workspace, err := target.Dir.CreateWorkspace()
	if err != nil {
		// Acquisition can fail after mkdir without a safely owned workspace.
		// Report possible residual data while preserving the primary failure.
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
		if cleanup != nil {
			warnExport(request.OnWarning)
			if !published && retErr != nil {
				retErr = errors.Join(retErr, cleanup)
			}
		}
		if retErr != nil {
			retErr = stableExportError(retErr)
		}
	}()

	sources := []struct{ path, entry, publicBase string }{
		{request.Paths.DaemonLog, exportDaemonEntry, "mihari-daemon.log"},
		{request.Paths.TUILog, exportTUIEntry, "mihari-tui.log"},
		{request.Paths.MihomoLog, exportMihomoEntry, "mihomo.log"},
	}
	for _, source := range sources {
		if err := runCheckpoint(ctx, ops, stageEnumerate); err != nil {
			return ExportResult{}, exportPipelineError(err)
		}
		handles, err := snapshotSource(ctx, request.PrivateFS, source.path, request.EnterRecordMutex, request.OpenLock, nil)
		if err != nil {
			return ExportResult{}, exportPipelineError(err)
		}
		ops.Snapshots(handles)
		spool, name, err := workspace.CreateTemp("spool-*")
		if err != nil {
			return ExportResult{}, exportPipelineError(joinCleanupError(err, closeSnapshots(handles), request.OnWarning))
		}
		item := exportSpool{name: name, file: spool, data: exportFile{Name: source.entry}}
		spools = append(spools, item)
		current := &spools[len(spools)-1]
		for _, handle := range handles {
			part, partErr := exportJSONWithCheckpoints(ctx, io.LimitReader(handle.file, handle.size), spool, exportRange, request.Redactor, func(stage exportStage) error {
				return runCheckpoint(ctx, ops, stage)
			})
			current.data.Lines += part.Lines
			current.data.SkippedInvalid += part.SkippedInvalid
			current.data.Redacted += part.Redacted
			current.data.Sources = append(current.data.Sources, fixedSourceName(source.path, source.publicBase, handle.name))
			if partErr != nil {
				return ExportResult{}, exportPipelineError(joinCleanupError(partErr, closeSnapshots(handles), request.OnWarning))
			}
		}
		if err := closeSnapshots(handles); err != nil {
			return ExportResult{}, exportPipelineError(err)
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
		}
	}
	if len(spools) == 0 {
		return ExportResult{}, ErrNoLogLines
	}

	zipFile, zipName, err = workspace.CreateTemp("archive-*.zip")
	if err != nil {
		return ExportResult{}, exportPipelineError(err)
	}
	zw := ops.NewZipWriter(zipFile)
	files := make([]exportFile, len(spools))
	for i := range spools {
		files[i] = spools[i].data
	}
	manifestWriter, err := createDeflatedEntry(zw, exportManifestEntry)
	if err == nil {
		err = json.NewEncoder(manifestWriter).Encode(newExportManifest(request.Now, exportRange, files))
	}
	if err != nil {
		return ExportResult{}, exportPipelineError(joinCleanupError(err, zw.Close(), request.OnWarning))
	}
	for i := range spools {
		if _, err := spools[i].file.Seek(0, io.SeekStart); err != nil {
			return ExportResult{}, exportPipelineError(joinCleanupError(err, zw.Close(), request.OnWarning))
		}
		writer, err := createDeflatedEntry(zw, spools[i].data.Name)
		if err == nil {
			err = copySpool(ctx, spools[i].file, writer, ops)
		}
		closeErr := spools[i].file.Close()
		spools[i].file = nil
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
		if err != nil || inside {
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

func exportDefaults(ctx context.Context, request ExportRequest, ops exportOps) (context.Context, ExportRequest, exportOps) {
	if ctx == nil {
		ctx = context.Background()
	}
	if request.OpenLock == nil {
		request.OpenLock = platform.OpenAdvisoryLock
	}
	if request.Redactor == nil {
		request.Redactor = NewRedactor()
	}
	if ops.Checkpoint == nil {
		ops.Checkpoint = func(exportStage) error { return nil }
	}
	if ops.NewZipWriter == nil {
		ops.NewZipWriter = func(w io.Writer) zipWriter { return zip.NewWriter(w) }
	}
	if ops.Sync == nil {
		ops.Sync = func(f *os.File) error { return f.Sync() }
	}
	if ops.Publish == nil {
		ops.Publish = func(d *platform.PublishDir, w *platform.PublishWorkspace, temp, target string, warning func(error)) error {
			return d.PublishNoReplace(w, temp, target, warning)
		}
	}
	if ops.Observe == nil {
		ops.Observe = func(*exportTarget, *platform.PublishWorkspace) {}
	}
	if ops.Snapshots == nil {
		ops.Snapshots = func([]snapshotHandle) {}
	}
	if ops.Remove == nil {
		ops.Remove = func(w *platform.PublishWorkspace, name string) error { return w.Remove(name) }
	}
	if ops.CloseWorkspace == nil {
		ops.CloseWorkspace = func(w *platform.PublishWorkspace) error { return w.Close() }
	}
	if ops.ClosePublishDir == nil {
		ops.ClosePublishDir = func(d *platform.PublishDir) error { return d.Close() }
	}
	if ops.CloseLogDir == nil {
		ops.CloseLogDir = func(d *platform.DirectoryIdentity) error { return d.Close() }
	}
	return ctx, request, ops
}

func createDeflatedEntry(zw zipWriter, name string) (io.Writer, error) {
	return zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
}

func copySpool(ctx context.Context, source io.Reader, destination io.Writer, ops exportOps) error {
	buffer := make([]byte, 32<<10)
	for {
		if err := runCheckpoint(ctx, ops, stageWriteZip); err != nil {
			return err
		}
		n, readErr := source.Read(buffer)
		if n > 0 {
			if _, err := destination.Write(buffer[:n]); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func runCheckpoint(ctx context.Context, ops exportOps, stage exportStage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ops.Checkpoint(stage)
}

func closeExportTargetWithWarning(target *exportTarget, warning func(error)) {
	if err := errors.Join(target.Dir.Close(), target.LogDir.Close()); err != nil {
		warnExport(warning)
	}
}

func warnExport(warning func(error)) {
	if warning != nil {
		warning(errors.New("log export cleanup incomplete"))
	}
}

func joinCleanupError(primary, cleanup error, warning func(error)) error {
	if cleanup != nil {
		warnExport(warning)
	}
	return errors.Join(primary, cleanup)
}

type publicExportError struct{ cause error }

func (e publicExportError) Error() string {
	switch {
	case errors.Is(e.cause, context.Canceled), errors.Is(e.cause, context.DeadlineExceeded):
		return "log export cancelled"
	case errors.Is(e.cause, ErrInvalidExportRequest):
		return ErrInvalidExportRequest.Error()
	case errors.Is(e.cause, ErrExportTargetExists):
		return ErrExportTargetExists.Error()
	case errors.Is(e.cause, ErrExportTargetChanged):
		return ErrExportTargetChanged.Error()
	case errors.Is(e.cause, ErrNoLogLines):
		return ErrNoLogLines.Error()
	case errors.Is(e.cause, platform.ErrPublishDirectoryChanged):
		return platform.ErrPublishDirectoryChanged.Error()
	default:
		return "log export failed"
	}
}

func (e publicExportError) Unwrap() error { return e.cause }

func stableExportError(err error) error {
	if err == nil {
		return nil
	}
	return publicExportError{cause: err}
}

func exportPipelineError(err error) error { return stableExportError(err) }

func fixedSourceName(configuredPath, publicBase, snapshotName string) string {
	configuredBase := filepath.Base(configuredPath)
	if snapshotName == configuredBase {
		return publicBase
	}
	if strings.HasPrefix(snapshotName, configuredBase+".") {
		return publicBase + strings.TrimPrefix(snapshotName, configuredBase)
	}
	return publicBase
}
