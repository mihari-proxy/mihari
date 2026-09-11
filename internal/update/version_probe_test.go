package update

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

type versionRunnerFunc func(context.Context, string, string) ([]byte, error)

func (f versionRunnerFunc) RunVersion(c context.Context, e, d string) ([]byte, error) {
	return f(c, e, d)
}

func TestVersionProbe_JSON(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"valid", `{"schema":"mihari/v1","version":"1.2.3"}`, "v1.2.3"},
		{"duplicate", `{"schema":"mihari/v1","version":"v1.2.3","version":"v9.0.0"}`, ""},
		{"duplicate schema", `{"schema":"wrong","schema":"mihari/v1","version":"v1.2.3"}`, ""},
		{"wrong schema", `{"schema":"wrong","version":"v1.2.3"}`, ""},
		{"extra object", `{"schema":"mihari/v1","version":"v1.2.3"} {}`, ""},
		{"field", `{"schema":"mihari/v1","version":"v1.2.3","token":"private"}`, ""},
		{"malformed", `{"schema":`, ""},
		{"unknown", `{"schema":"mihari/v1","version":"dirty"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := decodeProbedVersion([]byte(tc.raw)); got != tc.want {
				t.Fatalf("version=%q want=%q", got, tc.want)
			}
		})
	}
}
func TestVersionProbe_ObservesAndNeverElevates(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		exists, trusted, changed bool
		queryErr                 error
	}{
		{name: "trusted", exists: true, trusted: true}, {name: "untrusted", exists: true}, {name: "fresh"},
		{name: "changed", exists: true, trusted: true, changed: true}, {name: "query failed", exists: true, trusted: true, queryErr: errors.New("query failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, queries := 0, 0
			observed := platform.ReplacementFile{Path: filepath.Join(t.TempDir(), "mihari"), Exists: tc.exists, MayExecute: tc.trusted, FileID: "one", SHA256: strings.Repeat("a", 64)}
			observer := func(context.Context, string) (platform.ReplacementFile, error) {
				calls++
				v := observed
				if calls > 1 && tc.changed {
					v.FileID = "two"
				}
				return v, nil
			}
			runner := versionRunnerFunc(func(ctx context.Context, path, dir string) ([]byte, error) {
				queries++
				if path != observed.Path {
					t.Fatal("wrong executable")
				}
				if _, err := os.Stat(dir); err != nil {
					t.Fatal(err)
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 3*time.Second {
					t.Fatal("missing bounded deadline")
				}
				return []byte(`{"schema":"mihari/v1","version":"v1.2.3"}`), tc.queryErr
			})
			got, err := observeReplacementTarget(context.Background(), "binary", observed.Path, runner, observer)
			if tc.changed {
				if err == nil {
					t.Fatal("changed file accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantQueries := 0
			if tc.exists && tc.trusted {
				wantQueries = 1
			}
			if queries != wantQueries || got.Exists != tc.exists {
				t.Fatalf("queries=%d target=%+v", queries, got)
			}
			wantVersion := ""
			if wantQueries == 1 && tc.queryErr == nil {
				wantVersion = "v1.2.3"
			}
			if got.Version != wantVersion {
				t.Fatalf("version=%q", got.Version)
			}
		})
	}
}
func TestVersionProbe_ParentCancellationPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observe := func(context.Context, string) (platform.ReplacementFile, error) {
		return platform.ReplacementFile{Exists: true, MayExecute: true, Path: "/fixture"}, nil
	}
	runner := versionRunnerFunc(func(c context.Context, _, _ string) ([]byte, error) { cancel(); <-c.Done(); return nil, c.Err() })
	_, err := observeReplacementTarget(ctx, "binary", "/fixture", runner, observe)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
func TestVersionProbe_NativeRunner(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "versionprobe")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(goBin, "build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w -X main.version=v1.2.3", "-o", exe, "./testdata/versionprobe")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN="+runtime.Version())
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, raw)
	}
	for _, mode := range []string{"version", "stdout-overflow", "stderr-overflow", "wait"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			data := filepath.Join(dir, "data")
			if err := os.Mkdir(data, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(data, "probe-mode"), []byte(mode), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			raw, err := (ExecVersionRunner{}).RunVersion(ctx, exe, dir)
			if mode == "version" {
				if err != nil || decodeProbedVersion(raw) != "v1.2.3" {
					t.Fatalf("version query: %s %v", raw, err)
				}
			} else if err == nil {
				t.Fatalf("%s accepted", mode)
			}
		})
	}
}
