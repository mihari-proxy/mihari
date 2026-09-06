//go:build linux || darwin

package subscription

import (
	"context"
	"errors"
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
	input := rootPolicyInput()
	input.YAML = []byte("rule-providers:\n  rules: {type: inline, behavior: domain, payload: ['example.test']}\nrules: ['RULE-SET,rules,DIRECT']\n")
	output, err := NewRootConfigPolicy().Build(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := s.Prepare(ctx, output.Providers[0])
	if err != nil {
		t.Fatal(err)
	}
	target, err := providerTarget(output.Providers[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = candidate.Commit(ctx, func(c context.Context) error {
		b, e := s.files.read(c, target, maxDocumentBytes)
		if e != nil {
			return e
		}
		if providerDigest(b) != providerDigest(output.Providers[0].Inline) {
			return errors.New("wrong published bytes")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
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
	if _, err = s.Prepare(ctx, output.Providers[0]); err == nil {
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
