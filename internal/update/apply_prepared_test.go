package update

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

func preparedFixture(t *testing.T) (*selfUpdateEnv, PreparedUpdate) {
	t.Helper()
	payload := []byte("prepared")
	env := startSelfUpdateEnv(t, selfUpdateServerConfig{checksumBody: fixtureSHA256Hex(payload) + "  mihari-linux-amd64\n", binaryBody: payload})
	p, err := env.updater.Prepare(context.Background(), env.binaryPath, "v1.0.0", ChannelMain)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	return env, p
}
func TestApplyPrepared_ChangedCandidateDoesNotReplace(t *testing.T) {
	env, p := preparedFixture(t)
	before, err := os.ReadFile(env.binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.CandidatePath, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := env.updater.ApplyPrepared(context.Background(), p)
	after, readErr := os.ReadFile(env.binaryPath)
	if err == nil || result.Updated || readErr != nil || !bytes.Equal(before, after) || env.afterReplace {
		t.Fatalf("changed candidate accepted: updated=%v err=%v", result.Updated, err)
	}
}
func TestApplyPrepared_ChangedTargetDoesNotReplace(t *testing.T) {
	env, p := preparedFixture(t)
	if err := os.WriteFile(env.binaryPath, []byte("changed"), 0755); err != nil {
		t.Fatal(err)
	}
	result, err := env.updater.ApplyPrepared(context.Background(), p)
	after, readErr := os.ReadFile(env.binaryPath)
	if err == nil || result.Updated || readErr != nil || string(after) != "changed" || env.afterReplace {
		t.Fatalf("changed target accepted: updated=%v err=%v", result.Updated, err)
	}
}
func TestApplyPrepared_FixedCandidateAndCompletion(t *testing.T) {
	env, p := preparedFixture(t)
	// Any attempt to query latest or redownload now fails.
	env.updater.APIBase = "invalid://latest-changed"
	expected := errors.New("service synchronization failed")
	calls := 0
	env.updater.AfterReplacePrepared = func(_ context.Context, got PreparedUpdate) error {
		calls++
		if got.Version != p.Version || got.Preview.ID != p.Preview.ID {
			t.Error("completion received different candidate")
		}
		return expected
	}
	result, err := env.updater.ApplyPrepared(context.Background(), p)
	after, readErr := os.ReadFile(env.binaryPath)
	if !errors.Is(err, expected) || !result.Updated || result.Version != p.Version || calls != 1 || env.afterReplace || readErr != nil || string(after) != "prepared" {
		t.Fatalf("fixed candidate/completion: result=%+v calls=%d err=%v", result, calls, err)
	}
}
func TestSelfPrepare_BindsTargetAndPreview(t *testing.T) {
	env, p := preparedFixture(t)
	if p.Preview.ID == "" || len(p.Preview.Snapshot.Targets) != 1 || p.Preview.Candidate.SHA256 != p.SHA256 || p.TargetPath != env.binaryPath {
		t.Fatal("preparation did not bind candidate and target")
	}
}

func TestApplyPrepared_UnknownRequiresConsent(t *testing.T) {
	env, p := preparedFixture(t)
	env.updater.ObserveTargets = nil
	var err error
	p.Preview.Snapshot, err = env.updater.observeReplacement(context.Background(), p.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	p.Preview, err = NewReplacementPreview(p.Preview.Candidate, p.Preview.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	result, err := env.updater.ApplyPrepared(context.Background(), p)
	if err == nil || result.Updated || env.afterReplace {
		t.Fatalf("unknown accepted: %+v %v", result, err)
	}
	p.Consent.Yes = true
	result, err = env.updater.ApplyPrepared(context.Background(), p)
	if err != nil || !result.Updated || !env.afterReplace {
		t.Fatalf("confirmed unknown failed: %+v %v", result, err)
	}
}
func TestApplyPrepared_ReplacementFailureDoesNotComplete(t *testing.T) {
	env, p := preparedFixture(t)
	// A directory dot entry cannot be moved to a file or stashed by platform rename.
	if err := os.Remove(env.binaryPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(env.binaryPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.binaryPath+"/child", []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	p.TargetPath = env.binaryPath + string(os.PathSeparator) + "."
	// Freeze the observer to isolate the replace boundary from file observation errors.
	env.updater.ObserveTargets = func(context.Context, string) (ReplacementSnapshot, error) { return p.Preview.Snapshot, nil }
	result, err := env.updater.ApplyPrepared(context.Background(), p)
	if err == nil || result.Updated || env.afterReplace {
		t.Fatalf("replace failure lost: %+v %v", result, err)
	}
}
func TestApplyPrepared_CanceledDoesNotReplace(t *testing.T) {
	env, p := preparedFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := env.updater.ApplyPrepared(ctx, p)
	if !errors.Is(err, context.Canceled) || result.Updated || env.afterReplace {
		t.Fatalf("canceled replacement: %+v %v", result, err)
	}
}
func TestApplyPrepared_NoCandidateDoesNotObserve(t *testing.T) {
	u := SelfUpdater{ObserveTargets: func(context.Context, string) (ReplacementSnapshot, error) {
		t.Fatal("observed no-op")
		return ReplacementSnapshot{}, nil
	}}
	result, err := u.ApplyPrepared(context.Background(), PreparedUpdate{Version: "v9.9.9", Channel: ChannelMain, Ahead: true})
	if err != nil || result.Updated || !result.Ahead {
		t.Fatalf("no-op: %+v %v", result, err)
	}
}

func TestApplyPrepared_LegacyCompletionFailurePreservesUpdated(t *testing.T) {
	env, p := preparedFixture(t)
	expected := errors.New("legacy synchronization failed")
	env.updater.AfterReplace = func(context.Context, string) error { return expected }
	result, err := env.updater.ApplyPrepared(context.Background(), p)
	after, readErr := os.ReadFile(env.binaryPath)
	if !errors.Is(err, expected) || !result.Updated || readErr != nil || string(after) != "prepared" {
		t.Fatalf("partial success: %+v %v read=%v", result, err, readErr)
	}
}

func TestApplyPrepared_ChangeDuringStagingDoesNotReplace(t *testing.T) {
	for _, change := range []string{"target", "staged candidate"} {
		t.Run(change, func(t *testing.T) {
			env, p := preparedFixture(t)
			env.updater.openCandidate = func(path string) (io.WriteCloser, error) {
				f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if err != nil {
					return nil, err
				}
				return &stagingMutationWriter{WriteCloser: f, mutate: func() {
					if change == "target" {
						if err := os.WriteFile(p.TargetPath, []byte("changed target"), 0755); err != nil {
							t.Error(err)
						}
					}
				}, tamper: change == "staged candidate"}, nil
			}
			result, err := env.updater.ApplyPrepared(context.Background(), p)
			after, readErr := os.ReadFile(p.TargetPath)
			want := oldBinaryContent
			if change == "target" {
				want = "changed target"
			}
			if err == nil || result.Updated || env.afterReplace || readErr != nil || string(after) != want {
				t.Fatalf("staging change accepted: result=%+v err=%v", result, err)
			}
		})
	}
}

type stagingMutationWriter struct {
	io.WriteCloser
	mutate func()
	tamper bool
}

func (w *stagingMutationWriter) Write(p []byte) (int, error) {
	w.mutate()
	if w.tamper {
		p = bytes.Repeat([]byte("x"), len(p))
	}
	return w.WriteCloser.Write(p)
}

func TestApplyPrepared_DerivesRiskFromBoundSnapshot(t *testing.T) {
	env, p := preparedFixture(t)
	p.Preview.Snapshot.Targets[0].Version = "v10.0.0"
	var err error
	p.Preview, err = NewReplacementPreview(p.Preview.Candidate, p.Preview.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	env.updater.ObserveTargets = func(context.Context, string) (ReplacementSnapshot, error) { return p.Preview.Snapshot, nil }
	p.Preview.Risk = ReplacementNone
	result, err := env.updater.ApplyPrepared(context.Background(), p)
	if err == nil || result.Updated || env.afterReplace {
		t.Fatalf("unbound risk accepted: %+v %v", result, err)
	}
}
