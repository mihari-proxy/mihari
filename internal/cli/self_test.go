package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/update"
)

type fakeSelfUpdater struct {
	calls       int
	prepares    int
	prepared    update.PreparedUpdate
	consent     update.ReplacementConsent
	lastChannel string
	result      update.Result
	err         error
	applyErr    error
}

func (f *fakeSelfUpdater) Prepare(_ context.Context, _, _, channel string) (update.PreparedUpdate, error) {
	f.prepares++
	f.lastChannel = channel
	return f.prepared, f.err
}
func (f *fakeSelfUpdater) ApplyPrepared(_ context.Context, p update.PreparedUpdate) (update.Result, error) {
	f.calls++
	f.consent = p.Consent
	return f.result, f.applyErr
}

func TestSelfUpdate_ReplacementConfirmation(t *testing.T) {
	for _, current := range []string{"v3.0.0", "unknown", "v1.0.0"} {
		for _, yes := range []bool{false, true} {
			t.Run(current+fmt.Sprint(yes), func(t *testing.T) {
				t.Setenv("MIHARI_DATA", t.TempDir())
				previous := elevate.Check
				t.Cleanup(func() { elevate.Check = previous })
				elevate.Check = func() bool { return true }
				preview, err := update.NewReplacementPreview(update.ReplacementCandidate{Version: "v2.0.0", SHA256: strings.Repeat("a", 64), Channel: "main"}, update.ReplacementSnapshot{Targets: []update.ReplacementTarget{{Roles: []string{"binary", "service"}, Path: "/private/token-secret", Exists: true, Version: current}}})
				if err != nil {
					t.Fatal(err)
				}
				fake := &fakeSelfUpdater{prepared: update.PreparedUpdate{Available: true, Version: "v2.0.0", Channel: "main", Preview: preview}, result: update.Result{Updated: true, Version: "v2.0.0", Channel: "main"}}
				args := []string{"self", "update", "--json"}
				if yes {
					args = append(args, "--yes")
				}
				var out, stderr bytes.Buffer
				code := Execute(context.Background(), args, &out, &stderr, Dependencies{SelfUpdater: fake})
				risky := preview.Risk != update.ReplacementNone
				if risky && !yes {
					if code != ExitUsage || fake.calls != 0 || out.Len() != 0 {
						t.Fatalf("code=%d calls=%d out=%q err=%q", code, fake.calls, out.String(), stderr.String())
					}
					var envelope struct {
						Error struct {
							Code    string                     `json:"code"`
							Message string                     `json:"message"`
							Details map[string]json.RawMessage `json:"details"`
						} `json:"error"`
					}
					if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
						t.Fatal(err)
					}
					if envelope.Error.Code != "invalid_argument" || !strings.Contains(envelope.Error.Message, "does not roll back disk state") {
						t.Fatalf("error=%s", stderr.String())
					}
					for _, key := range []string{"reason", "risk", "targets", "target_version", "preview_id"} {
						if len(envelope.Error.Details[key]) == 0 {
							t.Fatalf("missing %s: %s", key, stderr.String())
						}
					}
				} else {
					if code != ExitOK || fake.calls != 1 || fake.consent.Yes != yes || !json.Valid(out.Bytes()) {
						t.Fatalf("code=%d calls=%d consent=%+v out=%q err=%q", code, fake.calls, fake.consent, out.String(), stderr.String())
					}
					if risky && !strings.Contains(stderr.String(), update.ReplacementWarning(preview)) {
						t.Fatalf("missing warning: %q", stderr.String())
					}
					if !risky && stderr.Len() != 0 {
						t.Fatalf("unexpected warning: %q", stderr.String())
					}
				}
				if strings.Contains(out.String()+stderr.String(), "token-secret") {
					t.Fatal("private path exposed")
				}
			})
		}
	}
}

func TestSelfChannelShowDefaultsMain(t *testing.T) {
	t.Setenv("MIHARI_DATA", t.TempDir())
	stdout := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"self", "channel", "--json"}, stdout, &bytes.Buffer{}, Dependencies{})
	if exit != ExitOK || !strings.Contains(stdout.String(), `"channel":"main"`) {
		t.Fatalf("exit=%d stdout=%q", exit, stdout)
	}
}

func TestSelfChannelSetDev(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MIHARI_DATA", root)
	if Execute(context.Background(), []string{"self", "channel", "dev"}, &bytes.Buffer{}, &bytes.Buffer{}, Dependencies{}) != ExitOK {
		t.Fatal("set")
	}
	raw, _ := os.ReadFile(filepath.Join(root, "mihari-channel"))
	if string(raw) != "dev\n" {
		t.Fatalf("raw=%q", raw)
	}
}

func TestSelfChannelRejectsStable(t *testing.T) {
	t.Setenv("MIHARI_DATA", t.TempDir())
	if Execute(context.Background(), []string{"self", "channel", "stable"}, &bytes.Buffer{}, &bytes.Buffer{}, Dependencies{}) != ExitUsage {
		t.Fatal("want usage")
	}
}

func TestSelfChannelInvalidFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MIHARI_DATA", root)
	_ = os.WriteFile(filepath.Join(root, "mihari-channel"), []byte("stable\n"), 0o600)
	if Execute(context.Background(), []string{"self", "channel"}, &bytes.Buffer{}, &bytes.Buffer{}, Dependencies{}) != ExitData {
		t.Fatal("want data")
	}
}

func TestSelfChannelDoesNotRequireElevation(t *testing.T) {
	t.Setenv("MIHARI_DATA", t.TempDir())
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return false }
	if Execute(context.Background(), []string{"self", "channel", "dev"}, &bytes.Buffer{}, &bytes.Buffer{}, Dependencies{}) != ExitOK {
		t.Fatal("channel should not require elevation")
	}
}

func TestSelfUpdateJSONIncludesAhead(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	root := t.TempDir()
	t.Setenv("MIHARI_DATA", root)
	_ = os.WriteFile(filepath.Join(root, "mihari-channel"), []byte("dev\n"), 0o600)
	fake := &fakeSelfUpdater{result: update.Result{Version: "v0.8.2", Updated: false, Ahead: true, Channel: "dev"}}
	stdout := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"self", "update", "--json"}, stdout, &bytes.Buffer{}, Dependencies{SelfUpdater: fake})
	if exit != ExitOK || fake.calls != 1 || fake.lastChannel != "dev" || !strings.Contains(stdout.String(), `"ahead":true`) {
		t.Fatalf("exit=%d stdout=%q calls=%d channel=%q", exit, stdout, fake.calls, fake.lastChannel)
	}
}

func TestSelfUpdateAheadText(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	root := t.TempDir()
	t.Setenv("MIHARI_DATA", root)
	_ = os.WriteFile(filepath.Join(root, "mihari-channel"), []byte("main\n"), 0o600)
	fake := &fakeSelfUpdater{result: update.Result{Version: "v0.8.2", Updated: false, Ahead: true, Channel: "main"}}
	stdout := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"self", "update"}, stdout, &bytes.Buffer{}, Dependencies{SelfUpdater: fake})
	if exit != ExitOK || !strings.Contains(stdout.String(), "current ") || !strings.Contains(stdout.String(), "is ahead of main v0.8.2") {
		t.Fatalf("exit=%d stdout=%q", exit, stdout)
	}
}

func TestSelfUpdateInvalidFileDoesNotCallUpdater(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	root := t.TempDir()
	t.Setenv("MIHARI_DATA", root)
	_ = os.WriteFile(filepath.Join(root, "mihari-channel"), []byte("stable\n"), 0o600)
	fake := &fakeSelfUpdater{}
	if Execute(context.Background(), []string{"self", "update"}, &bytes.Buffer{}, &bytes.Buffer{}, Dependencies{SelfUpdater: fake}) != ExitData {
		t.Fatal("want data")
	}
	if fake.calls != 0 {
		t.Fatalf("calls=%d", fake.calls)
	}
}

func TestSelfUpdateRequiresElevation(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return false }
	fake := &fakeSelfUpdater{}
	exit := Execute(context.Background(), []string{"self", "update", "--json"}, &bytes.Buffer{}, &bytes.Buffer{}, Dependencies{SelfUpdater: fake})
	if exit != ExitPermission || fake.calls != 0 {
		t.Fatalf("exit=%d calls=%d", exit, fake.calls)
	}
}

func TestSelfUpdateWhenElevated(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	fake := &fakeSelfUpdater{result: update.Result{Version: "v2.0.0", Updated: true}}
	stdout := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"self", "update", "--json"}, stdout, &bytes.Buffer{}, Dependencies{SelfUpdater: fake})
	if exit != ExitOK || fake.calls != 1 || !strings.Contains(stdout.String(), `"updated":true`) {
		t.Fatalf("exit=%d stdout=%q calls=%d", exit, stdout, fake.calls)
	}
}

func TestSelfVersionJSON(t *testing.T) {
	stdout := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"self", "version", "--json"}, stdout, &bytes.Buffer{}, Dependencies{})
	if exit != ExitOK || !strings.Contains(stdout.String(), `"version"`) {
		t.Fatalf("exit=%d stdout=%q", exit, stdout)
	}
}

func TestSelfUpdate_ConfirmedApplyFailureJSON(t *testing.T) {
	t.Setenv("MIHARI_DATA", t.TempDir())
	previous := elevate.Check
	t.Cleanup(func() { elevate.Check = previous })
	elevate.Check = func() bool { return true }
	preview, err := update.NewReplacementPreview(update.ReplacementCandidate{Version: "v1.0.0"}, update.ReplacementSnapshot{Targets: []update.ReplacementTarget{{Roles: []string{"binary"}, Exists: true, Version: "v2.0.0"}}})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeSelfUpdater{prepared: update.PreparedUpdate{Available: true, Preview: preview}, applyErr: protocol.APIError{Code: protocol.CodeInvalidState, Message: "installation changed; start again"}}
	var out, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"self", "update", "--yes", "--json"}, &out, &stderr, Dependencies{SelfUpdater: fake})
	if code != ExitInvalidState || fake.prepares != 1 || fake.calls != 1 || !fake.consent.Yes || out.Len() != 0 || !json.Valid(stderr.Bytes()) || !strings.Contains(stderr.String(), "does not roll back disk state") || !strings.Contains(stderr.String(), "installation changed") {
		t.Fatalf("code=%d calls=%d out=%q err=%q", code, fake.calls, out.String(), stderr.String())
	}
}

func TestSelfUpdate_PartialFailureRetainsErrorJSONContract(t *testing.T) {
	t.Setenv("MIHARI_DATA", t.TempDir())
	previous := elevate.Check
	t.Cleanup(func() { elevate.Check = previous })
	elevate.Check = func() bool { return true }
	fake := &fakeSelfUpdater{
		result:   update.Result{Version: "v1.0.0", Updated: true},
		applyErr: protocol.APIError{Code: protocol.CodeInvalidState, Message: "Mihari updated, but the installed service could not be synchronized"},
	}
	var out, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"self", "update", "--yes", "--json"}, &out, &stderr, Dependencies{SelfUpdater: fake})
	if code != ExitInvalidState || out.Len() != 0 || !json.Valid(stderr.Bytes()) || !strings.Contains(stderr.String(), "Mihari updated, but") {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), stderr.String())
	}
}

func TestSelfUpdate_InvalidArgsDoNotPrepare(t *testing.T) {
	for _, args := range [][]string{{"self", "update", "extra"}, {"self", "update", "--yes=invalid"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Setenv("MIHARI_DATA", t.TempDir())
			fake := &fakeSelfUpdater{}
			code := Execute(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, Dependencies{SelfUpdater: fake})
			if code != ExitUsage || fake.prepares != 0 || fake.calls != 0 {
				t.Fatalf("code=%d prepares=%d calls=%d", code, fake.prepares, fake.calls)
			}
		})
	}
}
