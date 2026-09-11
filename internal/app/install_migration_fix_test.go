package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestUnixMigration_IdentityAliasIsNested(t *testing.T) {
	fx := newMigrationFixture(t)
	alias := openDirCap(filepath.Join(t.TempDir(), "alias-target"))
	if err := os.MkdirAll(alias.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	alias.dev, alias.ino, alias.mount = fx.source.Identity()
	opts := fx.options()
	opts.Target = alias
	_, err := prepareMigration(context.Background(), opts)
	if err == nil || apiCode(err) != protocol.CodeInvalidArgument {
		t.Fatalf("identity alias: %v", err)
	}
}

func TestUnixMigration_BindAliasIdentityIsNested(t *testing.T) {
	fx := newMigrationFixture(t)
	alias := openDirCap(filepath.Join(t.TempDir(), "bind-target"))
	if err := os.MkdirAll(alias.dir, 0700); err != nil {
		t.Fatal(err)
	}
	alias.dev, alias.ino, _ = fx.source.Identity()
	alias.mount = "other-mount"
	opts := fx.options()
	opts.Target = alias
	_, err := prepareMigration(context.Background(), opts)
	if err == nil || apiCode(err) != protocol.CodeInvalidArgument {
		t.Fatalf("bind alias: %v", err)
	}
}

func TestUnixMigration_UnknownTargetDIsInvalidState(t *testing.T) {
	fx := newMigrationFixture(t)
	if err := os.WriteFile(fx.target.osPath("mihari.yaml"), []byte("unknown-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := prepareMigration(context.Background(), fx.options())
	if err == nil || apiCode(err) != protocol.CodeInvalidState {
		t.Fatalf("unknown target D: %v", err)
	}
}

func TestUnixMigration_CompleteJournalBlocksReimport(t *testing.T) {
	fx := newMigrationFixture(t)
	h := newInstallHarness(t, InstallDataCreate)
	if _, err := h.tx.Apply(context.Background(), h.req); err != nil {
		t.Fatal(err)
	}
	journal := h.loadedJournal(t, h.disk)
	journal.SourcePath = fx.source.Path()
	journal.Phase = InstallPhaseComplete
	if _, err := h.tx.Store.Save(context.Background(), journal); err != nil {
		t.Fatal(err)
	}
	opts := fx.options()
	opts.Store = h.tx.Store
	_, err := prepareMigration(context.Background(), opts)
	if err == nil || apiCode(err) != protocol.CodeInvalidState {
		t.Fatalf("complete journal re-import: %v", err)
	}
}

func TestUnixMigration_ServiceFixedSource(t *testing.T) {
	fx := newMigrationFixture(t)
	opts := fx.options()
	opts.FixedSource = filepath.Join(t.TempDir(), "service-data")
	_, err := prepareMigration(context.Background(), opts)
	if err == nil || apiCode(err) != protocol.CodeInvalidArgument {
		t.Fatalf("mismatched service source: %v", err)
	}
}

func TestUnixMigration_CandidateHashDeterministicAndPublishes(t *testing.T) {
	fx := newMigrationFixture(t)
	opts := fx.options()
	opts.NewSecret = func() string { return strings.Repeat("ab", 32) }
	first, err := prepareMigration(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	hash1 := first.artifacts().CandidateHash
	first.cleanup()
	opts.Staging = openDirCap(filepath.Join(t.TempDir(), "staging-2"))
	if err := os.MkdirAll(opts.Staging.Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	second, err := prepareMigration(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer second.cleanup()
	if hash1 == "" || hash1 != second.artifacts().CandidateHash {
		t.Fatalf("candidate hash not deterministic: %s vs %s", hash1, second.artifacts().CandidateHash)
	}
	if second.artifacts().DefinitionNew == nil {
		t.Fatal("definition candidate missing")
	}
	if strings.Contains(filepath.ToSlash(second.artifacts().Target), "/var/lib/mihari") && second.target != nil {
		t.Fatalf("layout fallback used despite target: %s", second.artifacts().Target)
	}
}

func TestUnixMigration_ApplyLockedPublishesAfterStop(t *testing.T) {
	fx := newMigrationFixture(t)
	h := newInstallHarness(t, InstallDataCreate)
	h.tx.Artifacts = InstallArtifacts{}
	opts := fx.options()
	opts.Request.Endpoint = filepath.ToSlash(filepath.Join(fx.target.dir, "control.sock"))
	opts.Request.Credential = filepath.ToSlash(filepath.Join(fx.target.dir, "control.token"))
	opts.Request.InstallRoot = filepath.ToSlash(filepath.Join(t.TempDir(), "install"))
	h.tx.migrate = ptrOptions(opts)
	lease, err := h.lease.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.tx.ApplyLocked(context.Background(), lease, opts.Request); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fx.target.osPath("subscriptions/catalog.yaml")); err != nil {
		t.Fatalf("staged tree was not published to D: %v", err)
	}
	if _, err := os.Stat(fx.target.osPath("web/zashboard/testbuild/index.html")); err != nil {
		t.Fatalf("panel tree was not published to D: %v", err)
	}
	if _, err := os.Stat(fx.target.osPath("control.token")); err == nil {
		t.Fatal("secret published to D")
	}
	if h.tx.prepared == nil || h.tx.prepared.files["runtime/config.yaml"].hash == "" {
		t.Fatal("runtime was not staged")
	}
}

func TestUnixMigration_AfterStopDriftIsConflict(t *testing.T) {
	fx := newMigrationFixture(t)
	h := newInstallHarness(t, InstallDataCreate)
	h.tx.Artifacts = InstallArtifacts{}
	opts := fx.options()
	opts.Request.Endpoint = filepath.ToSlash(filepath.Join(fx.target.dir, "control.sock"))
	opts.Request.Credential = filepath.ToSlash(filepath.Join(fx.target.dir, "control.token"))
	opts.Request.InstallRoot = filepath.ToSlash(filepath.Join(t.TempDir(), "install"))
	opts.AfterStop = func() {
		_ = os.WriteFile(fx.source.osPath("mihari.yaml"), append(fx.settingsYAML, []byte("\n# after-stop\n")...), 0o600)
	}
	h.tx.migrate = ptrOptions(opts)
	lease, err := h.lease.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.tx.ApplyLocked(context.Background(), lease, opts.Request)
	if err == nil || apiCode(err) != protocol.CodeRevisionConflict {
		t.Fatalf("after-stop drift: %v", err)
	}
	if _, err := os.Stat(fx.target.osPath("subscriptions/catalog.yaml")); err == nil {
		t.Fatal("conflict still published D")
	}
}

func TestInstallArtifact_BinaryNotMihomoTable(t *testing.T) {
	fx := newMigrationFixture(t)
	opts := fx.options()
	opts.Trust.binary = nil
	_, err := prepareMigration(context.Background(), opts)
	if err == nil || apiCode(err) != protocol.CodeInvalidState {
		t.Fatalf("empty installer trust: %v", err)
	}
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "mihomo") {
		t.Fatalf("installer binary used mihomo table: %v", err)
	}
}

func TestInstallArtifact_BundleUsesCapabilityWrites(t *testing.T) {
	fx := newMigrationFixture(t)
	raw := mustBundleBytes(t, map[string][]byte{"payload.txt": []byte("via-cap")})
	bundle := filepath.Join(t.TempDir(), "bundle.zip")
	if err := os.WriteFile(bundle, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := fx.options()
	opts.Request.Bundle = filepath.ToSlash(bundle)
	opts.Trust.bundle = map[string]struct{}{sha256HexBytes(raw): {}}
	prepared, err := prepareMigration(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.cleanup()
	if !containsRel(fx.staging.writeRel, ".bundle/payload.txt") {
		t.Fatal("bundle extract bypassed staging capability")
	}
	got, err := fx.staging.ReadFile(context.Background(), ".bundle/payload.txt", 1<<20)
	if err != nil || string(got) != "via-cap" {
		t.Fatalf("capability extract: %q err=%v", got, err)
	}
}

func TestInstallArtifact_PrepareCleansStagingOnError(t *testing.T) {
	fx := newMigrationFixture(t)
	if err := os.Remove(fx.source.osPath("subscriptions/cache/" + fx.profileID + ".yaml")); err != nil {
		t.Fatal(err)
	}
	staging := fx.staging.dir
	_, err := prepareMigration(context.Background(), fx.options())
	if err == nil {
		t.Fatal("expected failure")
	}
	if _, statErr := os.Stat(filepath.Join(staging, "mihari.yaml")); !os.IsNotExist(statErr) {
		t.Fatal("failed prepare leaked staging files")
	}
}

func containsRel(rels []string, want string) bool {
	for _, rel := range rels {
		if rel == want {
			return true
		}
	}
	return false
}

func TestUnixMigration_StationaryRechecksIdentitySet(t *testing.T) {
	fx := newMigrationFixture(t)
	opts := fx.options()
	opts.AfterCopy = func() {
		fx.source.dev = "changed-dev"
	}
	_, err := prepareMigration(context.Background(), opts)
	if err == nil || apiCode(err) != protocol.CodeRevisionConflict {
		t.Fatalf("identity drift: %v", err)
	}
}

func TestUnixMigration_DataPublishWaitsForDurableIntent(t *testing.T) {
	fx := newMigrationFixture(t)
	h := newInstallHarness(t, InstallDataCreate)
	h.tx.Artifacts = InstallArtifacts{}
	opts := fx.options()
	opts.Request.Endpoint = filepath.ToSlash(filepath.Join(fx.target.dir, "control.sock"))
	opts.Request.Credential = filepath.ToSlash(filepath.Join(fx.target.dir, "control.token"))
	opts.Request.InstallRoot = filepath.ToSlash(filepath.Join(t.TempDir(), "install"))
	h.tx.migrate = ptrOptions(opts)
	h.tx.crash = &installCrashSpec{index: 5, point: "before-intent"}
	func() {
		defer func() {
			if got := recover(); got != errInstallCrash {
				t.Fatalf("expected data intent crash, got %v", got)
			}
		}()
		_, _ = h.tx.Apply(context.Background(), opts.Request)
	}()
	if _, err := os.Stat(fx.target.osPath("subscriptions/catalog.yaml")); !os.IsNotExist(err) {
		t.Fatalf("live D changed before data-publish intent: %v", err)
	}
	journal := h.loadedJournal(t, h.disk)
	for _, action := range journal.Actions {
		if action.Kind == JournalActionDataPublish {
			t.Fatal("crash was after intent")
		}
	}
}

func TestUnixMigration_VerifiedArtifactsDoNotRequireExistingPathDestination(t *testing.T) {
	fx := newMigrationFixture(t)
	opts := fx.options()
	opts.Request.PathBinary = filepath.Join(t.TempDir(), "not-installed-yet")
	opts.Request = businessMigrationRequest(opts.Request)
	prepared, err := prepareMigration(context.Background(), opts)
	if err != nil {
		t.Fatalf("valid business source rejected for absent PATH destination: %v", err)
	}
	defer prepared.cleanup()
	if len(prepared.art.ManagedNew) != 0 || len(prepared.art.PathNew) != 0 {
		t.Fatal("business migration took ownership of installation artifacts")
	}
	for name, hash := range fx.sourceSnap {
		if fx.sourceHashes(t)[name] != hash {
			t.Fatal("source changed", name)
		}
	}
}
