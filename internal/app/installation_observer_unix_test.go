//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
)

type fakeUnixInstallationControl struct {
	state      []byte
	readErr    error
	locked     bool
	probeErr   error
	readCalls  int
	probeCalls int
	closeCalls int
}

func (f *fakeUnixInstallationControl) ReadState(context.Context) ([]byte, string, error) {
	f.readCalls++
	return append([]byte(nil), f.state...), installationSHA256(f.state), f.readErr
}
func (f *fakeUnixInstallationControl) ProbeOperation(context.Context) (bool, error) {
	f.probeCalls++
	return f.locked, f.probeErr
}
func (f *fakeUnixInstallationControl) Close() error { f.closeCalls++; return nil }

type fakeUnixInstallationResources struct {
	matches     bool
	verifyErr   error
	legacy      *InstallationManifest
	legacyErr   error
	verifyCalls int
	legacyCalls int
}

func (f *fakeUnixInstallationResources) Verify(context.Context, InstallationManifest, service.Definition) (bool, error) {
	f.verifyCalls++
	return f.matches, f.verifyErr
}
func (f *fakeUnixInstallationResources) VerifiedLegacy(context.Context) (*InstallationManifest, error) {
	f.legacyCalls++
	return cloneInstallationManifestPointer(f.legacy), f.legacyErr
}

type fakeUnixInstallationService struct {
	definition service.Definition
	err        error
	calls      int
}

func (f *fakeUnixInstallationService) InspectDefinition(context.Context) (service.Definition, error) {
	f.calls++
	return f.definition, f.err
}

func TestUnixInstallationObserver_SnapshotDistinguishesAbsentActiveAndInvalidK(t *testing.T) {
	raw := mustInstallationStateJSON(t, testInstallationState(InstallationStateApplying))
	legacy := testInstallationManifest()
	tests := []struct {
		name       string
		control    *fakeUnixInstallationControl
		openErr    error
		legacy     *InstallationManifest
		want       InstallationSnapshot
		wantErr    error
		wantLegacy int
	}{
		{name: "absent", openErr: os.ErrNotExist, want: InstallationSnapshot{}, wantLegacy: 1},
		{name: "verified legacy", openErr: os.ErrNotExist, legacy: legacy, want: InstallationSnapshot{Legacy: legacy}, wantLegacy: 1},
		{name: "permission", openErr: os.ErrPermission, wantErr: ErrInstallationPermissionRequired},
		{name: "existing malformed", openErr: platform.ErrInstallControlUninitialized, wantErr: ErrInstallationObservationUnknown},
		{name: "active", control: &fakeUnixInstallationControl{state: raw, locked: true}, want: InstallationSnapshot{Present: true, OperationLocked: true, State: raw}},
		{name: "inactive", control: &fakeUnixInstallationControl{state: raw}, want: InstallationSnapshot{Present: true, State: raw}},
		{name: "state vanished after open", control: &fakeUnixInstallationControl{readErr: os.ErrNotExist}, wantErr: ErrInstallationObservationUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resources := &fakeUnixInstallationResources{legacy: tc.legacy}
			observer := newUnixInstallationObserver(unixInstallationObserverDeps{
				openControl: func(context.Context) (unixInstallationControl, error) { return tc.control, tc.openErr },
				resources:   resources,
			})
			got, err := observer.Snapshot(context.Background())
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Snapshot error=%v want=%v", err, tc.wantErr)
			}
			if !sameInstallationSnapshot(got, tc.want) {
				t.Fatalf("Snapshot=%+v want=%+v", got, tc.want)
			}
			if resources.legacyCalls != tc.wantLegacy {
				t.Fatalf("legacy calls=%d", resources.legacyCalls)
			}
			if tc.control != nil && tc.control.closeCalls != 1 {
				t.Fatalf("control close calls=%d", tc.control.closeCalls)
			}
		})
	}
}

func TestUnixInstallationObserver_VerifyManifestUsesOnlyReadOnlyEvidence(t *testing.T) {
	manifest := *testInstallationManifest()
	tests := []struct {
		name    string
		match   bool
		err     error
		want    bool
		wantErr error
	}{
		{name: "matches", match: true, want: true},
		{name: "resource mismatch"},
		{name: "permission", err: os.ErrPermission, wantErr: ErrInstallationPermissionRequired},
		{name: "identity unknown", err: platform.ErrIdentityMismatch, wantErr: ErrInstallationObservationUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resources := &fakeUnixInstallationResources{matches: tc.match, verifyErr: tc.err}
			serviceObserver := &fakeUnixInstallationService{definition: service.Definition{Status: service.StatusStopped}}
			observer := newUnixInstallationObserver(unixInstallationObserverDeps{service: serviceObserver, resources: resources})
			got, err := observer.VerifyManifest(context.Background(), manifest)
			if !errors.Is(err, tc.wantErr) || got.Matches != tc.want {
				t.Fatalf("observation=%+v error=%v", got, err)
			}
			if resources.verifyCalls != 1 || serviceObserver.calls != 1 {
				t.Fatalf("resource calls=%d service calls=%d", resources.verifyCalls, serviceObserver.calls)
			}
		})
	}
}

func TestUnixInstallationObserver_ServiceConnectionFailureIsOnlyNotReady(t *testing.T) {
	connectionErr := errors.New("daemon unavailable")
	serviceObserver := &fakeUnixInstallationService{definition: service.Definition{Status: service.StatusRunning, Running: true, Enabled: true, Env: []string{"MIHARI_CONTROL_ENDPOINT=/run/mihari.sock", "MIHARI_CONTROL_CREDENTIAL=/run/mihari.token"}}}
	readyCalls := 0
	observer := newUnixInstallationObserver(unixInstallationObserverDeps{
		service: serviceObserver,
		ready: func(_ context.Context, endpoint, credential string) (bool, error) {
			readyCalls++
			if endpoint != "/run/mihari.sock" || credential != "/run/mihari.token" {
				t.Fatalf("ready scope=%q %q", endpoint, credential)
			}
			return false, connectionErr
		},
		resources: &fakeUnixInstallationResources{},
	})
	got, err := observer.ObserveService(context.Background())
	if err != nil || got.State != InstallServiceRunning || !got.Enabled || got.Ready || readyCalls != 1 {
		t.Fatalf("service=%+v readyCalls=%d error=%v", got, readyCalls, err)
	}
}

func TestUnixInstallationObserver_ServiceDoesNotProbeReadyWhenStopped(t *testing.T) {
	serviceObserver := &fakeUnixInstallationService{definition: service.Definition{Status: service.StatusStopped}}
	observer := newUnixInstallationObserver(unixInstallationObserverDeps{
		service: serviceObserver,
		ready: func(context.Context, string, string) (bool, error) {
			t.Fatal("Ready called for stopped service")
			return false, nil
		},
		resources: &fakeUnixInstallationResources{},
	})
	got, err := observer.ObserveService(context.Background())
	if err != nil || got.State != InstallServiceStopped {
		t.Fatalf("service=%+v error=%v", got, err)
	}
}

func TestUnixInstallationObserver_NoKWithServiceRegistrationRemainsUnknown(t *testing.T) {
	resources := &fakeUnixInstallationResources{}
	observer := newUnixInstallationObserver(unixInstallationObserverDeps{
		openControl: func(context.Context) (unixInstallationControl, error) { return nil, os.ErrNotExist },
		service:     &fakeUnixInstallationService{definition: service.Definition{Status: service.StatusStopped}},
		resources:   resources,
	})
	manager := NewInstallationManager(InstallationManagerOptions{Observer: observer})
	status, err := manager.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Kind != InstallationKindUnknown || status.ServiceState != InstallServiceStopped || status.Reason != InstallationReasonRecordInvalid {
		t.Fatalf("status=%+v", status)
	}
	if resources.legacyCalls != 2 {
		t.Fatalf("legacy calls=%d want=2", resources.legacyCalls)
	}
}

func TestUnixInstallationObserver_ManagerClassifiesCompleteAndHeldApplyingRecords(t *testing.T) {
	complete := mustInstallationStateJSON(t, testInstallationState(InstallationStateComplete))
	applying := mustInstallationStateJSON(t, testInstallationState(InstallationStateApplying))
	for _, tc := range []struct {
		name     string
		raw      []byte
		locked   bool
		wantKind string
	}{
		{name: "complete", raw: complete, wantKind: InstallationKindInstalled},
		{name: "held applying", raw: applying, locked: true, wantKind: InstallationKindInProgress},
		{name: "malformed", raw: []byte("{"), wantKind: InstallationKindUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resources := &fakeUnixInstallationResources{matches: true}
			observer := newUnixInstallationObserver(unixInstallationObserverDeps{
				openControl: func(context.Context) (unixInstallationControl, error) {
					return &fakeUnixInstallationControl{state: tc.raw, locked: tc.locked}, nil
				},
				service:   &fakeUnixInstallationService{definition: service.Definition{Status: service.StatusStopped}},
				resources: resources,
			})
			status, err := NewInstallationManager(InstallationManagerOptions{Observer: observer}).Inspect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if status.Kind != tc.wantKind {
				t.Fatalf("status=%+v want kind=%s", status, tc.wantKind)
			}
		})
	}
}

func TestUnixInstallationDefinitionDigest_BindsStaticDefinitionOnly(t *testing.T) {
	defaults := platform.LayoutDefaults{OS: "linux", BaseDir: "/var/lib/mihari", InstallRoot: "/usr/local/lib/mihari", SocketLimit: 100}
	layout, err := platform.ResolveLayout(platform.LayoutInput{
		CWD: "/", Data: "/srv/mihari", InstallRoot: "/opt/mihari", EUID: 0,
	}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := service.BuildUnixDefinition(layout, "linux")
	if err != nil {
		t.Fatal(err)
	}
	want, err := unixInstallationDefinitionSHA256(definition)
	if err != nil {
		t.Fatal(err)
	}
	dynamic := definition
	dynamic.Status = service.StatusRunning
	dynamic.Running = true
	dynamic.Enabled = false
	dynamic.Masked = true
	dynamic.Process = service.ProcessIdentity{PID: 42, BootID: "boot-b", Group: "/mihari"}
	dynamic.Links = []service.DefinitionLink{{Path: "/dynamic/enabled", Target: definition.Files[0].Path}}
	dynamic.Files = append([]service.DefinitionFile(nil), definition.Files...)
	dynamic.Files[0].Identity = "replacement-inode"
	dynamic.Env = append([]string(nil), definition.Env...)
	for left, right := 0, len(dynamic.Env)-1; left < right; left, right = left+1, right-1 {
		dynamic.Env[left], dynamic.Env[right] = dynamic.Env[right], dynamic.Env[left]
	}
	got, err := unixInstallationDefinitionSHA256(dynamic)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("dynamic observation changed digest: got=%s want=%s", got, want)
	}
	dynamic.Args = append(append([]string(nil), definition.Args...), "--unexpected")
	changed, err := unixInstallationDefinitionSHA256(dynamic)
	if err != nil {
		t.Fatal(err)
	}
	if changed == want {
		t.Fatal("static argument did not change definition digest")
	}
	endpoint, credential, ok := verifiedUnixDefinitionControlScope(definition, "linux", defaults)
	if !ok || endpoint != layout.ControlEndpoint || credential != layout.CredentialPath {
		t.Fatalf("verified scope=%q %q ok=%v", endpoint, credential, ok)
	}
	tampered := cloneUnixInstallationDefinition(definition)
	tampered.Files[0].Bytes = append(tampered.Files[0].Bytes, '\n')
	if _, _, ok := verifiedUnixDefinitionControlScope(tampered, "linux", defaults); ok {
		t.Fatal("tampered service definition authorized a readiness scope")
	}
}

func TestUnixInstallationResources_VerifiesProtectedManifestEvidence(t *testing.T) {
	fixture := newUnixInstallationResourceFixture(t)
	resource := nativeUnixInstallationResources{goos: runtime.GOOS, defaults: fixture.defaults, boot: func() (string, error) { return fixture.bootID, nil }, owner: uint32(os.Geteuid())}
	actual := fixture.definition
	actual.Status = service.StatusRunning
	actual.Running = true
	actual.Process = service.ProcessIdentity{PID: 42, BootID: fixture.bootID}

	matches, err := resource.Verify(context.Background(), fixture.manifest, actual)
	if err != nil || !matches {
		t.Fatalf("verified resources matches=%v error=%v", matches, err)
	}

	tests := []struct {
		name   string
		mutate func(*InstallationManifest, *service.Definition)
	}{
		{name: "binary hash", mutate: func(m *InstallationManifest, _ *service.Definition) { m.Binary.SHA256 = strings.Repeat("f", 64) }},
		{name: "data identity on same boot", mutate: func(m *InstallationManifest, _ *service.Definition) { m.DataIdentity.Key = "replaced-data-root" }},
		{name: "data marker", mutate: func(m *InstallationManifest, _ *service.Definition) { m.DataIdentity.Marker = strings.Repeat("e", 64) }},
		{name: "definition digest", mutate: func(m *InstallationManifest, _ *service.Definition) { m.DefinitionSHA256 = strings.Repeat("d", 64) }},
		{name: "endpoint scope", mutate: func(m *InstallationManifest, _ *service.Definition) {
			m.Endpoint = filepath.Join(m.DataRoot, "other.sock")
		}},
		{name: "credential scope", mutate: func(m *InstallationManifest, _ *service.Definition) {
			m.Credential = filepath.Join(m.DataRoot, "other.token")
		}},
		{name: "install scope", mutate: func(m *InstallationManifest, _ *service.Definition) {
			m.InstallRoot += "-other"
			m.Binary.Path = filepath.Join(m.InstallRoot, "mihari")
		}},
		{name: "definition bytes", mutate: func(_ *InstallationManifest, d *service.Definition) {
			d.Files[0].Bytes = append(d.Files[0].Bytes, '\n')
		}},
		{name: "definition environment", mutate: func(_ *InstallationManifest, d *service.Definition) { d.Env = append(d.Env, "UNEXPECTED=value") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			manifest := cloneInstallationManifest(fixture.manifest)
			definition := cloneUnixInstallationDefinition(actual)
			tc.mutate(&manifest, &definition)
			matches, err := resource.Verify(context.Background(), manifest, definition)
			if err != nil || matches {
				t.Fatalf("mismatch matches=%v error=%v", matches, err)
			}
		})
	}

	crossBoot := cloneInstallationManifest(fixture.manifest)
	crossBoot.DataIdentity.BootID = "earlier-boot"
	crossBoot.DataIdentity.Key = "earlier-device-and-inode"
	matches, err = resource.Verify(context.Background(), crossBoot, actual)
	if err != nil || !matches {
		t.Fatalf("cross-boot marker evidence matches=%v error=%v", matches, err)
	}
}

func TestUnixInstallationResources_MissingOrInvalidCredentialIsReadOnlyMismatch(t *testing.T) {
	fixture := newUnixInstallationResourceFixture(t)
	resource := nativeUnixInstallationResources{goos: runtime.GOOS, defaults: fixture.defaults, boot: func() (string, error) { return fixture.bootID, nil }, owner: uint32(os.Geteuid())}
	credentialPath := fixture.manifest.Credential
	if err := os.Remove(credentialPath); err != nil {
		t.Fatal(err)
	}
	matches, err := resource.Verify(context.Background(), fixture.manifest, fixture.definition)
	if err != nil || matches {
		t.Fatalf("missing credential matches=%v error=%v", matches, err)
	}
	if _, err := os.Lstat(credentialPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only verification recreated credential: %v", err)
	}
	if err := os.WriteFile(credentialPath, []byte(strings.Repeat("z", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	matches, err = resource.Verify(context.Background(), fixture.manifest, fixture.definition)
	if err != nil || matches {
		t.Fatalf("invalid credential matches=%v error=%v", matches, err)
	}
}

type unixInstallationResourceFixture struct {
	defaults   platform.LayoutDefaults
	bootID     string
	manifest   InstallationManifest
	definition service.Definition
}

func newUnixInstallationResourceFixture(t *testing.T) unixInstallationResourceFixture {
	t.Helper()
	root := unixInstallationObserverTrustedTempDir(t)
	dataRoot := filepath.Join(root, "data")
	installRoot := filepath.Join(root, "program")
	for _, item := range []struct {
		path string
		mode os.FileMode
	}{{dataRoot, 0700}, {filepath.Join(dataRoot, "locks"), 0700}, {installRoot, 0755}} {
		if err := os.Mkdir(item.path, item.mode); err != nil {
			t.Fatal(err)
		}
	}
	binaryRaw := []byte("fixture executable")
	binaryPath := filepath.Join(installRoot, "mihari")
	if err := os.WriteFile(binaryPath, binaryRaw, 0755); err != nil {
		t.Fatal(err)
	}
	markerRaw := []byte("0123456789abcdef0123456789abcdef")
	if err := os.WriteFile(filepath.Join(dataRoot, "locks", "install-data-id"), markerRaw, 0600); err != nil {
		t.Fatal(err)
	}
	credentialPath := filepath.Join(dataRoot, "control.token")
	if err := os.WriteFile(credentialPath, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	defaults := platform.SystemLayoutDefaults()
	layout, err := platform.ResolveLayout(platform.LayoutInput{
		CWD: "/", Data: dataRoot, Endpoint: "/run/mihari-observer-test.sock", InstallRoot: installRoot, EUID: 0,
	}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := service.BuildUnixDefinition(layout, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	definitionSHA256, err := unixInstallationDefinitionSHA256(definition)
	if err != nil {
		t.Fatal(err)
	}
	data, err := platform.OpenTrustedRoot(context.Background(), dataRoot, platform.RootPolicy{Owner: uint32(os.Geteuid()), Mode: 0700})
	if err != nil {
		t.Fatal(err)
	}
	_, dataIdentity, _, _, snapshotErr := data.Snapshot(context.Background())
	if err := errors.Join(snapshotErr, data.Close()); err != nil {
		t.Fatal(err)
	}
	bootID := "fixture-boot"
	return unixInstallationResourceFixture{
		defaults: defaults,
		bootID:   bootID,
		manifest: InstallationManifest{
			Installed: true, DataRoot: dataRoot, InstallRoot: installRoot, Endpoint: layout.ControlEndpoint, Credential: credentialPath,
			DataIdentity:     &InstallationIdentity{BootID: bootID, Key: dataIdentity, Marker: sha256HexBytes(markerRaw)},
			Binary:           &InstallationBinary{Path: binaryPath, SHA256: sha256HexBytes(binaryRaw), Version: "fixture-v1"},
			DefinitionSHA256: definitionSHA256, Enabled: true, RunAfterInstall: true,
		},
		definition: definition,
	}
}

func unixInstallationObserverTrustedTempDir(t *testing.T) string {
	t.Helper()
	base := os.Getenv("MIHARI_INSTALL_OBSERVER_TEST_ROOT")
	if base == "" {
		return migrationTrustedTempDir(t)
	}
	root, err := os.MkdirTemp(base, "mihari-observer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	root, err = filepath.Abs(root)
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func cloneUnixInstallationDefinition(definition service.Definition) service.Definition {
	clone := definition
	clone.Args = append([]string(nil), definition.Args...)
	clone.Env = append([]string(nil), definition.Env...)
	clone.Files = append([]service.DefinitionFile(nil), definition.Files...)
	for i := range clone.Files {
		clone.Files[i].Bytes = append([]byte(nil), definition.Files[i].Bytes...)
	}
	clone.Links = append([]service.DefinitionLink(nil), definition.Links...)
	return clone
}
