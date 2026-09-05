package logging

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Checkpoint   func(exportStage) error
	NewZipWriter func(io.Writer) zipWriter
	Sync         func(*os.File) error
	Publish      func(*platform.PublishDir, *platform.PublishWorkspace, string, string, func(error)) error
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
		return ExportResult{}, err
	}
	target, err := resolveExportTarget(request)
	if err != nil {
		return ExportResult{}, err
	}
	workspace, err := target.Dir.CreateWorkspace()
	if err != nil {
		closeExportTargetWithWarning(target, request.OnWarning)
		return ExportResult{}, exportPipelineError(err)
	}
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
			cleanup = errors.Join(cleanup, workspace.Remove(spools[i].name))
		}
		if zipName != "" {
			removeErr := workspace.Remove(zipName)
			if !errors.Is(removeErr, os.ErrNotExist) {
				cleanup = errors.Join(cleanup, removeErr)
			}
		}
		cleanup = errors.Join(cleanup, workspace.Close(), target.Dir.Close(), target.LogDir.Close())
		if cleanup != nil {
			warnExport(request.OnWarning)
			if !published && retErr != nil {
				retErr = errors.Join(retErr, cleanup)
			}
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
		spool, name, err := workspace.CreateTemp("spool-*")
		if err != nil {
			_ = closeSnapshots(handles)
			return ExportResult{}, exportPipelineError(err)
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
				_ = closeSnapshots(handles)
				return ExportResult{}, exportPipelineError(partErr)
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
		_ = zw.Close()
		return ExportResult{}, exportPipelineError(err)
	}
	for i := range spools {
		if _, err := spools[i].file.Seek(0, io.SeekStart); err != nil {
			_ = zw.Close()
			return ExportResult{}, exportPipelineError(err)
		}
		writer, err := createDeflatedEntry(zw, spools[i].data.Name)
		if err == nil {
			err = copySpool(ctx, spools[i].file, writer, ops)
		}
		closeErr := spools[i].file.Close()
		spools[i].file = nil
		if err = errors.Join(err, closeErr); err != nil {
			_ = zw.Close()
			return ExportResult{}, exportPipelineError(err)
		}
	}
	if err := runCheckpoint(ctx, ops, stageBeforeZipClose); err != nil {
		_ = zw.Close()
		return ExportResult{}, exportPipelineError(err)
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

func exportPipelineError(err error) error { return fmt.Errorf("log export failed: %w", err) }

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
