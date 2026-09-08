package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

func TestUnixMigration_PreservesFunction(t *testing.T) {
	fx := newMigrationFixture(t)
	before := fx.sourceHashes(t)
	prepared, err := prepareMigration(context.Background(), fx.options())
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.cleanup()
	if prepared.activeID != fx.profileID {
		t.Fatalf("active id=%q want %q", prepared.activeID, fx.profileID)
	}
	if !prepared.hasRel("subscriptions/catalog.yaml") || !prepared.hasRel("subscriptions/cache/"+fx.profileID+".yaml") {
		t.Fatal("active catalog/cache refs were not preserved")
	}
	if prepared.hasRel("control.token") || prepared.hasRel("web/credential") {
		t.Fatal("secret was copied")
	}
	if bytes.Equal(prepared.runtimeYAML, fx.oldRuntime) {
		t.Fatal("runtime/config.yaml was copied instead of regenerated")
	}
	if !bytes.Contains(prepared.runtimeYAML, []byte("secret:")) {
		t.Fatal("regenerated runtime missing managed secret")
	}
	if bytes.Contains(prepared.runtimeYAML, []byte(fx.oldSecret)) {
		t.Fatal("old controller secret leaked into regenerated runtime")
	}
	if _, ok := prepared.files["runtime/config.yaml"]; !ok {
		t.Fatal("regenerated runtime was not staged")
	}
	after := fx.sourceHashes(t)
	if !mapsEqual(before, after) {
		t.Fatal("old tree hashes changed")
	}
	if _, err := os.Stat(fx.source.osPath("control.token")); err != nil {
		t.Fatal("source was deleted or lost secrets")
	}
}

func TestUnixMigration_NegativeCases(t *testing.T) {
	cases := []string{
		"missing-active-cache", "missing-provider-resource", "untrusted-core",
		"unknown-top-level", "nested-source-target", "concurrent-business-write",
		"oversize", "hardlink", "nested-mount",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newMigrationFixture(t)
			opts := fx.options()
			wantCode := protocol.CodeDataFailure
			switch name {
			case "missing-active-cache":
				if err := os.Remove(fx.source.osPath("subscriptions/cache/" + fx.profileID + ".yaml")); err != nil {
					t.Fatal(err)
				}
			case "missing-provider-resource":
				if err := os.Remove(fx.source.osPath("runtime/core-home/providers/" + fx.fileProviderID + ".yaml")); err != nil {
					t.Fatal(err)
				}
			case "untrusted-core":
				if err := os.WriteFile(fx.source.osPath("bin/mihomo"), []byte("evil-core"), 0o700); err != nil {
					t.Fatal(err)
				}
				wantCode = protocol.CodeInvalidState
			case "unknown-top-level":
				if err := os.WriteFile(fx.source.osPath("evil.bin"), []byte("nope"), 0o600); err != nil {
					t.Fatal(err)
				}
				wantCode = protocol.CodeInvalidState
			case "nested-source-target":
				nested := openDirCap(filepath.Join(fx.source.dir, "nested-target"))
				if err := os.MkdirAll(nested.dir, 0o700); err != nil {
					t.Fatal(err)
				}
				opts.Target = nested
				wantCode = protocol.CodeInvalidArgument
			case "concurrent-business-write":
				opts.AfterCopy = func() {
					_ = os.WriteFile(fx.source.osPath("mihari.yaml"), append(fx.settingsYAML, []byte("\n# mutated\n")...), 0o600)
				}
				wantCode = protocol.CodeRevisionConflict
			case "oversize":
				if err := os.WriteFile(fx.source.osPath("mihari.yaml"), bytes.Repeat([]byte("a"), migrationSettingsMax+1), 0o600); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				fx.source.setFlags("mihari.yaml", nodeFlags{hardlink: true, nlink: 2})
				wantCode = protocol.CodeInvalidArgument
			case "nested-mount":
				fx.source.setFlags("subscriptions", nodeFlags{nestedMount: true})
				wantCode = protocol.CodeInvalidArgument
			}
			before := fx.sourceHashes(t)
			defBefore := append([]byte(nil), fx.serviceDef...)
			_, err := prepareMigration(context.Background(), opts)
			if err == nil {
				t.Fatalf("%s: expected failure", name)
			}
			if strings.Contains(err.Error(), "not implemented") {
				t.Fatalf("%s: missing rejection behavior: %v", name, err)
			}
			if apiCode(err) != wantCode {
				t.Fatalf("%s: code=%q want %q err=%v", name, apiCode(err), wantCode, err)
			}
			after := fx.sourceHashes(t)
			if name == "concurrent-business-write" {
				for rel, hash := range before {
					if rel == "mihari.yaml" {
						continue
					}
					if after[rel] != hash {
						t.Fatalf("%s: old data changed: %s", name, rel)
					}
				}
			} else if !mapsEqual(before, after) {
				t.Fatalf("%s: old data changed", name)
			}
			if !bytes.Equal(defBefore, fx.serviceDef) {
				t.Fatalf("%s: old service definition changed", name)
			}
		})
	}
}

type migrationFixture struct {
	source, target, staging *dirCap
	trust                   migrationTrust
	profileID               string
	fileProviderID          string
	httpProviderID          string
	oldSecret               string
	oldRuntime              []byte
	settingsYAML            []byte
	binaryPath              string
	binary                  []byte
	core                    []byte
	serviceDef              []byte
	sourceSnap              map[string]string
}

func newMigrationFixture(t *testing.T) *migrationFixture {
	t.Helper()
	root := t.TempDir()
	fx := &migrationFixture{
		source:         openDirCap(filepath.Join(root, "source")),
		target:         openDirCap(filepath.Join(root, "target")),
		staging:        openDirCap(filepath.Join(root, "staging")),
		profileID:      "00000000000000000000000000000001",
		fileProviderID: strings.Repeat("ab", 32),
		oldSecret:      strings.Repeat("aa", 32),
		serviceDef:     []byte("old-unit-definition"),
		binary:         []byte("trusted-mihari-binary"),
		core:           []byte("trusted-mihomo-core"),
	}
	for _, cap := range []*dirCap{fx.source, fx.target, fx.staging} {
		if err := os.MkdirAll(cap.dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	fx.httpProviderID, err = subscription.ProviderResourceID(fx.profileID, 1, "proxy", "http-nodes")
	if err != nil {
		t.Fatal(err)
	}
	fx.settingsYAML = []byte("schema: mihari.settings/v1\nmixed-addr: 127.0.0.1:9190\ncontroller-addr: 127.0.0.1:9090\nweb-addr: 127.0.0.1:9191\ncontroller-secret: " + fx.oldSecret + "\ncore-channel: stable\nlog:\n  level: info\n  max-size-mb: 10\n  max-files: 3\n")
	catalog := []byte("schema: mihari.subscriptions/v1\nglobal-interval: 12h\nactive-id: " + fx.profileID + "\nprofiles:\n  - id: " + fx.profileID + "\n    name: fixture\n    url: https://example.test/subscription\n    enabled: true\n    auto-refresh: true\n    generation: 1\n")
	cache := []byte("proxies:\n  - {name: node, type: direct}\nproxy-groups: []\nproxy-providers:\n  http-nodes:\n    type: http\n    url: https://example.test/provider\n  file-nodes:\n    type: file\n    path: " + fx.fileProviderID + "\n  inline-nodes:\n    type: inline\n    payload:\n      - {name: inline-node, type: direct}\nrules:\n  - MATCH,DIRECT\n")
	httpRes := []byte("proxies: [{name: http-node, type: direct}]\n")
	fileRes := []byte("proxies: [{name: file-node, type: direct}]\n")
	fx.oldRuntime = []byte("mixed-port: 1\nsecret: " + fx.oldSecret + "\n")
	onboarding := []byte("{\"schema\":\"mihari.onboarding/v1\",\"complete\":true}\n")
	tui := []byte("{\"schema\":\"mihari.tui-preferences/v1\",\"connections_columns\":[\"host\",\"network\",\"source\",\"destination\",\"chain\",\"rule\",\"traffic\"]}\n")
	active := []byte("{\"panel\":\"zashboard\",\"build\":\"testbuild\"}\n")
	country := readMigrationMMDB(t, "country.mmdb")
	asn := readMigrationMMDB(t, "asn.mmdb")
	panelZip := writePanelZip(t)
	fx.binaryPath = filepath.Join(root, "mihari-candidate")
	mustWrite(t, fx.binaryPath, fx.binary)
	files := map[string][]byte{
		"mihari.yaml":                fx.settingsYAML,
		"onboarding.json":            onboarding,
		"control.token":              []byte("old-control-token\n"),
		"subscriptions/catalog.yaml": catalog,
		"subscriptions/cache/" + fx.profileID + ".yaml":              cache,
		"runtime/config.yaml":                                        fx.oldRuntime,
		"runtime/core-home/providers/" + fx.httpProviderID + ".yaml": httpRes,
		"runtime/core-home/providers/" + fx.fileProviderID + ".yaml": fileRes,
		"preferences/tui.json":                                       tui,
		"bin/mihomo":                                                 fx.core,
		"geoip/GeoLite2-Country.mmdb":                                country,
		"geoip/GeoLite2-ASN.mmdb":                                    asn,
		"web/credential":                                             []byte("old-web-credential"),
		"web/active.json":                                            active,
		"logs/mihari-daemon.log":                                     []byte("old-log"),
		"logs-export/keep.zip":                                       []byte("old-zip"),
		"staging/tmp":                                                []byte("tmp"),
	}
	for rel, data := range files {
		mustWrite(t, fx.source.osPath(rel), data)
	}
	fx.trust = migrationTrust{
		core:   map[string]struct{}{sha256HexBytes(fx.core): {}},
		geo:    map[string]struct{}{sha256HexBytes(country): {}, sha256HexBytes(asn): {}},
		panel:  map[string][]byte{"zashboard/testbuild": panelZip},
		binary: map[string]struct{}{sha256HexBytes(fx.binary): {}},
	}
	fx.sourceSnap = fx.sourceHashes(t)
	return fx
}

func (fx *migrationFixture) options() migrationOptions {
	return migrationOptions{
		Source:  fx.source,
		Target:  fx.target,
		Staging: fx.staging,
		Request: fx.request(),
		Trust:   fx.trust,
		GOOS:    "linux",
		GOARCH:  "amd64",
		NewSecret: func() string {
			var raw [32]byte
			_, _ = rand.Read(raw[:])
			return hex.EncodeToString(raw[:])
		},
	}
}

func (fx *migrationFixture) request() InstallRequest {
	return InstallRequest{
		Schema:    InstallRequestSchema,
		Operation: InstallOperationInstall,
		Binary:    filepath.ToSlash(fx.binaryPath),
		Channel:   InstallChannelMain,
		Layout:    InstallLayoutSystem,
		Source:    filepath.ToSlash(fx.source.dir),
	}
}

func (fx *migrationFixture) sourceHashes(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(fx.source.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(fx.source.dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = sha256HexBytes(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (fx *migrationFixture) assertSourceUnchanged(t *testing.T) {
	t.Helper()
	if !mapsEqual(fx.sourceSnap, fx.sourceHashes(t)) {
		t.Fatal("old tree hashes changed")
	}
}

func readMigrationMMDB(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "subscription", "testdata", "rootpolicy", "mmdb", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writePanelZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("index.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("<html>ok</html>")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func ptrOptions(opts migrationOptions) *migrationOptions { return &opts }
