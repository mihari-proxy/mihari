package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mihari-proxy/mihari/internal/cli"
	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/update"
)

// Keep CLI's current process executable out of the test. All preparation,
// consent validation, filesystem replacement and JSON rendering remain real.
type replacementCLIUpdater struct {
	updater      update.SelfUpdater
	binary       string
	afterPrepare func()
	candidate    string
	applies      int
}

func (u *replacementCLIUpdater) Prepare(ctx context.Context, _, _, channel string) (update.PreparedUpdate, error) {
	p, err := u.updater.Prepare(ctx, u.binary, "v2.0.0-dev.1", channel)
	u.candidate = p.CandidatePath
	if err == nil && u.afterPrepare != nil {
		u.afterPrepare()
	}
	return p, err
}
func (u *replacementCLIUpdater) ApplyPrepared(ctx context.Context, p update.PreparedUpdate) (update.Result, error) {
	u.applies++
	return u.updater.ApplyPrepared(ctx, p)
}

func TestReplacementConfirmation_CLIUsesVerifiedCandidate(t *testing.T) {
	for _, scenario := range []string{"no consent", "confirmed fixed candidate", "confirmed stale target"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("MIHARI_DATA", t.TempDir())
			elevate.SetChecker(func() bool { return true })
			t.Cleanup(func() { elevate.SetChecker(nil) })
			binary := filepath.Join(t.TempDir(), platform.InstalledBinaryName())
			old := []byte("installed v2 fixture")
			if err := os.WriteFile(binary, old, 0700); err != nil {
				t.Fatal(err)
			}
			payload := []byte("verified v1.9.0 fixture")
			asset := "mihari-" + runtime.GOOS + "-" + runtime.GOARCH
			if runtime.GOOS == "windows" {
				asset += ".exe"
			}
			digest := sha256.Sum256(payload)
			checksum := hex.EncodeToString(digest[:]) + "  " + asset + "\n"
			var latestChanged atomic.Bool
			var latestRequests atomic.Int32
			var release *httptest.Server
			release = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/mihari-proxy/mihari/releases/latest", "/repos/mihari-proxy/mihari/releases/tags/v1.9.0":
					tag := "v1.9.0"
					if strings.HasSuffix(r.URL.Path, "/latest") {
						latestRequests.Add(1)
						if latestChanged.Load() {
							tag = "v1.8.0"
						}
					}
					_ = json.NewEncoder(w).Encode(update.Release{TagName: tag, Assets: []update.Asset{
						{Name: asset, URL: release.URL + "/candidate", Size: int64(len(payload))},
						{Name: "SHA256SUMS.txt", URL: release.URL + "/checksum", Size: int64(len(checksum))},
					}})
				case "/candidate":
					_, _ = w.Write(payload)
				case "/checksum":
					_, _ = w.Write([]byte(checksum))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(release.Close)
			replacements := 0
			u := &replacementCLIUpdater{binary: binary, updater: update.SelfUpdater{
				HTTPClient: release.Client(), APIBase: release.URL,
				ObserveTargets: func(ctx context.Context, path string) (update.ReplacementSnapshot, error) {
					file, err := platform.ObserveReplacementFile(ctx, path)
					if err != nil {
						return update.ReplacementSnapshot{}, err
					}
					// Fixture bytes stand in for a known version; actual identity/digest
					// observation and all subsequent risk/commit checks are production.
					return update.ReplacementSnapshot{Targets: []update.ReplacementTarget{{Roles: []string{"binary"}, Path: file.Path, FileID: file.FileID, SHA256: file.SHA256, Exists: file.Exists, Version: "v2.0.0-dev.1"}}}, nil
				},
				AfterReplace: func(context.Context, string) error { replacements++; return nil },
			}}
			u.afterPrepare = func() {
				latestChanged.Store(true)
				if scenario == "confirmed stale target" {
					if err := os.WriteFile(binary, []byte("concurrent installation"), 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			args := []string{"self", "update", "--json"}
			if scenario != "no consent" {
				args = append(args, "--yes")
			}
			var stdout, stderr bytes.Buffer
			exit := cli.Execute(context.Background(), args, &stdout, &stderr, cli.Dependencies{SelfUpdater: u, SelfUpdateChannel: func(context.Context) (string, error) { return "main", nil }})
			actual, err := os.ReadFile(binary)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "confirmed fixed candidate" {
				if exit != cli.ExitOK || !bytes.Equal(actual, payload) || replacements != 1 || u.applies != 1 {
					t.Fatalf("confirmed replacement: exit=%d replaces=%d applies=%d stderr=%s", exit, replacements, u.applies, stderr.String())
				}
				var result struct {
					Version string `json:"version"`
					Updated bool   `json:"updated"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Version != "v1.9.0" || !result.Updated {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			} else {
				if exit == cli.ExitOK || replacements != 0 || stdout.Len() != 0 {
					t.Fatalf("rejection: exit=%d replacements=%d", exit, replacements)
				}
				var envelope map[string]json.RawMessage
				if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
					t.Fatalf("single error envelope lost: %v", err)
				}
				if scenario == "no consent" && (exit != cli.ExitUsage || u.applies != 0 || !bytes.Equal(actual, old)) {
					t.Fatal("unconfirmed command reached replacement")
				}
				if scenario == "confirmed stale target" && string(actual) != "concurrent installation" {
					t.Fatal("stale consent overwrote the changed target")
				}
			}
			for _, warning := range []string{"settings", "subscriptions", "data loss", "does not roll back disk state"} {
				if !strings.Contains(stderr.String(), warning) {
					t.Fatalf("missing risk text %q", warning)
				}
			}
			if latestRequests.Load() != 1 {
				t.Fatal("apply queried mutable latest again")
			}
			if _, err := os.Stat(u.candidate); !os.IsNotExist(err) {
				t.Fatalf("prepared candidate leaked: %v", err)
			}
		})
	}
}
