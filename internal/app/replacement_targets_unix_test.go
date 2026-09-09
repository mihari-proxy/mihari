//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/update"
)

func TestReplacementTargets_ActualPathsAndDefinition(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	managed := filepath.Join(root, "mihari")
	path := filepath.Join(root, "path-mihari")
	for _, p := range []string{managed, path} {
		if err := os.WriteFile(p, []byte("inert old binary"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	layout := platform.ResolvedLayout{InstallRoot: root}
	req := InstallRequest{Operation: InstallOperationUpdate, PathBinary: path}
	def := service.Definition{Status: service.StatusRunning, Running: true, Binary: managed, Args: []string{"daemon"}, Env: []string{"MIHARI_DATA=/private"}, Files: []service.DefinitionFile{{Path: "/fixture/unit", Bytes: []byte("definition")}}}
	first, err := observeUnixReplacementTargets(ctx, req, layout, "/unrelated-helper", def, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Targets) != 2 {
		t.Fatalf("targets=%+v", first.Targets)
	}
	for j := range first.Targets {
		first.Targets[j].Version = "v2.0.0"
	}
	def.Status = service.StatusStopped
	def.Running = false
	def.Process.PID = 123
	stopped, err := observeUnixReplacementTargets(ctx, req, layout, "/unrelated-helper", def, &first)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, stopped) {
		t.Fatal("transient service state changed the replacement snapshot")
	}
	def.Args = append(def.Args, "--changed")
	changed, err := observeUnixReplacementTargets(ctx, req, layout, "/unrelated-helper", def, &first)
	if err != nil {
		t.Fatal(err)
	}
	if first.ServiceDefinitionSHA256 == changed.ServiceDefinitionSHA256 {
		t.Fatal("persistent service arguments not bound")
	}
	if err := os.WriteFile(managed, []byte("same version different contents"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err = observeUnixReplacementTargets(ctx, req, layout, "/unrelated-helper", def, &first)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range changed.Targets {
		if target.Path == managed && target.Version != "" {
			t.Fatal("version was reused for changed bytes")
		}
	}
}

func TestReplacementTargets_StandaloneNeverUsesInstallRoot(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "mihari")
	if err := os.WriteFile(binary, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	forbidden := filepath.Join(root, "B")
	if err := os.WriteFile(forbidden, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	i := &UnixInstaller{binary: binary, layout: platform.ResolvedLayout{InstallRoot: forbidden}, adapter: func(service.ActionHook) service.RecoveryAdapter {
		return replacementDefinitionAdapter{definition: service.Definition{Status: service.StatusNotInstalled}}
	}}
	got, err := i.ObserveReplacement(context.Background(), binary)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Targets) != 1 || got.Targets[0].Path != binary {
		t.Fatalf("wrong standalone targets: %+v", got)
	}
}

type replacementDefinitionAdapter struct {
	service.RecoveryAdapter
	definition service.Definition
}

func (a replacementDefinitionAdapter) InspectDefinition(context.Context) (service.Definition, error) {
	return a.definition, nil
}

type replacementTransport func(*http.Request) (*http.Response, error)

func (f replacementTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestInstallReplacement_PublicApplyRejectsBeforeOpeningBinaryLease(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("Unix installer requires root; only temporary inert files are used")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "mihari")
	candidate := filepath.Join(root, "candidate")
	for p, b := range map[string]string{binary: "old inert bytes", candidate: "verified new bytes"} {
		if err := os.WriteFile(p, []byte(b), 0600); err != nil {
			t.Fatal(err)
		}
	}
	i := &UnixInstaller{binary: binary, channel: "main", adapter: func(service.ActionHook) service.RecoveryAdapter {
		return replacementDefinitionAdapter{definition: service.Definition{Status: service.StatusNotInstalled}}
	}, client: &http.Client{Transport: replacementTransport(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/v1.0.0/") {
			t.Fatalf("candidate was not fixed: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(sha256Hex("verified new bytes") + "  mihari-" + runtime.GOOS + "-" + runtime.GOARCH + "\n")), Header: make(http.Header)}, nil
	})}}
	req := InstallRequest{Schema: InstallRequestSchema, Operation: InstallOperationUpdate, Layout: InstallLayoutSystem, Binary: candidate, ReleaseTag: "v1.0.0", Channel: "main"}
	snapshot, err := i.ObserveReplacement(context.Background(), binary)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := update.NewReplacementPreview(update.ReplacementCandidate{Version: req.ReleaseTag, Channel: req.Channel, SHA256: sha256Hex("verified new bytes")}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = i.ApplyWithConsent(context.Background(), req, update.ReplacementConsent{})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidArgument {
		t.Fatalf("want risk confirmation, got %v", err)
	}
	after, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatal("unconfirmed call created a lock/stage")
	}
	for _, consent := range []update.ReplacementConsent{{ExpectedPreview: preview.ID}, {Yes: true, ExpectedPreview: strings.Repeat("f", 64)}, {Yes: true, Warn: func(string) error { return errors.New("warning output failed") }}} {
		if _, err := i.ApplyWithConsent(context.Background(), req, consent); err == nil {
			t.Fatal("invalid consent or failed warning was ignored")
		}
	}
	if err := os.WriteFile(binary, []byte("changed old bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, yes := range []bool{false, true} {
		stale := preview
		result, err := i.ApplyPrepared(context.Background(), update.PreparedUpdate{Available: true, Version: req.ReleaseTag, Channel: req.Channel, CandidatePath: candidate, SHA256: preview.Candidate.SHA256, TargetPath: binary, Preview: stale, Consent: update.ReplacementConsent{Yes: yes}})
		if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState || result.Updated {
			t.Fatalf("prepared snapshot was not checked independently: yes=%v result=%+v err=%v", yes, result, err)
		}
	}
	raw, err := os.ReadFile(binary)
	if err != nil || string(raw) != "changed old bytes" {
		t.Fatalf("binary changed: %v", err)
	}
}

func TestInstallReplacement_ServiceSessionRejectsBeforeLoadAndRecovery(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale", true: "pending"}[pending], func(t *testing.T) {
			h := newInstallHarness(t, InstallDataRetain)
			if pending {
				h.tx.crash = &installCrashSpec{index: 1, point: "after-intent"}
				if !catchInstallCrash(func() { _, _ = h.tx.Apply(context.Background(), h.req) }) {
					t.Fatal("fixture did not stop at pending intent")
				}
				if object, err := h.tx.Store.files.inspect(context.Background(), installJournalFileName); err != nil || !object.Present {
					t.Fatalf("pending fixture absent: %v", err)
				}
			}
			before := h.disk.snapshot()
			p, err := update.NewReplacementPreview(update.ReplacementCandidate{Version: "v1.0.0"}, update.ReplacementSnapshot{})
			if err != nil {
				t.Fatal(err)
			}
			checked := 0
			guard := &installReplacementGuard{preview: p, recheck: func(context.Context) error { checked++; return errors.New("stale replacement") }}
			session := &nativeInstallSession{tx: h.tx}
			_, err = (&UnixInstaller{}).applyServiceSession(context.Background(), h.req, service.Definition{}, false, guard, nil, session, platform.ResolvedLayout{})
			if err == nil || before != h.disk.snapshot() || session.state.TransactionID != "" {
				t.Fatalf("rejection loaded or mutated journal: %v", err)
			}
			if pending && checked != 0 {
				t.Fatal("pending was not rejected before snapshot use")
			}
			if err := session.cleanupCompletedCandidates(context.Background()); err != nil {
				t.Fatal(err)
			}
			if before != h.disk.snapshot() {
				t.Fatal("rejected session cleanup changed disk")
			}
		})
	}
}

func TestNativeInstallReplacement_StandaloneUpgradeWithoutYes(t *testing.T) {
	ctx, root := nativeInstallFixture(t)
	binary := filepath.Join(root, "mihari")
	candidate := filepath.Join(root, "candidate")
	probeLog := filepath.Join(root, "version-probes")
	script := []byte("#!/bin/sh\nprintf x >> '" + strings.ReplaceAll(probeLog, "'", "'\"'\"'") + "'\nprintf '%s\\n' '{\"schema\":\"mihari/v1\",\"version\":\"v1.0.0\"}'\n")
	if err := os.WriteFile(binary, script, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("verified v2 bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	layout, err := platform.ResolveLayout(platform.LayoutInput{EUID: 0}, platform.SystemLayoutDefaults())
	if err != nil {
		t.Fatal(err)
	}
	// An invalid B makes any accidental installation-root access fail the test.
	layout.InstallRoot = filepath.Join(root, "forbidden-B")
	if err := os.WriteFile(layout.InstallRoot, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	i := &UnixInstaller{layout: layout, binary: binary, channel: "main", adapter: func(service.ActionHook) service.RecoveryAdapter {
		return replacementDefinitionAdapter{definition: service.Definition{Status: service.StatusNotInstalled}}
	}, client: &http.Client{Transport: replacementTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(sha256Hex("verified v2 bytes") + "  mihari-" + runtime.GOOS + "-" + runtime.GOARCH + "\n")), Header: make(http.Header)}, nil
	})}}
	snapshot, err := i.ObserveReplacement(ctx, binary)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Targets[0].Version != "v1.0.0" {
		t.Fatalf("isolated fixture did not obtain trusted version: %+v", snapshot)
	}
	preview, err := update.NewReplacementPreview(update.ReplacementCandidate{Version: "v2.0.0", Channel: "main", SHA256: sha256Hex("verified v2 bytes")}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	p := update.PreparedUpdate{Available: true, Version: "v2.0.0", Channel: "main", CandidatePath: candidate, SHA256: preview.Candidate.SHA256, TargetPath: binary, Preview: preview}
	// Same-version byte changes invalidate the prepared snapshot even without yes.
	if err := os.WriteFile(binary, append(append([]byte(nil), script...), []byte("# changed\n")...), 0755); err != nil {
		t.Fatal(err)
	}
	for _, yes := range []bool{false, true} {
		p.Consent.Yes = yes
		if result, err := i.ApplyPrepared(ctx, p); err == nil || result.Updated {
			t.Fatalf("stale same-version target accepted: yes=%v result=%+v err=%v", yes, result, err)
		}
	}
	if err := os.WriteFile(binary, script, 0755); err != nil {
		t.Fatal(err)
	}
	p.Consent = update.ReplacementConsent{}
	result, err := i.ApplyPrepared(ctx, p)
	if err != nil || !result.Updated {
		t.Fatalf("ordinary prepared upgrade without yes failed: %+v %v", result, err)
	}
	probes, err := os.ReadFile(probeLog)
	if err != nil || string(probes) != "xxxx" {
		t.Fatalf("version probe ran under the commit lease: probes=%q err=%v", probes, err)
	}
	bytes, err := os.ReadFile(binary)
	if err != nil || string(bytes) != "verified v2 bytes" {
		t.Fatalf("replacement was not published: %v", err)
	}
}
