//go:build linux || darwin

package subscription

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
)

// Run only in explicitly isolated root CI, with TMPDIR below a root-owned,
// non-writable-by-others ancestry. Default /tmp is intentionally not trusted.
func TestProviderStore_IsolatedRootIO(t *testing.T) {
	if os.Getenv("MIHARI_ISOLATED_ROOT_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires explicit isolated root CI and trusted TMPDIR; not local acceptance")
	}
	ctx := context.Background()
	path := t.TempDir()
	root, err := platform.OpenTrustedRoot(ctx, path, platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e := root.Close(); e != nil {
			t.Error(e)
		}
	})
	s, err := NewProviderStore(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	// Root-level state parents borrow D. Backup/restore must neither reject a
	// same-parent move nor close the daemon's lifetime capability.
	for _, target := range []string{"mihari.yaml", "onboarding.json"} {
		if err = s.files.write(ctx, target, []byte("sealed state"), providerObject{}); err != nil {
			t.Fatal(err)
		}
		old, inspectErr := s.files.inspect(ctx, target)
		if inspectErr != nil {
			t.Fatal(inspectErr)
		}
		backup := target + ".old-0123456789abcdef0123456789abcdef"
		if err = s.files.move(ctx, target, old, backup, providerObject{}); err != nil {
			t.Fatal(err)
		}
		if err = s.files.move(ctx, backup, old, target, providerObject{}); err != nil {
			t.Fatal(err)
		}
		if _, _, _, _, err = root.Snapshot(ctx); err != nil {
			t.Fatal("borrowed root closed by state IO")
		}
	}
	fixture, target := legacyProviderFixture(t, "intent", true, false, true)
	// Seed historical bytes through the trusted IO adapter, rebinding journal
	// identities to the actual filesystem objects before exercising recovery.
	var journal providerJournal
	if err = json.Unmarshal(fixture.objects[providerJournalPath], &journal); err != nil {
		t.Fatal(err)
	}
	for rel, content := range fixture.objects {
		if rel == providerJournalPath {
			continue
		}
		if err = s.files.write(ctx, rel, content, providerObject{}); err != nil {
			t.Fatal(err)
		}
	}
	journal.New, err = s.files.inspect(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	journal.Backup, err = s.files.inspect(ctx, target+".old-"+journal.ID)
	if err != nil {
		t.Fatal(err)
	}
	journal.Marker, err = s.files.inspect(ctx, "staging/providers/"+journal.ID+"/transaction-id")
	if err != nil {
		t.Fatal(err)
	}
	journal.Old.Identity = "historical-removed-object"
	journal.Old.BootID = journal.Marker.BootID
	if err = s.saveJournal(ctx, journal); err != nil {
		t.Fatal(err)
	}
	if err = s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	restored, err := s.files.read(ctx, target, maxDocumentBytes)
	if err != nil || string(restored) != "old" {
		t.Fatal("wrong recovery bytes", err)
	}
	info, err := os.Stat(filepath.Join(path, filepath.FromSlash(target)))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("published mode: %v %v", info, err)
	}
	if err = s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(path, "staging", "providers"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging cleanup: %v %v", entries, err)
	}
	if err = root.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.Recover(ctx); err == nil {
		t.Fatal("closed root accepted")
	}
}
func TestProviderStore_RejectsAbsentCapability(t *testing.T) {
	if _, err := NewProviderStore(context.Background(), nil); err == nil {
		t.Fatal("nil root accepted")
	}
}
func TestProviderStore_AllowedPathsExcludeOtherBusinessData(t *testing.T) {
	for _, path := range []string{"control.token", "settings.yaml", "runtime/../control.token", "runtime/core-home/Country.mmdb.old", "staging/providers/commit.json/child"} {
		if providerAllowedPath(path) {
			t.Fatal("unregistered resource path accepted")
		}
	}
}

func TestProviderStore_AllowedPathsIncludeOnlyFiniteActivationState(t *testing.T) {
	for _, path := range []string{"mihari.yaml", "onboarding.json", "subscriptions/catalog.yaml", "subscriptions/cache/0123456789abcdef0123456789abcdef.yaml"} {
		if !providerAllowedPath(path) || !providerAllowedPath(path+".old-0123456789abcdef0123456789abcdef") {
			t.Fatal("registered activation state path rejected")
		}
	}
	for _, path := range []string{"mihari.yaml/child", "mihari.yaml.old-bad", "subscriptions/cache/not-an-id.yaml", "subscriptions/cache/../../control.token", "subscriptions/catalog.json", "onboarding.json/child"} {
		if providerAllowedPath(path) {
			t.Fatal("unregistered state path accepted")
		}
	}
}
