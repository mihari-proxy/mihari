package logging

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

const (
	diagnosticMaxDepth = 32
	diagnosticMaxNodes = 64
	diagnosticMaxBytes = 4096
)

// NewDiagnosticReporter adapts internal diagnostics to logger output.
func NewDiagnosticReporter(logger *slog.Logger, redactor *Redactor) diagnostics.Reporter {
	if logger == nil {
		return nil
	}
	return func(ctx context.Context, record diagnostics.Record) {
		if ctx == nil {
			ctx = context.Background()
		}
		if !logger.Enabled(ctx, record.Level) {
			return
		}
		logger.LogAttrs(ctx, record.Level, record.Event,
			slog.String("component", record.Component),
			slog.String("cause", diagnosticText(record.Err, redactor)),
		)
	}
}

func diagnosticText(err error, redactor *Redactor) string {
	if err == nil {
		return ""
	}
	formatter := diagnosticFormatter{redactor: redactor, seen: make(map[error]struct{})}
	formatter.visit(err, 1)
	if len(formatter.parts) == 0 {
		formatter.add(typeSummary(err))
	}
	return truncateDiagnostic(strings.Join(formatter.parts, ": "), diagnosticMaxBytes)
}

type diagnosticFormatter struct {
	redactor *Redactor
	seen     map[error]struct{}
	parts    []string
	nodes    int
	bounded  bool
}

func (f *diagnosticFormatter) visit(err error, depth int) {
	if f.bounded {
		return
	}
	if f.nodes >= diagnosticMaxNodes {
		f.markBounded()
		return
	}
	f.nodes++
	if err == nil {
		return
	}
	if depth > diagnosticMaxDepth {
		f.markBounded()
		return
	}
	reflected := reflect.ValueOf(err)
	if nilDiagnosticErrorValue(reflected) {
		f.add(typeSummary(err))
		return
	}
	if reflected.Comparable() {
		if _, ok := f.seen[err]; ok {
			return
		}
		f.seen[err] = struct{}{}
	}

	switch value := err.(type) {
	case *os.PathError:
		f.addOperation("path operation", value.Op)
		f.visit(value.Err, depth+1)
		return
	case *os.LinkError:
		f.addOperation("link operation", value.Op)
		f.visit(value.Err, depth+1)
		return
	case *os.SyscallError:
		f.addOperation("system call", value.Syscall)
		f.visit(value.Err, depth+1)
		return
	case *url.Error:
		f.addOperation("URL operation", value.Op)
		f.visit(value.Err, depth+1)
		return
	case *net.OpError:
		f.addOperation("network operation", value.Op)
		f.visit(value.Err, depth+1)
		return
	case *exec.ExitError, *exec.Error:
		f.add("command execution failed")
		return
	case protocol.APIError:
		f.add(apiErrorSummary(value.Code))
		return
	case *protocol.APIError:
		if value == nil {
			f.add("api error")
		} else {
			f.add(apiErrorSummary(value.Code))
		}
		return
	}
	if summary, ok := sensitiveTypeSummary(err); ok {
		f.add(summary)
		return
	}

	switch value := err.(type) {
	case interface{ Unwrap() []error }:
		children := value.Unwrap()
		if len(children) == 0 {
			f.add(typeSummary(err))
			return
		}
		for _, child := range children {
			if f.nodes >= diagnosticMaxNodes {
				f.markBounded()
				break
			}
			f.visit(child, depth+1)
			if f.bounded {
				break
			}
		}
		return
	case interface{ Unwrap() error }:
		if child := value.Unwrap(); child != nil {
			f.visit(child, depth+1)
			return
		}
	}

	f.add(knownLeafSummary(err))
}

func nilDiagnosticErrorValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (f *diagnosticFormatter) addOperation(kind, operation string) {
	if f.redactor == nil {
		f.add(kind + " failed")
		return
	}
	operation = f.clean(operation)
	if operation == "" {
		f.add(kind + " failed")
		return
	}
	f.parts = append(f.parts, truncateDiagnostic(kind+" "+operation, diagnosticMaxBytes))
}

func (f *diagnosticFormatter) add(text string) {
	if text = f.clean(text); text != "" {
		f.parts = append(f.parts, truncateDiagnostic(text, diagnosticMaxBytes))
	}
}

func (f *diagnosticFormatter) clean(text string) string {
	text = strings.ToValidUTF8(text, "�")
	if f.redactor != nil {
		text = f.redactor.String(text)
	}
	text = pathTokenPattern.ReplaceAllString(text, "[path]")
	text = strings.NewReplacer("\r", " ", "\n", " ").Replace(text)
	return strings.TrimSpace(text)
}

func (f *diagnosticFormatter) markBounded() {
	if f.bounded {
		return
	}
	f.bounded = true
	f.add("diagnostic graph truncated")
}

func sensitiveTypeSummary(err error) (string, bool) {
	typeOf := reflect.TypeOf(err)
	if typeOf == nil {
		return "error", true
	}
	for typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}
	switch typeOf.PkgPath() {
	case "go.yaml.in/yaml/v3", "gopkg.in/yaml.v2", "gopkg.in/yaml.v3", "encoding/json":
		return "configuration parse error", true
	case "net/url":
		return "URL parse error", true
	case "os/exec":
		return "command execution failed", true
	}
	return "", false
}

func knownLeafSummary(err error) string {
	if !reflect.ValueOf(err).Comparable() {
		return typeSummary(err)
	}
	switch err {
	case context.Canceled:
		return "operation canceled"
	case context.DeadlineExceeded:
		return "operation deadline exceeded"
	case os.ErrInvalid:
		return "invalid operation"
	case os.ErrPermission:
		return "permission denied"
	case os.ErrExist:
		return "file already exists"
	case os.ErrNotExist:
		return "file does not exist"
	case os.ErrClosed:
		return "file already closed"
	case os.ErrDeadlineExceeded:
		return "I/O deadline exceeded"
	case io.EOF:
		return "end of input"
	case io.ErrUnexpectedEOF:
		return "unexpected end of input"
	case io.ErrNoProgress:
		return "I/O made no progress"
	case io.ErrShortBuffer:
		return "short buffer"
	case io.ErrShortWrite:
		return "short write"
	case io.ErrClosedPipe:
		return "closed pipe"
	}
	if errno, ok := err.(syscall.Errno); ok {
		return "system error: " + errno.Error()
	}
	return typeSummary(err)
}

func typeSummary(err error) string {
	typeOf := reflect.TypeOf(err)
	if typeOf == nil {
		return "error"
	}
	return "error (" + typeOf.String() + ")"
}

func apiErrorSummary(code protocol.ErrorCode) string {
	if !knownAPIErrorCode(code) {
		return "api error"
	}
	return "api error (" + string(code) + ")"
}

func knownAPIErrorCode(code protocol.ErrorCode) bool {
	switch code {
	case protocol.CodeInvalidArgument,
		protocol.CodeDaemonUnavailable,
		protocol.CodeInvalidState,
		protocol.CodePermissionDenied,
		protocol.CodeRevisionConflict,
		protocol.CodeUpstreamFailure,
		protocol.CodeNetworkFailure,
		protocol.CodeDataFailure,
		protocol.CodeInternal,
		protocol.CodeManagedField,
		protocol.CodeManagedOperation,
		protocol.CodeUnsupportedMutation,
		protocol.CodeSystemProxyConflict,
		protocol.CodeSystemProxyNotOwned,
		protocol.CodeTunConflict:
		return true
	default:
		return false
	}
}

func truncateDiagnostic(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	text = text[:limit]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}
