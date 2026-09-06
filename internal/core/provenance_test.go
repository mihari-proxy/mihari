package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSupportedPolicy_MatchesReleaseLock(t *testing.T) {
	data, err := os.ReadFile("../../scripts/release/release-inputs.lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Mihomo struct {
			Tag, Channel string
			Assets       map[string]struct {
				Name, URL, SHA256 string
				Size              int64
			}
		}
	}
	if err = json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	entries, err := supportedAssets()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("expected four compiled Unix assets, got %d", len(entries))
	}
	for _, a := range entries {
		v, ok := lock.Mihomo.Assets[a.OS+"/"+a.Arch]
		if !ok || a.OS == "windows" || a.Tag != lock.Mihomo.Tag || a.Channel != lock.Mihomo.Channel || a.Asset != v.Name || a.URL != v.URL || a.AssetSHA256 != v.SHA256 || a.Size != v.Size {
			t.Fatalf("compiled entry disagrees with lock: %s/%s", a.OS, a.Arch)
		}
	}
}
func TestSupportedPolicy_RejectsUnknown(t *testing.T) {
	for _, tt := range []struct{ name, os, arch, tag, channel string }{{"unknown-tag", "linux", "amd64", "v9.99.0", "stable"}, {"alpha-no-entry", "linux", "amd64", "alpha-deadbeef", "alpha"}, {"windows-legacy", "windows", "amd64", "v1.19.30", "stable"}} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := supportedCore(context.Background(), tt.os, tt.arch, tt.tag, tt.channel); err == nil {
				t.Fatal("unsupported policy accepted")
			}
		})
	}
}

func TestVerifiedCommand_PurposeAndEnvironment(t *testing.T) {
	s := newMemoryStore()
	seedPair(t, s, true)
	b, _ := s.open(context.Background(), InstalledBinary, "")
	r, _ := s.open(context.Background(), InstalledReceipt, "")
	v := &VerifiedCore{store: s, binary: b, receipt: r, installed: true}
	command, err := v.Command(context.Background(), CoreVersion, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(command.Args) != 1 || command.Args[0] != "-v" || command.Binary == "" || len(command.Env) == 0 {
		t.Fatal("version command is not capability bound")
	}
	configuration := &ConfigCapability{root: s.location(), file: &memoryVerifiedFile{s: s, role: CandidateReceipt, tx: testTransaction, path: "/private/data/staging/config-first.yaml", observed: mustInspect(t, s, CandidateReceipt, testTransaction)}}
	command, err = v.Command(context.Background(), CoreValidate, configuration)
	if err != nil {
		t.Fatal(err)
	}
	if command.Config != "/private/data/staging/config-first.yaml" || command.Home != filepath.Join(s.location(), "runtime", "core-home") || len(command.Args) != 5 {
		t.Fatal("validation ignored selected config or home")
	}
	for _, bad := range []CorePurpose{CoreRun, CorePurpose(99)} {
		if _, err = v.Command(context.Background(), bad, configuration); err == nil {
			t.Fatal("invalid run allowed")
		}
	}
}
func mustInspect(t *testing.T, s ProvenanceStore, r ProvenanceRole, tx string) ProvenanceObject {
	t.Helper()
	v, e := s.Inspect(context.Background(), r, tx)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestConfigCapability_ChangedBytesRejected(t *testing.T) {
	s := newMemoryStore()
	seedPair(t, s, true)
	b, _ := s.open(context.Background(), InstalledBinary, "")
	r, _ := s.open(context.Background(), InstalledReceipt, "")
	v := &VerifiedCore{store: s, binary: b, receipt: r, installed: true}
	f, _ := s.open(context.Background(), CandidateReceipt, testTransaction)
	c := &ConfigCapability{file: f, root: s.location()}
	if err := s.Save(context.Background(), CandidateReceipt, testTransaction, []byte("other config")); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Command(context.Background(), CoreValidate, c); err == nil {
		t.Fatal("changed config accepted")
	}
}

type recordedExecutor struct {
	store                      *memoryStore
	binaryHashes, configHashes []string
	commands                   []CoreCommand
	failValidation             bool
	oldBinary                  string
}

func (x *recordedExecutor) Execute(ctx context.Context, c CoreCommand) ([]byte, error) {
	x.commands = append(x.commands, c)
	if x.store != nil {
		binary := x.store.disk.files[strings.TrimPrefix(c.Binary, x.store.location()+"/")].bytes
		configuration := x.store.disk.files[strings.TrimPrefix(c.Config, x.store.location()+"/")].bytes
		x.binaryHashes = append(x.binaryHashes, digest(binary))
		x.configHashes = append(x.configHashes, digest(configuration))
	}
	if len(c.Args) > 0 && c.Args[0] == "-t" && x.failValidation && c.Binary != x.oldBinary {
		return nil, errors.New("candidate rejected")
	}
	return []byte("Mihomo v1.19.30"), nil
}
func trustedFixture(t *testing.T) (*memoryStore, Installer, *recordedExecutor) {
	t.Helper()
	s := newMemoryStore()
	if e := s.Save(context.Background(), CandidateReceipt, testTransaction, []byte("generated config")); e != nil {
		t.Fatal(e)
	}
	x := &recordedExecutor{store: s}
	i := Installer{Provenance: s, Executor: x, GeneratedConfig: func(ctx context.Context) (*ConfigCapability, error) {
		f, e := s.open(ctx, CandidateReceipt, testTransaction)
		return &ConfigCapability{root: s.location(), file: f}, e
	}}
	return s, i, x
}
func stageFixture(t *testing.T, i Installer) *Candidate {
	t.Helper()
	a, e := supportedCore(context.Background(), "linux", "amd64", "v1.19.30", "stable")
	if e != nil {
		t.Fatal(e)
	}
	c, e := i.stageTrustedBinary(context.Background(), a, []byte("authenticated candidate binary"))
	if e != nil {
		t.Fatal(e)
	}
	return c.(*Candidate)
}
func TestTrustedCandidate_GreenInstallWithoutInstalledPair(t *testing.T) {
	s, i, x := trustedFixture(t)
	c := stageFixture(t, i)
	defer c.Cleanup()
	if len(x.commands) != 2 || x.commands[0].Args[0] != "-v" || x.commands[1].Args[0] != "-t" {
		t.Fatal("candidate version/validation not called once each")
	}
	if s.disk.files[objectKey(InstalledBinary, "")].bytes != nil {
		t.Fatal("prepare published binary")
	}
	for index, cmd := range x.commands {
		if x.binaryHashes[index] != digest([]byte("authenticated candidate binary")) {
			t.Fatal("executor saw different candidate binary bytes")
		}
		if !strings.Contains(cmd.Binary, c.trusted.transaction) {
			t.Fatal("candidate command used installed binary")
		}
	}
	if _, e := c.Commit(); e != nil {
		t.Fatal(e)
	}
	v, e := OpenInstalledCore(context.Background(), s)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if closeErr := v.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if _, e = c.Verified(context.Background()); e == nil {
		t.Fatal("committed candidate still usable")
	}
}
func TestTrustedCandidate_UpdateExecutesCandidateNotOld(t *testing.T) {
	s, i, x := trustedFixture(t)
	seedInstalledReceipt(t, s, "old trusted binary")
	before := mustInspect(t, s, InstalledBinary, "")
	x.failValidation = true
	x.oldBinary = "/private/data/binary"
	a, _ := supportedCore(context.Background(), "linux", "amd64", "v1.19.30", "stable")
	c, e := i.stageTrustedBinary(context.Background(), a, []byte("new rejects validation"))
	if e == nil || c != nil {
		t.Fatal("new -t failure accepted")
	}
	if mustInspect(t, s, InstalledBinary, "") != before {
		t.Fatal("failed candidate changed old binary")
	}
	if len(x.commands) != 2 {
		t.Fatalf("expected candidate -v/-t, got %d", len(x.commands))
	}
	for _, cmd := range x.commands {
		if cmd.Binary == "/private/data/binary" {
			t.Fatal("old binary executed instead of candidate")
		}
	}
}
func TestTrustedCandidate_CannotRunBeforeCommit(t *testing.T) {
	_, i, x := trustedFixture(t)
	c := stageFixture(t, i)
	defer c.Cleanup()
	v, e := c.Verified(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if closeErr := v.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	configuration, e := i.GeneratedConfig(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if closeErr := configuration.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	before := len(x.commands)
	if _, e = executeVerified(context.Background(), v, CoreRun, configuration, x); e == nil {
		t.Fatal("candidate run permitted")
	}
	if len(x.commands) != before {
		t.Fatal("executor called for candidate run")
	}
}
func seedInstalledReceipt(t *testing.T, s *memoryStore, binary string) {
	t.Helper()
	a, e := supportedCore(context.Background(), "linux", "amd64", "v1.19.30", "stable")
	if e != nil {
		t.Fatal(e)
	}
	r := ProvenanceReceipt{Schema: provenanceSchema, PolicyID: subscription.RootPolicyID, AssetSHA256: a.AssetSHA256, BinarySHA256: digest([]byte(binary)), OS: a.OS, Arch: a.Arch, Tag: a.Tag}
	rb, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	for role, b := range map[ProvenanceRole][]byte{InstalledBinary: []byte(binary), InstalledReceipt: rb} {
		if e = s.Save(context.Background(), role, "", b); e != nil {
			t.Fatal(e)
		}
	}
}
func TestInstalledProvenance_RejectsBeforeExecution(t *testing.T) {
	for _, name := range []string{"missing-receipt", "binary-hash-mismatch", "unknown-tag", "alpha-no-entry", "asset-hash-mismatch", "user-owned-receipt"} {
		t.Run(name, func(t *testing.T) {
			s := newMemoryStore()
			seedInstalledReceipt(t, s, "trusted binary")
			rb, _ := s.Load(context.Background(), InstalledReceipt, "")
			var r ProvenanceReceipt
			if e := json.Unmarshal(rb, &r); e != nil {
				t.Fatal(e)
			}
			switch name {
			case "missing-receipt":
				delete(s.disk.files, objectKey(InstalledReceipt, ""))
			case "binary-hash-mismatch":
				s.disk.files[objectKey(InstalledBinary, "")] = memoryObject{[]byte("changed"), "changed"}
			case "unknown-tag":
				r.Tag = "v1.99.0"
			case "alpha-no-entry":
				r.Tag = "alpha-deadbeef"
			case "asset-hash-mismatch":
				r.AssetSHA256 = strings.Repeat("0", 64)
			case "user-owned-receipt":
				s.denyReceipt = true
			}
			if name != "missing-receipt" && name != "user-owned-receipt" {
				rb, _ = json.Marshal(r)
				if e := s.Save(context.Background(), InstalledReceipt, "", rb); e != nil {
					t.Fatal(e)
				}
			}
			x := &recordedExecutor{}
			v, e := OpenInstalledCore(context.Background(), s)
			if e == nil {
				defer func() {
					if closeErr := v.Close(); closeErr != nil {
						t.Error(closeErr)
					}
				}()
				_, e = DetectVerifiedVersion(context.Background(), v, x)
			}
			if e == nil || len(x.commands) != 0 {
				t.Fatal("invalid provenance reached executor")
			}
		})
	}
}
func TestTrustedCandidate_ReplacedAndClosedRejected(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprint(replace), func(t *testing.T) {
			s, i, x := trustedFixture(t)
			c := stageFixture(t, i)
			v, e := c.Verified(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			if replace {
				key := objectKey(CandidateBinary, c.trusted.transaction)
				value := s.disk.files[key]
				value.id = "attacker-replacement"
				s.disk.files[key] = value
			} else {
				c.Cleanup()
			}
			before := len(x.commands)
			if _, e = executeVerified(context.Background(), v, CoreVersion, nil, x); e == nil {
				t.Fatal("changed/closed candidate executed")
			}
			if _, e = c.Commit(); e == nil {
				t.Fatal("changed/closed candidate committed")
			}
			if len(x.commands) != before {
				t.Fatal("invalid candidate called executor")
			}
			if e = v.Close(); e != nil {
				t.Fatal(e)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTrustedPrepare_BadCompressedHashNeverStagesOrExecutes(t *testing.T) {
	entries, e := supportedAssets()
	if e != nil {
		t.Fatal(e)
	}
	for _, a := range entries {
		t.Run(a.OS+"/"+a.Arch, func(t *testing.T) {
			s, i, x := trustedFixture(t)
			initial := len(s.disk.files)
			requests := 0
			i.GOOS = a.OS
			i.GOARCH = a.Arch
			i.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				if r.URL.String() != a.URL {
					t.Fatal("download did not use compiled URL")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("hostile gzip")), Header: make(http.Header)}, nil
			})}
			if c, e := i.Prepare(context.Background(), InstallRequest{Channel: "stable"}); e == nil || c != nil {
				t.Fatal("bad compressed asset accepted")
			}
			if len(s.disk.files) != initial || len(x.commands) != 0 || requests != 1 {
				t.Fatal("digest rejection staged or executed a candidate")
			}
			requests = 0
			if c, e := i.Prepare(context.Background(), InstallRequest{Channel: "alpha"}); e == nil || c != nil {
				t.Fatal("alpha unexpectedly supported")
			}
			if requests != 0 {
				t.Fatal("unknown policy made a network request")
			}
		})
	}
}
