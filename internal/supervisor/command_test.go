package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/mihari-proxy/mihari/internal/core"
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

func TestCommandStarter_TrustedFactoryRejectsBeforeStart(t *testing.T) {
	calls := 0
	s := CommandStarter{BinaryPath: "must-never-execute", CommandFactory: func(context.Context) (core.CoreCommand, func() error, error) {
		calls++
		return core.CoreCommand{}, nil, errors.New("invalid provenance")
	}}
	if child, e := s.Start(); e == nil || child != nil || calls != 1 {
		t.Fatal("invalid factory reached child start")
	}
}
func TestCommandStarter_TrustedEnvironmentAndSelectedConfig(t *testing.T) {
	t.Setenv("LD_PRELOAD", "hostile")
	t.Setenv("DYLD_INSERT_LIBRARIES", "hostile")
	t.Setenv("SAFE_PATHS", "/")
	closed := 0
	s := CommandStarter{BinaryPath: "legacy-must-not-run", CommandFactory: func(context.Context) (core.CoreCommand, func() error, error) {
		return core.CoreCommand{Binary: "/private/bin/mihomo", Home: "/private/runtime/core-home", Config: "/private/runtime/config.yaml", Args: []string{"-d", "/private/runtime/core-home", "-f", "/private/runtime/config.yaml"}, Env: []string{"PATH=/usr/bin:/bin", "LANG=C"}}, func() error { closed++; return nil }, nil
	}}
	command, release, e := s.command(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if closeErr := release(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if command.Path != "/private/bin/mihomo" || command.Dir != "/private/runtime/core-home" || len(command.Env) != 2 || command.Args[4] != "/private/runtime/config.yaml" || closed != 0 {
		t.Fatal("factory command identity/environment changed")
	}
}
