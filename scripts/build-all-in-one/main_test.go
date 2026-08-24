package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/mihari-proxy/mihari/scripts/internal/releaseinputs"
)

// fakeRunner satisfies core.CommandRunner for the host-matching `-v` smoke.
type fakeRunner struct{ output []byte }

func (f fakeRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return f.output, nil
}

// recordingRunner records whether Run was invoked, so a test can assert that the
// host-matching target goes through the exec path (not the magic-number path).
type recordingRunner struct {
	called bool
	output []byte
}

func (r *recordingRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, error) {
	r.called = true
	return r.output, nil
}

func TestBundlerProducesSixPlatformBundles(t *testing.T) {
	fixture := newLockedFixture(t, "stable")
	out := filepath.Join(t.TempDir(), "bundles")
	err := run(fixture.options(out))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	expected := map[string]string{
		"linux/amd64":   "mihari-all-in-one-linux-amd64.tar.gz",
		"linux/arm64":   "mihari-all-in-one-linux-arm64.tar.gz",
		"darwin/amd64":  "mihari-all-in-one-darwin-amd64.tar.gz",
		"darwin/arm64":  "mihari-all-in-one-darwin-arm64.tar.gz",
		"windows/amd64": "mihari-all-in-one-windows-amd64.zip",
		"windows/arm64": "mihari-all-in-one-windows-arm64.zip",
	}
	for platform, bundle := range expected {
		entries := extractBundle(t, filepath.Join(out, bundle))
		goos, _, _ := strings.Cut(platform, "/")
		suffix, script := "", "install-aio.sh"
		if goos == "windows" {
			suffix, script = ".exe", "install-aio.ps1"
		}
		want := map[string]bool{
			"mihari" + suffix:                  true,
			script:                             true,
			"data/bin/mihomo" + suffix:         true,
			"data/bin/core-channel":            true,
			"data/geoip/GeoLite2-Country.mmdb": true,
			"data/geoip/GeoLite2-ASN.mmdb":     true,
		}
		got := make(map[string]bool, len(entries))
		for name := range entries {
			got[name] = true
		}
		if len(got) != len(want) {
			t.Fatalf("%s: bundle has %d entries, got=%v want=%v", platform, len(got), sortedKeys(got), sortedKeys(want))
		}
		for w := range want {
			if !got[w] {
				t.Fatalf("%s: missing %q in bundle, got=%v", platform, w, sortedKeys(got))
			}
		}
		// mihomo magic number sanity (the host-matching target was -v-smoked via
		// fakeRunner; the other five went through the magic check; verify bytes).
		if mihomo := entries["data/bin/mihomo"+suffix]; len(mihomo) == 0 {
			t.Fatalf("%s: empty mihomo binary", platform)
		}
		assertCoreChannelSidecar(t, entries, "stable", "stable-v1.19.30")
	}
	fixture.assertOnlyLockedRequests(t)
}

func TestBundlerAlphaChannelWritesSidecar(t *testing.T) {
	fixture := newLockedFixture(t, "alpha")
	out := filepath.Join(t.TempDir(), "bundles")
	err := run(fixture.options(out))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	expected := map[string]string{
		"linux/amd64":   "mihari-all-in-one-linux-amd64.tar.gz",
		"linux/arm64":   "mihari-all-in-one-linux-arm64.tar.gz",
		"darwin/amd64":  "mihari-all-in-one-darwin-amd64.tar.gz",
		"darwin/arm64":  "mihari-all-in-one-darwin-arm64.tar.gz",
		"windows/amd64": "mihari-all-in-one-windows-amd64.zip",
		"windows/arm64": "mihari-all-in-one-windows-arm64.zip",
	}
	for platform, bundle := range expected {
		entries := extractBundle(t, filepath.Join(out, bundle))
		assertCoreChannelSidecar(t, entries, "alpha", "alpha-e183c58")
		if _, ok := entries["data/bin/core-channel"]; !ok {
			t.Fatalf("%s: missing data/bin/core-channel", platform)
		}
	}
	fixture.assertOnlyLockedRequests(t)
}

func TestRunRequiresValidLockBeforeNetworkOrOutputMutation(t *testing.T) {
	tests := []struct {
		name     string
		lockPath func(*testing.T) string
	}{
		{name: "missing flag", lockPath: func(*testing.T) string { return "" }},
		{name: "missing file", lockPath: func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing.json") }},
		{name: "invalid file", lockPath: func(t *testing.T) string {
			path := filepath.Join(t.TempDir(), "invalid.json")
			if err := os.WriteFile(path, []byte(`{"schema":"wrong"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &fixtureTransport{payloads: map[string][]byte{}}
			out := filepath.Join(t.TempDir(), "must-not-exist")
			err := run(options{LockPath: test.lockPath(t), Out: out, HTTPClient: &http.Client{Transport: transport}})
			if err == nil {
				t.Fatal("run accepted a missing or invalid release input lock")
			}
			if transport.requestCount() != 0 {
				t.Fatalf("network requests = %d, want 0", transport.requestCount())
			}
			if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
				t.Fatalf("output path was mutated before lock validation: %v", statErr)
			}
		})
	}
}

func TestRunRejectsPlatformOutsideLockBeforeNetwork(t *testing.T) {
	fixture := newLockedFixture(t, "stable")
	out := filepath.Join(t.TempDir(), "must-not-exist")
	opts := fixture.options(out)
	opts.Platforms = []string{"linux/riscv64"}
	if err := run(opts); err == nil {
		t.Fatal("run accepted a platform outside the exact lock set")
	}
	if fixture.transport.requestCount() != 0 {
		t.Fatalf("network requests = %d, want 0", fixture.transport.requestCount())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("output path was mutated before platform validation: %v", err)
	}
}

func TestRunRejectsOutputPathOverlapBeforeNetwork(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		out  func(*lockedFixture) string
	}{
		{name: "working directory", out: func(*lockedFixture) string { return "." }},
		{name: "working directory ancestor", out: func(*lockedFixture) string { return filepath.Dir(workingDirectory) }},
		{name: "equals lock", out: func(f *lockedFixture) string { return f.lockPath }},
		{name: "contains lock", out: func(f *lockedFixture) string { return filepath.Dir(f.lockPath) }},
		{name: "equals mihari input", out: func(f *lockedFixture) string { return f.mihariDir }},
		{name: "contains mihari input", out: func(f *lockedFixture) string { return filepath.Dir(f.mihariDir) }},
		{name: "inside mihari input", out: func(f *lockedFixture) string { return filepath.Join(f.mihariDir, "nested-output") }},
		{name: "contains scripts input", out: func(f *lockedFixture) string { return filepath.Dir(f.scriptsDir) }},
		{name: "inside scripts input", out: func(f *lockedFixture) string { return filepath.Join(f.scriptsDir, "nested-output") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLockedFixture(t, "stable")
			out := test.out(fixture)
			opts := fixture.options(out)
			err := run(opts)
			if err == nil || !strings.Contains(err.Error(), "dedicated managed bundle directory") {
				t.Fatalf("run overlap error = %v, want fixed isolation error", err)
			}
			if fixture.transport.requestCount() != 0 {
				t.Fatalf("network requests = %d, want 0", fixture.transport.requestCount())
			}
			for _, sensitive := range []string{out, fixture.lockPath, fixture.mihariDir, fixture.scriptsDir} {
				if filepath.IsAbs(sensitive) && strings.Contains(err.Error(), sensitive) {
					t.Fatalf("isolation error %q leaked absolute path %q", err, sensitive)
				}
			}
		})
	}
}

func TestRunPublishesToResolvedOutputBelowSymlinkedAncestor(t *testing.T) {
	fixture := newLockedFixture(t, "stable")
	root := t.TempDir()
	realParent := filepath.Join(root, "private", "var")
	if err := os.MkdirAll(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(root, "var")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Skipf("directory symlink creation is unavailable: %v", err)
	}

	logicalOutput := filepath.Join(linkedParent, "bundles")
	canonicalOutput := filepath.Join(realParent, "bundles")
	redirectedParent := filepath.Join(root, "redirected")
	if err := os.Mkdir(redirectedParent, 0o700); err != nil {
		t.Fatal(err)
	}
	retargeted := false
	opts := fixture.options(logicalOutput)
	opts.PublishFault = func(operation, path string) error {
		if !pathContains(canonicalOutput, path) {
			t.Fatalf("publish path %q is outside canonical output", path)
		}
		if operation == "copy" && !retargeted {
			retargeted = true
			if err := os.Remove(linkedParent); err != nil {
				t.Fatalf("remove output ancestor symlink: %v", err)
			}
			if err := os.Symlink(redirectedParent, linkedParent); err != nil {
				t.Fatalf("retarget output ancestor symlink: %v", err)
			}
		}
		return nil
	}
	if err := run(opts); err != nil {
		t.Fatalf("run below symlinked ancestor: %v", err)
	}
	if !retargeted {
		t.Fatal("publish did not reach the identity-change seam")
	}
	for _, platform := range releaseinputs.RequiredPlatforms() {
		goos, goarch, err := splitPlatform(platform)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(canonicalOutput, bundleName(goos, goarch))); err != nil {
			t.Fatalf("canonical output is missing %s: %v", platform, err)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(redirectedParent, "bundles")); err == nil || !os.IsNotExist(err) {
		t.Fatalf("retargeted ancestor received output entries %v: %v", entries, err)
	}
}

func TestRunRejectsDestinationSymlinkBeforeNetwork(t *testing.T) {
	fixture := newLockedFixture(t, "stable")
	root := t.TempDir()
	target := filepath.Join(root, "real-bundles")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "bundles")
	if err := os.Symlink(target, destination); err != nil {
		t.Skipf("directory symlink creation is unavailable: %v", err)
	}

	err := run(fixture.options(destination))
	if !errors.Is(err, errUnsafeBundleOutput) {
		t.Fatalf("run destination symlink error = %v, want fixed isolation error", err)
	}
	if fixture.transport.requestCount() != 0 {
		t.Fatalf("network requests = %d, want 0", fixture.transport.requestCount())
	}
	entries, readErr := os.ReadDir(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("destination symlink target was mutated: %v", entries)
	}
}

func TestRunRejectsNonDirectoryDestinationBeforeNetwork(t *testing.T) {
	fixture := newLockedFixture(t, "stable")
	destination := filepath.Join(t.TempDir(), "bundles")
	if err := os.WriteFile(destination, []byte("do not replace"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := run(fixture.options(destination))
	if !errors.Is(err, errUnsafeBundleOutput) {
		t.Fatalf("run non-directory destination error = %v, want fixed isolation error", err)
	}
	if fixture.transport.requestCount() != 0 {
		t.Fatalf("network requests = %d, want 0", fixture.transport.requestCount())
	}
	got, readErr := os.ReadFile(destination)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "do not replace" {
		t.Fatalf("non-directory destination was mutated: %q", got)
	}
}

func TestRunRejectsLockedPayloadMismatchWithoutBundle(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*lockedFixture)
	}{
		{name: "mihomo size", mutate: func(f *lockedFixture) {
			asset := f.lock.Mihomo.Assets["windows/arm64"]
			asset.Size++
			f.lock.Mihomo.Assets["windows/arm64"] = asset
		}},
		{name: "mihomo digest", mutate: func(f *lockedFixture) {
			asset := f.lock.Mihomo.Assets["windows/arm64"]
			asset.SHA256 = strings.Repeat("0", 64)
			f.lock.Mihomo.Assets["windows/arm64"] = asset
		}},
		{name: "geoip digest", mutate: func(f *lockedFixture) {
			f.lock.GeoIP.Country.SHA256 = strings.Repeat("0", 64)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLockedFixture(t, "stable")
			test.mutate(fixture)
			fixture.writeLock(t)
			out := filepath.Join(t.TempDir(), "bundles")
			err := run(fixture.options(out))
			if err == nil {
				t.Fatal("run accepted payload that disagrees with the lock")
			}
			entries, readErr := os.ReadDir(out)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("failed build left outputs: %v", entries)
			}
		})
	}
}

func TestRunProducesByteIdenticalBundlesFromSameLock(t *testing.T) {
	fixture := newLockedFixture(t, "stable")
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	if err := run(fixture.options(first)); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := run(fixture.options(second)); err != nil {
		t.Fatalf("second run: %v", err)
	}
	for _, platform := range releaseinputs.RequiredPlatforms() {
		goos, goarch, err := splitPlatform(platform)
		if err != nil {
			t.Fatal(err)
		}
		name := bundleName(goos, goarch)
		firstBytes, err := os.ReadFile(filepath.Join(first, name))
		if err != nil {
			t.Fatal(err)
		}
		secondBytes, err := os.ReadFile(filepath.Join(second, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(firstBytes, secondBytes) {
			t.Fatalf("%s differs across identical builds", name)
		}
	}
}

func TestPackStageUsesCanonicalModesIndependentOfHostPermissions(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			firstStage := t.TempDir()
			secondStage := t.TempDir()
			writeCanonicalArchiveStage(t, firstStage, goos, 0o444)
			writeCanonicalArchiveStage(t, secondStage, goos, 0o777)
			firstOut := t.TempDir()
			secondOut := t.TempDir()
			if err := packStage(context.Background(), firstStage, firstOut, goos, "amd64", nil); err != nil {
				t.Fatalf("pack first stage: %v", err)
			}
			if err := packStage(context.Background(), secondStage, secondOut, goos, "amd64", nil); err != nil {
				t.Fatalf("pack second stage: %v", err)
			}
			name := bundleName(goos, "amd64")
			firstBytes, err := os.ReadFile(filepath.Join(firstOut, name))
			if err != nil {
				t.Fatal(err)
			}
			secondBytes, err := os.ReadFile(filepath.Join(secondOut, name))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(firstBytes, secondBytes) {
				t.Fatalf("%s archive bytes depend on host file permissions", goos)
			}
			modes := archiveEntryModes(t, filepath.Join(firstOut, name))
			want := canonicalArchiveModes(goos)
			if !reflect.DeepEqual(modes, want) {
				t.Fatalf("%s archive modes = %v, want %v", goos, modes, want)
			}
		})
	}
}

func TestPackStageHonorsCancellationBetweenChunks(t *testing.T) {
	for _, test := range []struct {
		name string
		goos string
	}{
		{name: "tar gzip", goos: "linux"},
		{name: "zip", goos: "windows"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stage := t.TempDir()
			name := "mihari"
			if test.goos == "windows" {
				name = "mihari.exe"
			}
			if err := os.WriteFile(filepath.Join(stage, name), bytes.Repeat([]byte("x"), 256<<10), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			chunks := 0
			hook := func() {
				chunks++
				if chunks == 1 {
					cancel()
				}
			}
			out := t.TempDir()
			err := packStage(ctx, stage, out, test.goos, "amd64", hook)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("packStage error = %v, want context cancellation", err)
			}
			if chunks == 0 {
				t.Fatal("copy hook was not reached")
			}
			if _, err := os.Lstat(filepath.Join(out, bundleName(test.goos, "amd64"))); !os.IsNotExist(err) {
				t.Fatalf("partial archive remains after cancellation: %v", err)
			}
		})
	}
}

func TestRunCancellationDuringLocalCopyPreservesManagedOutput(t *testing.T) {
	fixture := newLockedFixture(t, "stable")
	largeMihari := filepath.Join(fixture.mihariDir, "mihari-"+runtime.GOOS+"-"+runtime.GOARCH)
	if runtime.GOOS == "windows" {
		largeMihari += ".exe"
	}
	if err := os.WriteFile(largeMihari, bytes.Repeat([]byte("m"), 256<<10), 0o700); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	destination := filepath.Join(parent, "bundles")
	writeExistingOutput(t, destination)
	before := snapshotTree(t, destination)
	ctx, cancel := context.WithCancel(context.Background())
	opts := fixture.options(destination)
	opts.Context = ctx
	opts.CopyChunkHook = cancel
	err := run(opts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context cancellation", err)
	}
	after := snapshotTree(t, destination)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("managed output changed after local-copy cancellation\nbefore=%v\nafter=%v", before, after)
	}
	assertNoPublishResidue(t, parent)
}

func TestPublishBundlesCancellationDuringStagingPreservesManagedOutput(t *testing.T) {
	parent := t.TempDir()
	source := writePublishSource(t, parent)
	name := bundleName("linux", "amd64")
	if err := os.WriteFile(filepath.Join(source, name), bytes.Repeat([]byte("b"), 256<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(parent, "bundles")
	writeExistingOutput(t, destination)
	before := snapshotTree(t, destination)
	ctx, cancel := context.WithCancel(context.Background())
	err := publishBundles(ctx, source, destination, releaseinputs.RequiredPlatforms(), nil, nil, cancel)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("publishBundles error = %v, want context cancellation", err)
	}
	after := snapshotTree(t, destination)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("managed output changed after staging cancellation\nbefore=%v\nafter=%v", before, after)
	}
	assertNoPublishResidue(t, parent)
}

func TestPublishBundlesFinishesCommitAfterCancellation(t *testing.T) {
	parent := t.TempDir()
	source := writePublishSource(t, parent)
	destination := filepath.Join(parent, "bundles")
	writeExistingOutput(t, destination)
	ctx, cancel := context.WithCancel(context.Background())
	fault := func(operation, _ string) error {
		if operation == "replace" {
			cancel()
		}
		return nil
	}
	if err := publishBundles(ctx, source, destination, releaseinputs.RequiredPlatforms(), fault, nil, nil); err != nil {
		t.Fatalf("publishBundles interrupted an active commit: %v", err)
	}
	name := bundleName("windows", "arm64")
	got, err := os.ReadFile(filepath.Join(destination, name))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-"+name {
		t.Fatalf("active bundle = %q, want committed bundle", got)
	}
	assertNoPublishResidue(t, parent)
}

func TestPublishBundlesFailureIsAllOrNothing(t *testing.T) {
	tests := []struct {
		name      string
		operation string
	}{
		{name: "copy failure", operation: "copy"},
		{name: "chmod failure", operation: "chmod"},
		{name: "replace failure", operation: "replace"},
	}
	for _, test := range tests {
		t.Run(test.name+" preserves existing output", func(t *testing.T) {
			parent := t.TempDir()
			source := writePublishSource(t, parent)
			destination := filepath.Join(parent, "bundles")
			writeExistingOutput(t, destination)
			before := snapshotTree(t, destination)
			fault := func(operation, path string) error {
				if operation == test.operation && (operation == "replace" || filepath.Base(path) == bundleName("windows", "arm64")) {
					return errors.New("injected " + operation + " failure")
				}
				return nil
			}
			if err := publishBundles(context.Background(), source, destination, releaseinputs.RequiredPlatforms(), fault, nil, nil); err == nil {
				t.Fatalf("publishBundles accepted injected %s failure", test.operation)
			}
			after := snapshotTree(t, destination)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("existing output changed after %s failure\nbefore=%v\nafter=%v", test.operation, before, after)
			}
			assertNoPublishResidue(t, parent)
		})

		t.Run(test.name+" leaves absent output absent", func(t *testing.T) {
			parent := t.TempDir()
			source := writePublishSource(t, parent)
			createdParent := filepath.Join(parent, "new-parent")
			destination := filepath.Join(createdParent, "bundles")
			fault := func(operation, path string) error {
				if operation == test.operation && (operation == "replace" || filepath.Base(path) == bundleName("windows", "arm64")) {
					return errors.New("injected " + operation + " failure")
				}
				return nil
			}
			if err := publishBundles(context.Background(), source, destination, releaseinputs.RequiredPlatforms(), fault, nil, nil); err == nil {
				t.Fatalf("publishBundles accepted injected %s failure", test.operation)
			}
			if _, err := os.Lstat(destination); !os.IsNotExist(err) {
				t.Fatalf("destination exists after %s failure: %v", test.operation, err)
			}
			if _, err := os.Lstat(createdParent); !os.IsNotExist(err) {
				t.Fatalf("new parent exists after %s failure: %v", test.operation, err)
			}
			assertNoPublishResidue(t, parent)
		})
	}
}

func TestPublishBundlesRejectsUnmanagedEntriesWithoutMutation(t *testing.T) {
	parent := t.TempDir()
	source := writePublishSource(t, parent)
	destination := filepath.Join(parent, "bundles")
	writeExistingOutput(t, destination)
	if err := os.Mkdir(filepath.Join(destination, "notes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "notes", "keep.txt"), []byte("unmanaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, destination)
	if err := publishBundles(context.Background(), source, destination, releaseinputs.RequiredPlatforms(), nil, nil, nil); err == nil {
		t.Fatal("publishBundles accepted an unmanaged output entry")
	}
	after := snapshotTree(t, destination)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("managed output changed after rejecting unrelated entry\nbefore=%v\nafter=%v", before, after)
	}
	assertNoPublishResidue(t, parent)
}

func TestPublishBundlesRejectsUnsafeExistingOutput(t *testing.T) {
	parent := t.TempDir()
	source := writePublishSource(t, parent)
	destination := filepath.Join(parent, "bundles")
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "outside.txt")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(destination, "unsafe-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	before := snapshotTreeLstat(t, destination)
	if err := publishBundles(context.Background(), source, destination, releaseinputs.RequiredPlatforms(), nil, nil, nil); err == nil {
		t.Fatal("publishBundles accepted an existing symlink")
	}
	after := snapshotTreeLstat(t, destination)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("unsafe output changed\nbefore=%v\nafter=%v", before, after)
	}
	assertNoPublishResidue(t, parent)
}

func TestPublishBundlesRejectsNonDirectoryDestination(t *testing.T) {
	parent := t.TempDir()
	source := writePublishSource(t, parent)
	destination := filepath.Join(parent, "bundles")
	if err := os.WriteFile(destination, []byte("do not replace"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := publishBundles(context.Background(), source, destination, releaseinputs.RequiredPlatforms(), nil, nil, nil); err == nil {
		t.Fatal("publishBundles accepted a non-directory destination")
	}
	after, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("non-directory destination changed: got %q want %q", after, before)
	}
	assertNoPublishResidue(t, parent)
}

func TestPublishBundlesRejectsSymlinkedParent(t *testing.T) {
	parent := t.TempDir()
	source := writePublishSource(t, parent)
	realParent := filepath.Join(parent, "real-parent")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(parent, "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Skipf("directory symlink creation is unavailable: %v", err)
	}
	if err := publishBundles(context.Background(), source, filepath.Join(linkedParent, "bundles"), releaseinputs.RequiredPlatforms(), nil, nil, nil); err == nil {
		t.Fatal("publishBundles crossed a symlinked destination parent")
	}
	if _, err := os.Lstat(filepath.Join(realParent, "bundles")); !os.IsNotExist(err) {
		t.Fatalf("publish crossed parent link and created output: %v", err)
	}
	assertNoPublishResidue(t, parent)
}

func TestRunCleanupFailureWarnsAfterSuccessfulCommit(t *testing.T) {
	fixture := newLockedFixture(t, "stable")
	parent := t.TempDir()
	destination := filepath.Join(parent, "bundles")
	writeExistingOutput(t, destination)
	var warnings bytes.Buffer
	opts := fixture.options(destination)
	opts.PublishFault = func(operation, _ string) error {
		if operation == "cleanup" {
			return errors.New("injected cleanup failure at " + destination)
		}
		return nil
	}
	opts.WarningSink = func(message string) {
		warnings.WriteString(message)
		warnings.WriteByte('\n')
	}
	if err := run(opts); err != nil {
		t.Fatalf("run returned an error after commit: %v", err)
	}
	entries := extractBundle(t, filepath.Join(destination, bundleName("linux", "amd64")))
	if len(entries) == 0 {
		t.Fatal("new bundle directory is not active after cleanup failure")
	}
	warningLines := strings.Split(strings.TrimSpace(warnings.String()), "\n")
	if len(warningLines) != 1 {
		t.Fatalf("warnings = %q, want exactly one cleanup warning", warnings.String())
	}
	const wantWarning = "old bundle backup cleanup failed; manual cleanup may be required"
	if warningLines[0] != wantWarning {
		t.Fatalf("warning = %q, want fixed %q", warningLines[0], wantWarning)
	}
	for _, sensitive := range []string{destination, parent, filepath.Base(destination), "injected cleanup failure"} {
		if strings.Contains(warningLines[0], sensitive) {
			t.Fatalf("warning %q leaked path component %q", warningLines[0], sensitive)
		}
	}
	backupCount := 0
	parentEntries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range parentEntries {
		if strings.HasPrefix(entry.Name(), ".mihari-aio-backup-") {
			backupCount++
		}
	}
	if backupCount != 1 {
		t.Fatalf("backup count = %d, want one retained backup after injected cleanup failure", backupCount)
	}
}

func TestPublishBundlesCleanupFailureAllowsNoOpWarningSink(t *testing.T) {
	parent := t.TempDir()
	source := writePublishSource(t, parent)
	destination := filepath.Join(parent, "bundles")
	writeExistingOutput(t, destination)
	fault := func(operation, _ string) error {
		if operation == "cleanup" {
			return errors.New("injected cleanup failure")
		}
		return nil
	}
	if err := publishBundles(context.Background(), source, destination, releaseinputs.RequiredPlatforms(), fault, nil, nil); err != nil {
		t.Fatalf("publishBundles with no-op warning sink: %v", err)
	}
	name := bundleName("windows", "arm64")
	got, err := os.ReadFile(filepath.Join(destination, name))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-"+name {
		t.Fatalf("active bundle = %q, want committed bundle", got)
	}
}

func TestRunHonorsContextAndRejectsInsecureRedirects(t *testing.T) {
	t.Run("canceled context", func(t *testing.T) {
		fixture := newLockedFixture(t, "stable")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		opts := fixture.options(filepath.Join(t.TempDir(), "out"))
		opts.Context = ctx
		if err := run(opts); err == nil {
			t.Fatal("run accepted a canceled context")
		}
	})

	t.Run("non-200 status", func(t *testing.T) {
		fixture := newLockedFixture(t, "stable")
		asset := fixture.lock.Mihomo.Assets["linux/amd64"]
		fixture.transport.statuses[asset.URL] = http.StatusServiceUnavailable
		if err := run(fixture.options(filepath.Join(t.TempDir(), "out"))); err == nil {
			t.Fatal("run accepted a non-200 locked download")
		}
	})

	t.Run("HTTPS downgrade redirect", func(t *testing.T) {
		fixture := newLockedFixture(t, "stable")
		asset := fixture.lock.Mihomo.Assets["linux/amd64"]
		fixture.transport.redirects[asset.URL] = "http://example.invalid/core.gz"
		if err := run(fixture.options(filepath.Join(t.TempDir(), "out"))); err == nil {
			t.Fatal("run followed an HTTPS downgrade redirect")
		}
		if fixture.transport.requestsByURL()["http://example.invalid/core.gz"] != 0 {
			t.Fatal("insecure redirect target was requested")
		}
	})
}

func TestSidecarScriptInstallersCopyCoreChannel(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	for _, name := range []string{"install-aio.sh", "install-aio.ps1"} {
		data, err := os.ReadFile(filepath.Join(root, "scripts", "install", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(data)
		if !strings.Contains(text, "core-channel") {
			t.Errorf("%s: expected to copy data/bin/core-channel sidecar", name)
		}
		if !strings.Contains(text, "Never touches") || !strings.Contains(text, "mihari.yaml") {
			t.Errorf("%s: expected Never touches comment to mention mihari.yaml", name)
		}
	}
}

func TestSmokeMihomoExecutesHostTarget(t *testing.T) {
	ctx := context.Background()
	// Deliberately invalid magic: the host-matching target must exec the runner
	// (returning output), NOT fall through to the magic-number check. A
	// host-assumption bug (hardcoding linux/amd64) fails this on any non-linux
	// host because the magic check would reject the garbage bytes.
	path := filepath.Join(t.TempDir(), "mihomo")
	if err := os.WriteFile(path, []byte("not a valid executable magic"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{output: []byte("Mihomo Meta v1.19.0")}
	if err := smokeMihomo(ctx, runtime.GOOS, runtime.GOARCH, path, runner); err != nil {
		t.Fatalf("host-matching smoke: %v", err)
	}
	if !runner.called {
		t.Fatal("host-matching platform must exec the runner, not magic-check")
	}
}

func TestSmokeMihomoMagicChecksNonHostTarget(t *testing.T) {
	ctx := context.Background()
	goos, goarch := nonHostTarget(t)
	validMagic := map[string][]byte{
		"linux":   {0x7f, 'E', 'L', 'F'},
		"darwin":  {0xcf, 0xfa, 0xed, 0xfe},
		"windows": {'M', 'Z'},
	}[goos]
	path := filepath.Join(t.TempDir(), "mihomo")
	if err := os.WriteFile(path, validMagic, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := smokeMihomo(ctx, goos, goarch, path, nil); err != nil {
		t.Fatalf("non-host valid magic: %v", err)
	}
	if err := os.WriteFile(path, []byte("bad magic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := smokeMihomo(ctx, goos, goarch, path, nil); err == nil {
		t.Fatal("expected magic mismatch error for non-host target")
	}
}

// nonHostTarget returns any of the 6 supported platforms that is not the build
// host's, so a magic-number test is deterministic regardless of where it runs.
func nonHostTarget(t *testing.T) (string, string) {
	t.Helper()
	for _, target := range []struct{ goos, goarch string }{
		{"linux", "amd64"}, {"linux", "arm64"},
		{"darwin", "amd64"}, {"darwin", "arm64"},
		{"windows", "amd64"}, {"windows", "arm64"},
	} {
		if target.goos != runtime.GOOS || target.goarch != runtime.GOARCH {
			return target.goos, target.goarch
		}
	}
	t.Fatal("could not find a non-host target")
	return "", ""
}

func TestAssertStageEnforcesWhitelist(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		files   []string
		wantErr bool
	}{
		{name: "unix complete", goos: "linux", files: []string{"mihari", "install-aio.sh", "data/bin/mihomo", "data/bin/core-channel", "data/geoip/GeoLite2-Country.mmdb", "data/geoip/GeoLite2-ASN.mmdb"}},
		{name: "windows complete", goos: "windows", files: []string{"mihari.exe", "install-aio.ps1", "data/bin/mihomo.exe", "data/bin/core-channel", "data/geoip/GeoLite2-Country.mmdb", "data/geoip/GeoLite2-ASN.mmdb"}},
		{name: "missing geoip", goos: "linux", files: []string{"mihari", "install-aio.sh", "data/bin/mihomo", "data/bin/core-channel", "data/geoip/GeoLite2-Country.mmdb"}, wantErr: true},
		{name: "forbidden onboarding.json", goos: "linux", files: []string{"mihari", "install-aio.sh", "data/bin/mihomo", "data/bin/core-channel", "data/geoip/GeoLite2-Country.mmdb", "data/geoip/GeoLite2-ASN.mmdb", "onboarding.json"}, wantErr: true},
		{name: "forbidden mihari.yaml", goos: "windows", files: []string{"mihari.exe", "install-aio.ps1", "data/bin/mihomo.exe", "data/bin/core-channel", "data/geoip/GeoLite2-Country.mmdb", "data/geoip/GeoLite2-ASN.mmdb", "mihari.yaml"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := assertStage(test.goos, test.files)
			if test.wantErr && err == nil {
				t.Fatal("expected whitelist violation, got nil")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

type treeSnapshotEntry struct {
	Mode os.FileMode
	Data string
}

func canonicalArchiveModes(goos string) map[string]os.FileMode {
	suffix, script := "", "install-aio.sh"
	if goos == "windows" {
		suffix, script = ".exe", "install-aio.ps1"
	}
	modes := map[string]os.FileMode{
		"mihari" + suffix:                  0o755,
		script:                             0o644,
		"data/bin/mihomo" + suffix:         0o755,
		"data/bin/core-channel":            0o644,
		"data/geoip/GeoLite2-Country.mmdb": 0o644,
		"data/geoip/GeoLite2-ASN.mmdb":     0o644,
	}
	if goos != "windows" {
		modes[script] = 0o755
	}
	return modes
}

func writeCanonicalArchiveStage(t *testing.T, root, goos string, hostMode os.FileMode) {
	t.Helper()
	for name := range canonicalArchiveModes(goos) {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, hostMode); err != nil {
			t.Fatal(err)
		}
	}
}

func archiveEntryModes(t *testing.T, path string) map[string]os.FileMode {
	t.Helper()
	result := make(map[string]os.FileMode)
	if strings.HasSuffix(path, ".tar.gz") {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		compressed, err := gzip.NewReader(file)
		if err != nil {
			t.Fatal(err)
		}
		defer compressed.Close()
		reader := tar.NewReader(compressed)
		for {
			header, err := reader.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			result[header.Name] = os.FileMode(header.Mode).Perm()
		}
		return result
	}
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		result[file.Name] = file.Mode().Perm()
	}
	return result
}

func writePublishSource(t *testing.T, parent string) string {
	t.Helper()
	source := filepath.Join(parent, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, platform := range releaseinputs.RequiredPlatforms() {
		goos, goarch, err := splitPlatform(platform)
		if err != nil {
			t.Fatal(err)
		}
		name := bundleName(goos, goarch)
		if err := os.WriteFile(filepath.Join(source, name), []byte("new-"+name), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	return source
}

func writeExistingOutput(t *testing.T, destination string) {
	t.Helper()
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, platform := range releaseinputs.RequiredPlatforms() {
		goos, goarch, err := splitPlatform(platform)
		if err != nil {
			t.Fatal(err)
		}
		name := bundleName(goos, goarch)
		if err := os.WriteFile(filepath.Join(destination, name), []byte("old-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func snapshotTree(t *testing.T, root string) map[string]treeSnapshotEntry {
	t.Helper()
	return snapshotTreeWithLinks(t, root, false)
}

func snapshotTreeLstat(t *testing.T, root string) map[string]treeSnapshotEntry {
	t.Helper()
	return snapshotTreeWithLinks(t, root, true)
}

func snapshotTreeWithLinks(t *testing.T, root string, allowLinks bool) map[string]treeSnapshotEntry {
	t.Helper()
	result := make(map[string]treeSnapshotEntry)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		item := treeSnapshotEntry{Mode: info.Mode()}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			if !allowLinks {
				return errors.New("unexpected link in snapshot")
			}
			item.Data, err = os.Readlink(path)
		case info.Mode().IsRegular():
			var data []byte
			data, err = os.ReadFile(path)
			item.Data = string(data)
		}
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = item
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return result
}

func assertNoPublishResidue(t *testing.T, parent string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".mihari-aio-publish-") || strings.HasPrefix(entry.Name(), ".mihari-aio-backup-") {
			t.Fatalf("publish transaction residue remains: %s", entry.Name())
		}
	}
}

type lockedFixture struct {
	lock       releaseinputs.Lock
	lockPath   string
	mihariDir  string
	scriptsDir string
	transport  *fixtureTransport
}

func newLockedFixture(t *testing.T, channel string) *lockedFixture {
	t.Helper()
	const (
		tag    = "v1.19.30"
		commit = "69986b5d098c8d723a2c4d56317bc10cd5669c02"
	)
	lock := releaseinputs.Lock{
		Schema: releaseinputs.SchemaV1,
		Mihomo: releaseinputs.MihomoInputs{
			Repository: "MetaCubeX/mihomo",
			Channel:    channel,
			ReleaseID:  1,
			Tag:        tag,
			Assets:     make(map[string]releaseinputs.MihomoAsset),
		},
		GeoIP: releaseinputs.GeoIPInputs{
			Repository: "Loyalsoldier/geoip",
			Commit:     commit,
		},
	}
	if channel == "alpha" {
		lock.Mihomo.Tag = "Prerelease-Alpha"
	}
	payloads := make(map[string][]byte)
	for index, platform := range releaseinputs.RequiredPlatforms() {
		extension := ".gz"
		if strings.HasPrefix(platform, "windows/") {
			extension = ".zip"
		}
		name := "mihomo-" + strings.ReplaceAll(platform, "/", "-") + "-" + lock.Mihomo.Tag + extension
		if channel == "alpha" {
			name = "mihomo-" + strings.ReplaceAll(platform, "/", "-") + "-alpha-e183c58" + extension
		}
		payload := fakeMihomoArchive(t, name)
		sum := sha256.Sum256(payload)
		url := "https://github.com/MetaCubeX/mihomo/releases/download/" + lock.Mihomo.Tag + "/" + name
		lock.Mihomo.Assets[platform] = releaseinputs.MihomoAsset{
			AssetID: int64(index + 1), Name: name, URL: url,
			Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:]),
		}
		payloads[url] = payload
	}
	country := []byte("fake-GeoLite2-Country.mmdb")
	asn := []byte("fake-GeoLite2-ASN.mmdb")
	countrySum := sha256.Sum256(country)
	asnSum := sha256.Sum256(asn)
	lock.GeoIP.Country = releaseinputs.GeoIPFile{
		Name:   "GeoLite2-Country.mmdb",
		URL:    "https://raw.githubusercontent.com/Loyalsoldier/geoip/" + commit + "/GeoLite2-Country.mmdb",
		SHA256: hex.EncodeToString(countrySum[:]),
	}
	lock.GeoIP.ASN = releaseinputs.GeoIPFile{
		Name:   "GeoLite2-ASN.mmdb",
		URL:    "https://raw.githubusercontent.com/Loyalsoldier/geoip/" + commit + "/GeoLite2-ASN.mmdb",
		SHA256: hex.EncodeToString(asnSum[:]),
	}
	payloads[lock.GeoIP.Country.URL] = country
	payloads[lock.GeoIP.ASN.URL] = asn

	mihariDir := t.TempDir()
	writeMihariDist(t, mihariDir)
	scriptsDir := t.TempDir()
	for _, name := range []string{"install-aio.sh", "install-aio.ps1"} {
		if err := os.WriteFile(filepath.Join(scriptsDir, name), []byte("# aio installer\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixture := &lockedFixture{
		lock:       lock,
		lockPath:   filepath.Join(t.TempDir(), "release-inputs.lock.json"),
		mihariDir:  mihariDir,
		scriptsDir: scriptsDir,
		transport: &fixtureTransport{
			payloads:  payloads,
			statuses:  make(map[string]int),
			redirects: make(map[string]string),
		},
	}
	fixture.writeLock(t)
	return fixture
}

func (f *lockedFixture) writeLock(t *testing.T) {
	t.Helper()
	data, err := releaseinputs.Encode(f.lock)
	if err != nil {
		t.Fatalf("encode fixture lock: %v", err)
	}
	if err := os.WriteFile(f.lockPath, data, 0o600); err != nil {
		t.Fatalf("write fixture lock: %v", err)
	}
}

func (f *lockedFixture) options(out string) options {
	return options{
		LockPath: f.lockPath, MihariDir: f.mihariDir, Out: out, ScriptsDir: f.scriptsDir,
		Platforms:     releaseinputs.RequiredPlatforms(),
		HTTPClient:    &http.Client{Transport: f.transport},
		GeoIPValidate: func(string) error { return nil },
		Runner:        fakeRunner{output: []byte("Mihomo Meta v1.19.30")},
	}
}

func (f *lockedFixture) assertOnlyLockedRequests(t *testing.T) {
	t.Helper()
	want := make(map[string]int, 8)
	for _, asset := range f.lock.Mihomo.Assets {
		want[asset.URL]++
	}
	want[f.lock.GeoIP.Country.URL]++
	want[f.lock.GeoIP.ASN.URL]++
	got := f.transport.requestsByURL()
	if len(got) != len(want) {
		t.Fatalf("requested URLs = %v, want exactly locked URLs %v", got, want)
	}
	for url, count := range want {
		if got[url] != count {
			t.Fatalf("request count for %s = %d, want %d (all requests: %v)", url, got[url], count, got)
		}
	}
	for url := range got {
		if strings.Contains(url, "/latest") || strings.Contains(url, "/release/") || strings.HasSuffix(url, ".sha256sum") {
			t.Fatalf("mutable discovery request observed: %s", url)
		}
	}
}

type fixtureTransport struct {
	mu        sync.Mutex
	payloads  map[string][]byte
	statuses  map[string]int
	redirects map[string]string
	requests  []string
}

func (f *fixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.requests = append(f.requests, request.URL.String())
	payload, ok := f.payloads[request.URL.String()]
	status := f.statuses[request.URL.String()]
	redirect := f.redirects[request.URL.String()]
	f.mu.Unlock()
	if redirect != "" {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{redirect}},
			Body:       io.NopCloser(strings.NewReader("redirect")),
			Request:    request,
		}, nil
	}
	if status == 0 {
		status = http.StatusOK
	}
	if !ok && status == http.StatusOK {
		status = http.StatusNotFound
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(payload)),
		Request:    request,
	}, nil
}

func (f *fixtureTransport) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fixtureTransport) requestsByURL() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make(map[string]int, len(f.requests))
	for _, rawURL := range f.requests {
		result[rawURL]++
	}
	return result
}

func fakeMihomoArchive(t *testing.T, name string) []byte {
	t.Helper()
	goos := "linux"
	switch {
	case strings.HasPrefix(name, "mihomo-darwin-"):
		goos = "darwin"
	case strings.HasPrefix(name, "mihomo-windows-"):
		goos = "windows"
	}
	magic := map[string][]byte{
		"linux":   {0x7f, 'E', 'L', 'F'},
		"darwin":  {0xcf, 0xfa, 0xed, 0xfe}, // Mach-O 64 little-endian (real darwin/amd64+arm64)
		"windows": {'M', 'Z'},
	}[goos]
	binary := append(append([]byte(nil), magic...), []byte("-fake-mihomo")...)
	if strings.HasSuffix(name, ".gz") {
		var buffer bytes.Buffer
		writer := gzip.NewWriter(&buffer)
		if _, err := writer.Write(binary); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return buffer.Bytes()
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("mihomo.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func writeMihariDist(t *testing.T, dir string) {
	t.Helper()
	for _, target := range []struct{ goos, goarch, ext string }{
		{"linux", "amd64", ""}, {"linux", "arm64", ""},
		{"darwin", "amd64", ""}, {"darwin", "arm64", ""},
		{"windows", "amd64", ".exe"}, {"windows", "arm64", ".exe"},
	} {
		name := "mihari-" + target.goos + "-" + target.goarch + target.ext
		if err := os.WriteFile(filepath.Join(dir, name), []byte("dist-"+name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func extractBundle(t *testing.T, path string) map[string][]byte {
	t.Helper()
	entries := map[string][]byte{}
	switch {
	case strings.HasSuffix(path, ".tar.gz"):
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		gz, err := gzip.NewReader(file)
		if err != nil {
			t.Fatal(err)
		}
		defer gz.Close()
		reader := tar.NewReader(gz)
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if header.Typeflag != tar.TypeReg {
				continue
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			entries[header.Name] = data
		}
	case strings.HasSuffix(path, ".zip"):
		reader, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		for _, file := range reader.File {
			if file.FileInfo().IsDir() {
				continue
			}
			rc, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			entries[file.Name] = data
		}
	default:
		t.Fatalf("unknown bundle type: %s", path)
	}
	return entries
}

func assertCoreChannelSidecar(t *testing.T, entries map[string][]byte, wantChannel, wantStamp string) {
	t.Helper()
	raw, ok := entries["data/bin/core-channel"]
	if !ok {
		t.Fatal("missing data/bin/core-channel")
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) < 2 {
		t.Fatalf("sidecar %q: want at least 2 lines", text)
	}
	if got := strings.TrimSpace(lines[0]); got != wantChannel {
		t.Fatalf("sidecar channel=%q want %q", got, wantChannel)
	}
	stamp := strings.TrimSpace(lines[1])
	if stamp == "" {
		t.Fatal("sidecar stamp is empty")
	}
	if wantStamp != "" && stamp != wantStamp {
		t.Fatalf("sidecar stamp=%q want %q", stamp, wantStamp)
	}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
