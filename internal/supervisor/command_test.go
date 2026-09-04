package supervisor

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/logging"
)

const commandHelperEnv = "MIHARI_COMMAND_HELPER"

func init() {
	switch os.Getenv(commandHelperEnv) {
	case "partial-stdout":
		_, _ = os.Stdout.Write([]byte("partial"))
		os.Exit(0)
	case "full-stdout":
		_, _ = os.Stdout.Write([]byte("full-line\n"))
		os.Exit(0)
	}
}

func TestCommandArgumentsUseManagedDataAndConfig(t *testing.T) {
	want := []string{"-d", "/managed/data", "-f", "/managed/config.yaml"}
	if got := commandArguments("/managed/data", "/managed/config.yaml"); !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%q want=%q", got, want)
	}
}

func TestCommandStarter_FlushesCaptureOnWait(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	level := &slog.LevelVar{}
	logger := slog.New(logging.NewJSONHandler(&buf, level, "mihomo", logging.NewRedactor()))
	capture := logging.NewLineCaptureWriter(logger, slog.LevelInfo, "stdout")
	t.Cleanup(func() { _ = capture.Close() })
	starter := CommandStarter{
		BinaryPath: bin,
		DataDir:    t.TempDir(),
		ConfigPath: filepath.Join(t.TempDir(), "config.yaml"),
		Stdout:     capture,
		Stderr:     io.Discard,
	}

	t.Setenv(commandHelperEnv, "partial-stdout")
	child, err := starter.Start()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	first := parseHelperJSONL(t, buf.String())
	if len(first) != 1 || first[0]["msg"] != "partial" {
		t.Fatalf("after first Wait records=%v", first)
	}

	t.Setenv(commandHelperEnv, "full-stdout")
	child, err = starter.Start()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	second := parseHelperJSONL(t, buf.String())
	if len(second) != 2 || second[0]["msg"] != "partial" || second[1]["msg"] != "full-line" {
		t.Fatalf("after second Wait records=%v", second)
	}
}

func parseHelperJSONL(t *testing.T, raw string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	out := make([]map[string]any, 0, len(lines))
	for i, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d json: %v in %q", i, err, line)
		}
		out = append(out, rec)
	}
	return out
}
