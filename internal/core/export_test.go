package core

// This file is compiled only by go test. It exposes a synthetic backend fixture
// to core_test, where the real app/runtime consumers can be exercised without
// exporting production trust constructors or invoking native privileged IO.
import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"os"
	"testing"
)

type TestTrustedFixture struct {
	Trusted *TrustedExecution
	store   *memoryStore
	files   *memoryConfigs
	Execute func(context.Context, CoreCommand) ([]byte, error)
}

func NewTestTrustedFixture(t *testing.T, root string) *TestTrustedFixture {
	t.Helper()
	s := newMemoryStore()
	s.root = root
	seedInstalledReceipt(t, s, "old trusted binary")
	f := &TestTrustedFixture{store: s, files: &memoryConfigs{s: s}}
	f.Trusted = &TrustedExecution{store: s, files: f.files, executor: fixtureExecutor{f}}
	settings := config.Defaults()
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if e := f.Trusted.InitializeConfig(context.Background(), settings, FixturePolicyInput()); e != nil {
		t.Fatal(e)
	}
	return f
}
func FixturePolicyInput() subscription.PolicyInput {
	return subscription.PolicyInput{SubscriptionID: testTransaction, Generation: 1, CoreTag: "v1.19.30", OS: "linux", Arch: "amd64"}
}
func (f *TestTrustedFixture) ExecuteCommand(ctx context.Context, c CoreCommand) ([]byte, error) {
	if f.Execute != nil {
		return f.Execute(ctx, c)
	}
	return []byte("Mihomo v1.19.30"), nil
}

type fixtureExecutor struct{ f *TestTrustedFixture }

func (x fixtureExecutor) Execute(ctx context.Context, c CoreCommand) ([]byte, error) {
	return x.f.ExecuteCommand(ctx, c)
}
func (f *TestTrustedFixture) Content() []byte { b, _ := f.files.read(context.Background()); return b }
func (f *TestTrustedFixture) Binary() []byte {
	// Observe the pair under the same ownership used by a real Start; otherwise
	// synthetic map reads race with post-Commit candidate cleanup.
	release, e := f.store.execution().acquire(context.Background())
	if e != nil {
		return nil
	}
	defer release()
	b, _ := f.store.Load(context.Background(), InstalledBinary, "")
	return b
}
func (f *TestTrustedFixture) FailPublicationAndRecovery() { f.files.failWriteFrom = f.files.writes + 1 }
func (f *TestTrustedFixture) Prepare(ctx context.Context, _ InstallRequest) (PreparedCore, error) {
	a, e := supportedCore(ctx, "linux", "amd64", "v1.19.30", "stable")
	if e != nil {
		return nil, e
	}
	return f.Trusted.Installer().stageTrustedBinary(ctx, a, []byte("new authenticated binary"))
}
func (f *TestTrustedFixture) DetectVersion(ctx context.Context, path string) (string, error) {
	return f.Trusted.Installer().DetectVersion(ctx, path)
}

// InterruptPair leaves a real prepared WAL with an incomplete new publication.
func (f *TestTrustedFixture) InterruptPair(t *testing.T) {
	t.Helper()
	c, e := f.Prepare(context.Background(), InstallRequest{})
	if e != nil {
		t.Fatal(e)
	}
	candidate := c.(*Candidate)
	f.store.fail = f.store.step + 11 // backup pairs, sync, then prepared-journal Save after-effect failure.
	if e = commitProvenance(context.Background(), f.store, candidate.trusted.transaction); e == nil {
		t.Fatal("missing simulated interruption")
	}
	f.store.fail = 0
	if !f.Pending() {
		t.Fatal("fixture did not leave a durable journal")
	}

}
func (f *TestTrustedFixture) Pending() bool {
	_, e := f.store.Load(context.Background(), PairJournal, "")
	return !errors.Is(e, os.ErrNotExist)
}
