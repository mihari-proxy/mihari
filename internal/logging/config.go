package logging

import (
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// MaxCaptureLineBytes is the per-line capture cap for mihomo stdout/stderr.
	MaxCaptureLineBytes = 16 << 10
	// MaxExportRecordBytes is the per-record export parse cap.
	MaxExportRecordBytes = 1 << 20

	defaultMaxSizeBytes = 10 << 20
	defaultMaxFiles     = 3
	bootstrapMaxSize    = 100 << 20
	bootstrapMaxFiles   = 10
	writeWaitBound      = 250 * time.Millisecond
	failureReportWindow = time.Second
)

// Config is the runtime logging level and rotation limits.
type Config struct {
	Level        slog.Level
	MaxSizeBytes int64
	MaxFiles     int
}

// DefaultConfig is info, 10 MiB, 3 files.
func DefaultConfig() Config {
	return Config{Level: slog.LevelInfo, MaxSizeBytes: defaultMaxSizeBytes, MaxFiles: defaultMaxFiles}
}

// BootstrapConfig is the conservative TUI bootstrap: debug, 100 MiB, 10 files.
func BootstrapConfig() Config {
	return Config{Level: slog.LevelDebug, MaxSizeBytes: bootstrapMaxSize, MaxFiles: bootstrapMaxFiles}
}

// ParseLevel converts a persisted logging level to its slog level.
func ParseLevel(level string) (slog.Level, error) {
	switch level {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unsupported logging level %q", level)
	}
}

// ConfigFromFields validates persisted/API fields and converts MiB to bytes.
func ConfigFromFields(level string, maxSizeMB, maxFiles int64) (Config, error) {
	parsed, err := ParseLevel(level)
	if err != nil {
		return Config{}, err
	}
	if maxSizeMB < 1 || maxSizeMB > 100 {
		return Config{}, fmt.Errorf("logging max size must be between 1 and 100 MiB")
	}
	if maxFiles < 1 || maxFiles > 10 {
		return Config{}, fmt.Errorf("logging max files must be between 1 and 10")
	}
	return Config{
		Level:        parsed,
		MaxSizeBytes: maxSizeMB * 1024 * 1024,
		MaxFiles:     int(maxFiles),
	}, nil
}

// FailureClass is a stable, rate-limited stderr category.
type FailureClass string

const (
	FailureDropped FailureClass = "dropped"
	FailureWrite   FailureClass = "write"
	FailureRotate  FailureClass = "rotate"
	FailureCleanup FailureClass = "cleanup"
)

// FailureReporter records classified logging failures.
type FailureReporter interface {
	Report(class FailureClass, err error)
}

type failureReporter struct {
	out      io.Writer
	redactor *Redactor
	now      func() time.Time
	mu       sync.Mutex
	last     map[FailureClass]time.Time
}

var pathTokenPattern = regexp.MustCompile(`(?:[A-Za-z]:)?(?:[\\/][^\\/:*?"<>|\r\n]+)+`)

// NewFailureReporter writes rate-limited, redacted failure lines without full paths.
func NewFailureReporter(out io.Writer, redactor *Redactor, now func() time.Time) FailureReporter {
	if now == nil {
		now = time.Now
	}
	return &failureReporter{out: out, redactor: redactor, now: now, last: make(map[FailureClass]time.Time)}
}

func (r *failureReporter) Report(class FailureClass, err error) {
	if r == nil || r.out == nil {
		return
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if prev, ok := r.last[class]; ok && now.Sub(prev) < failureReportWindow {
		return
	}
	r.last[class] = now
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if r.redactor != nil {
		msg = r.redactor.String(msg)
	}
	msg = pathTokenPattern.ReplaceAllString(msg, "[path]")
	msg = strings.NewReplacer("\r", " ", "\n", " ").Replace(msg)
	line := "logging: " + string(class)
	if msg != "" {
		line += ": " + msg
	}
	_, _ = io.WriteString(r.out, line+"\n")
}
