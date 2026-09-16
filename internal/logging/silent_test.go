package logging

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
)

func TestJSONHandler_SilentSuppressesAndResumes(t *testing.T) {
	silent, err := ParseLevel("silent")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	var level slog.LevelVar
	level.Set(silent)
	logger := slog.New(NewJSONHandler(&output, &level, "daemon", nil)).With("field", "value").WithGroup("group")
	for _, severity := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError, slog.Level(1000)} {
		logger.Log(context.Background(), severity, "suppressed")
	}
	if output.Len() != 0 {
		t.Fatalf("silent emitted %q", output.String())
	}
	level.Set(slog.LevelInfo)
	logger.Info("resumed")
	if !bytes.Contains(output.Bytes(), []byte("resumed")) {
		t.Fatal("logger did not resume")
	}
}
