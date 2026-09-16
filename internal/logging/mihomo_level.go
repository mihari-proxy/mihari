package logging

import (
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
)

var mihomoANSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
var mihomoTextLevel = regexp.MustCompile(`^(?:time="(?:\\.|[^"\\])*"\s+)?level=([a-z]+)(?:\s|$)`)

// NewMihomoCaptureWriter recognizes core severity while preserving raw messages.
func NewMihomoCaptureWriter(logger *slog.Logger, fallback slog.Level, stream string) LineCaptureWriter {
	return &lineCaptureWriter{logger: logger, level: fallback, stream: stream, resolveLevel: mihomoLineLevel, buf: make([]byte, 0, MaxCaptureLineBytes)}
}

func mihomoLineLevel(message string, fallback slog.Level, truncated bool) slog.Level {
	view := mihomoANSI.ReplaceAllString(message, "")
	var level string
	if strings.HasPrefix(view, "{") {
		var record struct {
			Level string `json:"level"`
		}
		if json.Unmarshal([]byte(view), &record) == nil {
			level = record.Level
		} else if truncated {
			level = mihomoJSONPrefixLevel(view)
		}
	} else if match := mihomoTextLevel.FindStringSubmatch(view); len(match) == 2 {
		level = match[1]
	}
	switch level {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "fatal", "panic":
		return slog.LevelError
	default:
		return fallback
	}
}

// A capped JSON record may lose its closing quote/braces. Only accept a
// complete top-level level value found before the incomplete part; never search
// strings or nested objects for text that resembles a severity field.
func mihomoJSONPrefixLevel(view string) string {
	decoder := json.NewDecoder(strings.NewReader(view))
	if start, err := decoder.Token(); err != nil || start != json.Delim('{') {
		return ""
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return ""
		}
		if key == "level" {
			var value string
			if decoder.Decode(&value) == nil {
				return value
			}
			return ""
		}
		var ignored json.RawMessage
		if decoder.Decode(&ignored) != nil {
			return ""
		}
	}
	return ""
}
