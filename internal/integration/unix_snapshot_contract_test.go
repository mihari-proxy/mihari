//go:build linux || darwin

package integration

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
)

type staticSnapshotToken struct{ token string }

func (s staticSnapshotToken) Load(context.Context) (string, error) { return s.token, nil }

func TestUnixSnapshotContract_LayoutClientAssembleFixture(t *testing.T) {
	layout, locator := unixSnapshotLayout(t)
	_ = layout
	provider := staticSnapshotToken{token: "token"}
	if controlclient.WithCredentialProvider(locator, provider) == nil {
		t.Fatal("WithCredentialProvider returned nil")
	}

	fixture, err := os.ReadFile(filepath.Join("..", "control", "protocol", "testdata", "machine-snapshot-v1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/logging/snapshot" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fixture)
	}))
	t.Cleanup(srv.Close)

	c := controlclient.NewHTTPWithCredentialProvider(srv.URL, provider, srv.Client())
	to := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	set, err := c.OpenMachineSnapshot(context.Background(), logging.SnapshotWindow{To: to})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := set.Close(); err != nil {
			t.Error(err)
		}
	}()

	req, parent, out := unixAssembleRequest(t)
	result, err := logging.Assemble(context.Background(), req, logging.ExportScopeMachineAndCurrentUser, logging.NamedSourcesFromSet(set), set.Finish)
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != out {
		t.Fatalf("path=%q", result.Path)
	}
	raw := readZipManifest(t, out)
	var manifest map[string]any
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	if manifest["schema"] != "mihari-logs-export/v2" || manifest["scope"] != logging.ExportScopeMachineAndCurrentUser {
		t.Fatalf("manifest=%v", manifest)
	}
	files, _ := manifest["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("files=%v", files)
	}
	file, _ := files[0].(map[string]any)
	if file["lines"] != json.Number("1") || file["skipped_invalid"] != json.Number("0") || file["redacted"] != json.Number("0") {
		t.Fatalf("file=%v", file)
	}
	sources, _ := file["sources"].([]any)
	if len(sources) != 1 || sources[0] != "mihari-daemon.log" {
		t.Fatalf("sources=%v", sources)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 {
		t.Fatalf("parent=%v err=%v", entries, err)
	}
}

func TestUnixSnapshotContract_FakeCompleteAndEarlyEOFNeverPublish(t *testing.T) {
	layout, locator := unixSnapshotLayout(t)
	_ = layout
	provider := staticSnapshotToken{token: "token"}
	_ = controlclient.WithCredentialProvider(locator, provider)

	fixture, err := os.ReadFile(filepath.Join("..", "control", "protocol", "testdata", "machine-snapshot-v1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	truncated := fixture
	if i := strings.LastIndex(string(fixture), "\n{"); i >= 0 {
		truncated = fixture[:i]
	}
	for name, body := range map[string][]byte{
		"early-eof":     truncated,
		"fake-complete": append(append([]byte(nil), fixture...), '\n'),
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/x-ndjson")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(body)
			}))
			t.Cleanup(srv.Close)
			c := controlclient.NewHTTPWithCredentialProvider(srv.URL, provider, srv.Client())
			req, parent, out := unixAssembleRequest(t)
			set, err := c.OpenMachineSnapshot(context.Background(), logging.SnapshotWindow{To: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)})
			if err == nil {
				_, err = logging.Assemble(context.Background(), req, logging.ExportScopeMachineAndCurrentUser, logging.NamedSourcesFromSet(set), set.Finish)
				_ = set.Close()
			}
			if err == nil {
				t.Fatal("invalid stream published")
			}
			if _, statErr := os.Lstat(out); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("published: %v", statErr)
			}
			entries, readErr := os.ReadDir(parent)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("spool remains %v err=%v", entries, readErr)
			}
		})
	}
}

func unixSnapshotLayout(t *testing.T) (platform.ResolvedLayout, platform.ControlLocator) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	data := filepath.Join(root, "private")
	defaults := platform.LayoutDefaults{OS: runtime.GOOS, BaseDir: "/var/lib/mihari", InstallRoot: "/usr/local/lib/mihari", TrustedHome: home, SocketLimit: 107}
	if runtime.GOOS == "darwin" {
		defaults.BaseDir = "/Library/Application Support/mihari"
		defaults.SocketLimit = 103
	}
	layout, err := platform.ResolveLayout(platform.LayoutInput{CWD: root, Data: data, Home: home, EUID: uint32(os.Geteuid())}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	locator, err := layout.Locator(uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	return layout, locator
}

func unixAssembleRequest(t *testing.T) (logging.ExportRequest, string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "data")
	paths := platform.NewPaths(root)
	fs, err := platform.NewPrivateFS(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	out := filepath.Join(parent, "export.zip")
	return logging.ExportRequest{
		Now: time.Now(), Range: logging.ExportRange{Kind: logging.RangeAll}, OutputPath: out,
		Paths:     logging.ExportPaths{LogDir: paths.LogDir, ExportDir: paths.LogExportDir, DaemonLog: paths.DaemonLog, TUILog: paths.TUILog, MihomoLog: paths.MihomoLog},
		PrivateFS: fs, Redactor: logging.NewRedactor(),
	}, parent, out
}

func readZipManifest(t *testing.T, path string) string {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, f := range r.File {
		if f.Name != "manifest.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	t.Fatal("manifest.json missing")
	return ""
}
