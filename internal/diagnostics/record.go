package diagnostics

import (
	"context"
	"log/slog"
	"reflect"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// Record describes one internal diagnostic event. It must not be serialized.
type Record struct {
	Component string
	Event     string
	Level     slog.Level
	Err       error
}

// Reporter records an internal diagnostic event at an owning boundary.
type Reporter func(context.Context, Record)

// FailureLevel classifies an operation failure for diagnostic logging.
func FailureLevel(ctx context.Context, err error) (slog.Level, bool) {
	if err == nil {
		return 0, false
	}

	classification := inspectFailure(err)
	if classification.actual || (!classification.cancellation && !classification.expected) {
		return slog.LevelError, true
	}
	if classification.expected {
		return slog.LevelDebug, true
	}
	if ctx != nil && ctx.Err() != nil {
		return 0, false
	}
	return slog.LevelError, true
}

type failureClassification struct {
	actual       bool
	expected     bool
	cancellation bool
}

func inspectFailure(root error) failureClassification {
	const (
		maxDepth = 32
		maxNodes = 64
	)
	var result failureClassification
	states := make(map[error]failureVisitState)
	work := 0
	var visit func(error, int)
	visit = func(err error, depth int) {
		if result.actual {
			return
		}
		if work >= maxNodes {
			result.actual = true
			return
		}
		work++
		if err == nil {
			return
		}
		if depth > maxDepth {
			result.actual = true
			return
		}
		value := reflect.ValueOf(err)
		if nilErrorValue(value) {
			result.actual = true
			return
		}
		if value.Comparable() {
			switch states[err] {
			case failureVisitActive:
				result.actual = true
				return
			case failureVisitComplete:
				return
			}
			states[err] = failureVisitActive
			defer func() { states[err] = failureVisitComplete }()
		}
		if wrapped, ok := err.(failure); ok {
			if wrapped.cause != nil {
				visit(wrapped.cause, depth+1)
			} else {
				classifyAPIError(wrapped.api, &result)
			}
			return
		}
		if wrapped, ok := err.(*failure); ok && wrapped != nil {
			if wrapped.cause != nil {
				visit(wrapped.cause, depth+1)
			} else {
				classifyAPIError(wrapped.api, &result)
			}
			return
		}

		switch unwrapped := err.(type) {
		case interface{ Unwrap() []error }:
			children := unwrapped.Unwrap()
			if len(children) == 0 {
				result.actual = true
				return
			}
			for _, child := range children {
				if work >= maxNodes {
					result.actual = true
					break
				}
				visit(child, depth+1)
				if result.actual {
					break
				}
			}
			return
		case interface{ Unwrap() error }:
			if child := unwrapped.Unwrap(); child != nil {
				visit(child, depth+1)
				return
			}
		}

		switch value := err.(type) {
		case protocol.APIError:
			classifyAPIError(value, &result)
		case *protocol.APIError:
			if value != nil {
				classifyAPIError(*value, &result)
			} else {
				result.actual = true
			}
		default:
			if err == context.Canceled || err == context.DeadlineExceeded {
				result.cancellation = true
			} else {
				result.actual = true
			}
		}
	}
	visit(root, 1)
	return result
}

type failureVisitState uint8

const (
	failureVisitActive failureVisitState = iota + 1
	failureVisitComplete
)

func nilErrorValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func classifyAPIError(api protocol.APIError, result *failureClassification) {
	if expectedFailureCode(api.Code) {
		result.expected = true
	} else {
		result.actual = true
	}
}

func expectedFailureCode(code protocol.ErrorCode) bool {
	switch code {
	case protocol.CodeInvalidArgument,
		protocol.CodeRevisionConflict,
		protocol.CodeSystemProxyConflict,
		protocol.CodeSystemProxyNotOwned,
		protocol.CodeTunConflict:
		return true
	default:
		return false
	}
}
