package logging

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

// RangeKind identifies a supported log export time window.
type RangeKind string

const (
	RangeLast24Hours   RangeKind = "last_24h"
	RangeLast60Minutes RangeKind = "last_60m"
	RangeBetween       RangeKind = "between"
	RangeAll           RangeKind = "all"
)

// ExportRange describes the requested closed time window.
type ExportRange struct {
	Kind RangeKind
	From time.Time
	To   time.Time
}

// ExportPaths contains the private logging and default export paths.
type ExportPaths struct {
	LogDir    string
	ExportDir string
	DaemonLog string
	TUILog    string
	MihomoLog string
}

// ExportRequest contains one immutable log export request.
type ExportRequest struct {
	Now        time.Time
	Range      ExportRange
	OutputPath string
	AutoNumber bool
	Paths      ExportPaths
	PrivateFS  *platform.PrivateFS

	OpenLock         func(*platform.PrivateFS, string) (platform.AdvisoryLock, error)
	EnterRecordMutex func(basePath string) func()
	Redactor         *Redactor
	OnWarning        func(error)
}

// ExportResult identifies the published archive.
type ExportResult struct {
	Path string
}

type exportTarget struct {
	Dir        *platform.PublishDir
	LogDir     *platform.DirectoryIdentity
	Name       string
	Path       string
	AutoNumber bool
	Base       string
	Suffix     int64
}

var (
	ErrInvalidExportRequest = errors.New("invalid export request")
	ErrExportTargetExists   = errors.New("export target already exists")
	ErrExportTargetChanged  = errors.New("export target changed")
	ErrNoLogLines           = errors.New("no log lines in selected range")

	errExportPipelineUnavailable  = errors.New("log export pipeline is not implemented")
	errExportTargetSuffixOverflow = errors.New("export target suffix overflow")
)

// Export validates dependency defaults before handing the request to the
// export pipeline. The pipeline is completed by the export orchestration task.
func Export(_ context.Context, request ExportRequest) (ExportResult, error) {
	if request.OpenLock == nil {
		request.OpenLock = platform.OpenAdvisoryLock
	}
	return ExportResult{}, errExportPipelineUnavailable
}

func normalizeExportRange(now time.Time, exportRange ExportRange) (ExportRange, error) {
	switch exportRange.Kind {
	case RangeLast24Hours:
		to := now.UTC()
		return ExportRange{Kind: exportRange.Kind, From: to.Add(-24 * time.Hour), To: to}, nil
	case RangeLast60Minutes:
		to := now.UTC()
		return ExportRange{Kind: exportRange.Kind, From: to.Add(-60 * time.Minute), To: to}, nil
	case RangeBetween:
		if exportRange.From.After(exportRange.To) {
			return ExportRange{}, ErrInvalidExportRequest
		}
		return ExportRange{Kind: exportRange.Kind, From: exportRange.From.UTC(), To: exportRange.To.UTC()}, nil
	case RangeAll:
		return ExportRange{Kind: exportRange.Kind}, nil
	default:
		return ExportRange{}, ErrInvalidExportRequest
	}
}

func resolveExportTarget(request ExportRequest) (_ *exportTarget, retErr error) {
	if request.PrivateFS == nil {
		return nil, fmt.Errorf("%w: private log storage is unavailable", ErrInvalidExportRequest)
	}
	custom := request.OutputPath != ""
	if custom && request.AutoNumber {
		return nil, fmt.Errorf("%w: custom target cannot use automatic numbering", ErrInvalidExportRequest)
	}
	if !custom && !request.AutoNumber {
		return nil, fmt.Errorf("%w: default target requires automatic numbering", ErrInvalidExportRequest)
	}

	name, parent, base, err := exportTargetParts(request)
	if err != nil {
		return nil, err
	}

	logDir, err := request.PrivateFS.OpenDirIdentity(request.Paths.LogDir)
	if err != nil {
		return nil, fmt.Errorf("%w: open log directory identity", ErrInvalidExportRequest)
	}
	defer func() {
		if retErr != nil {
			_ = logDir.Close()
		}
	}()

	var dir *platform.PublishDir
	if custom {
		dir, err = platform.OpenPublishDir(parent)
	} else {
		if err = request.PrivateFS.EnsureDir(request.Paths.ExportDir); err == nil {
			dir, err = request.PrivateFS.OpenPublishDir(request.Paths.ExportDir)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%w: open export directory", ErrInvalidExportRequest)
	}
	defer func() {
		if retErr != nil {
			_ = dir.Close()
		}
	}()

	inside, err := dir.IsWithin(logDir)
	if err != nil {
		return nil, fmt.Errorf("%w: check export directory", ErrInvalidExportRequest)
	}
	if inside {
		return nil, fmt.Errorf("%w: export directory is within log directory", ErrInvalidExportRequest)
	}

	target := &exportTarget{
		Dir:        dir,
		LogDir:     logDir,
		Name:       name,
		AutoNumber: request.AutoNumber,
		Base:       base,
	}
	for {
		exists, existsErr := dir.Exists(target.Name)
		if existsErr != nil {
			return nil, fmt.Errorf("%w: inspect export target", ErrInvalidExportRequest)
		}
		if !exists {
			break
		}
		if !target.AutoNumber {
			return nil, ErrExportTargetExists
		}
		if err := target.Advance(); err != nil {
			return nil, err
		}
	}
	target.Path = filepath.Join(dir.Path(), target.Name)
	return target, nil
}

func exportTargetParts(request ExportRequest) (name, parent, base string, err error) {
	if request.OutputPath == "" {
		if request.Now.IsZero() || request.Paths.ExportDir == "" {
			return "", "", "", fmt.Errorf("%w: default target is incomplete", ErrInvalidExportRequest)
		}
		base = "mihari-logs-" + request.Now.Format("20060102-150405-0700")
		return base + ".zip", request.Paths.ExportDir, base, nil
	}
	if !filepath.IsAbs(request.OutputPath) || !strings.EqualFold(filepath.Ext(request.OutputPath), ".zip") {
		return "", "", "", fmt.Errorf("%w: target must be an absolute zip path", ErrInvalidExportRequest)
	}
	abs, err := filepath.Abs(filepath.Clean(request.OutputPath))
	if err != nil {
		return "", "", "", fmt.Errorf("%w: resolve export target", ErrInvalidExportRequest)
	}
	name = filepath.Base(abs)
	if name == ".zip" || name == "" || name == "." || name == ".." {
		return "", "", "", fmt.Errorf("%w: target basename is invalid", ErrInvalidExportRequest)
	}
	return name, filepath.Dir(abs), strings.TrimSuffix(name, filepath.Ext(name)), nil
}

func (t *exportTarget) Advance() error {
	if t == nil || !t.AutoNumber || t.Base == "" {
		return ErrInvalidExportRequest
	}
	if t.Suffix == math.MaxInt64 {
		return errExportTargetSuffixOverflow
	}
	t.Suffix++
	t.Name = t.Base + "-" + strconv.FormatInt(t.Suffix, 10) + ".zip"
	if t.Dir != nil {
		t.Path = filepath.Join(t.Dir.Path(), t.Name)
	}
	return nil
}
