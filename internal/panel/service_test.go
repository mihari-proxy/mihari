package panel

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

type fixtureAdapter struct {
	id      string
	name    string
	build   string
	asset   string
	resolve func(ctx context.Context) (string, string, error)
}

func (a fixtureAdapter) ID() string          { return a.id }
func (a fixtureAdapter) DisplayName() string { return a.name }
func (a fixtureAdapter) SetupPath(string) string {
	return "/?hostname=127.0.0.1:9191"
}
func (a fixtureAdapter) ResolveLatest(ctx context.Context) (string, string, error) {
	if a.resolve != nil {
		return a.resolve(ctx)
	}
	return a.build, a.asset, nil
}

func TestServiceInstallActivateRollback(t *testing.T) {
	paths, server, zipV1, zipV2 := panelFixture(t)
	defer server.Close()

	service, err := Open(ServiceOptions{
		WebRoot: paths.WebRoot, WebActive: paths.WebActive, StagingDir: paths.PanelStaging,
		HTTPClient: server.Client(), AllowHTTP: true,
		Adapters: []Adapter{
			fixtureAdapter{id: IDZashboard, name: "Zashboard", build: "v1.0.0", asset: server.URL + "/v1.zip"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Serve successive builds by swapping resolve after first install.
	_ = zipV1
	_ = zipV2

	if err := service.Install(context.Background(), IDZashboard, ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(context.Background(), IDZashboard); err != nil {
		t.Fatal(err)
	}
	active, err := service.Active()
	if err != nil || active.Panel != IDZashboard || active.Build != "v1.0.0" {
		t.Fatalf("active=%#v err=%v", active, err)
	}
	if _, err := os.Stat(filepath.Join(paths.WebRoot, IDZashboard, "v1.0.0", "index.html")); err != nil {
		t.Fatal(err)
	}

	// Update to v2 via adapter resolve change.
	service.adapters[IDZashboard] = fixtureAdapter{
		id: IDZashboard, name: "Zashboard", build: "v2.0.0", asset: server.URL + "/v2.zip",
	}
	if err := service.Update(context.Background(), IDZashboard); err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(context.Background(), IDZashboard); err != nil {
		t.Fatal(err)
	}
	active, err = service.Active()
	if err != nil || active.Build != "v2.0.0" {
		t.Fatalf("active=%#v err=%v", active, err)
	}
	if active.Previous[IDZashboard] != "v1.0.0" {
		t.Fatalf("previous=%#v", active.Previous)
	}

	if err := service.Rollback(context.Background(), IDZashboard); err != nil {
		t.Fatal(err)
	}
	active, err = service.Active()
	if err != nil || active.Build != "v1.0.0" {
		t.Fatalf("after rollback active=%#v err=%v", active, err)
	}
}

func TestServiceUninstallRemovesBuildsAndClearsDefault(t *testing.T) {
	paths, server, _, _ := panelFixture(t)
	defer server.Close()
	service, err := Open(ServiceOptions{
		WebRoot: paths.WebRoot, WebActive: paths.WebActive, StagingDir: paths.PanelStaging,
		HTTPClient: server.Client(), AllowHTTP: true,
		Adapters: []Adapter{
			fixtureAdapter{id: IDZashboard, name: "Zashboard", build: "v1.0.0", asset: server.URL + "/v1.zip"},
			fixtureAdapter{id: IDMetaCubeXD, name: "MetaCubeXD", build: "abc123", asset: server.URL + "/v1.zip"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Install(context.Background(), IDZashboard, ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Install(context.Background(), IDMetaCubeXD, ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(context.Background(), IDZashboard); err != nil {
		t.Fatal(err)
	}
	if err := service.Uninstall(context.Background(), IDZashboard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(paths.WebRoot, IDZashboard)); !os.IsNotExist(err) {
		t.Fatalf("zashboard tree should be removed, err=%v", err)
	}
	active, err := service.Active()
	if err != nil || active.Panel != "" || active.Build != "" {
		t.Fatalf("default should be cleared after uninstalling active panel: %#v err=%v", active, err)
	}
	// Other panel remains installed and openable by path.
	dir, err := service.PanelDir(IDMetaCubeXD)
	if err != nil || dir == "" {
		t.Fatalf("metacubexd dir=%q err=%v", dir, err)
	}
	infos := service.List()
	var z, m PanelInfo
	for _, info := range infos {
		switch info.ID {
		case IDZashboard:
			z = info
		case IDMetaCubeXD:
			m = info
		}
	}
	if z.Health != "missing" || z.InstalledBuild != "" || z.Active {
		t.Fatalf("zashboard after uninstall=%#v", z)
	}
	if m.Health != "installed" || m.InstalledBuild == "" {
		t.Fatalf("metacubexd after peer uninstall=%#v", m)
	}
}

func TestServiceReinstallReplacesTreeAndRestoresDefault(t *testing.T) {
	paths, server, _, _ := panelFixture(t)
	defer server.Close()
	service, err := Open(ServiceOptions{
		WebRoot: paths.WebRoot, WebActive: paths.WebActive, StagingDir: paths.PanelStaging,
		HTTPClient: server.Client(), AllowHTTP: true,
		Adapters: []Adapter{
			fixtureAdapter{id: IDZashboard, name: "Zashboard", build: "v1.0.0", asset: server.URL + "/v1.zip"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Install(context.Background(), IDZashboard, ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(context.Background(), IDZashboard); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(paths.WebRoot, IDZashboard, "v1.0.0", "stale.txt")
	if err := os.WriteFile(marker, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Same build id: reinstall must still replace the tree (force reinstall).
	if err := service.Reinstall(context.Background(), IDZashboard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("stale file should be gone after reinstall, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.WebRoot, IDZashboard, "v1.0.0", "index.html")); err != nil {
		t.Fatal(err)
	}
	active, err := service.Active()
	if err != nil || active.Panel != IDZashboard || active.Build != "v1.0.0" {
		t.Fatalf("default should be restored after reinstall: %#v err=%v", active, err)
	}
}

func TestServicePrepareUpdateDoesNotPromoteUntilCommit(t *testing.T) {
	paths, server, _, _ := panelFixture(t)
	defer server.Close()
	service, err := Open(ServiceOptions{
		WebRoot: paths.WebRoot, WebActive: paths.WebActive, StagingDir: paths.PanelStaging,
		HTTPClient: server.Client(), AllowHTTP: true,
		Adapters: []Adapter{
			fixtureAdapter{id: IDZashboard, name: "Zashboard", build: "v2.0.0", asset: server.URL + "/v2.zip"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	prepared, err := service.PrepareUpdate(context.Background(), IDZashboard)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Cleanup()
	if !prepared.Valid() {
		t.Fatal("prepared update candidate is not valid")
	}
	buildDir := filepath.Join(paths.WebRoot, IDZashboard, "v2.0.0")
	if _, err := os.Stat(buildDir); !os.IsNotExist(err) {
		t.Fatalf("prepared update promoted before commit: %v", err)
	}
	if err := prepared.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(buildDir, "index.html")); err != nil {
		t.Fatal(err)
	}
}

func TestServicePrepareInstallDoesNotPromoteUntilCommit(t *testing.T) {
	paths, server, _, _ := panelFixture(t)
	defer server.Close()
	service, err := Open(ServiceOptions{
		WebRoot: paths.WebRoot, WebActive: paths.WebActive, StagingDir: paths.PanelStaging,
		HTTPClient: server.Client(), AllowHTTP: true,
		Adapters: []Adapter{
			fixtureAdapter{id: IDZashboard, name: "Zashboard", build: "v1.0.0", asset: server.URL + "/v1.zip"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	prepared, err := service.PrepareInstall(context.Background(), IDZashboard, "")
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Cleanup()
	if !prepared.Valid() {
		t.Fatal("prepared install candidate is not valid")
	}
	buildDir := filepath.Join(paths.WebRoot, IDZashboard, "v1.0.0")
	if _, err := os.Stat(buildDir); !os.IsNotExist(err) {
		t.Fatalf("prepared install promoted before commit: %v", err)
	}
	if err := prepared.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(buildDir, "index.html")); err != nil {
		t.Fatal(err)
	}
}

func TestServicePreparedInstallNoOpsWhenSameBuildBecomesReadyBeforeCommit(t *testing.T) {
	paths, server, _, _ := panelFixture(t)
	defer server.Close()
	service, err := Open(ServiceOptions{
		WebRoot: paths.WebRoot, WebActive: paths.WebActive, StagingDir: paths.PanelStaging,
		HTTPClient: server.Client(), AllowHTTP: true,
		Adapters: []Adapter{
			fixtureAdapter{id: IDZashboard, name: "Zashboard", build: "v1.0.0", asset: server.URL + "/v1.zip"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := service.PrepareInstall(context.Background(), IDZashboard, "")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := service.PrepareInstall(context.Background(), IDZashboard, "")
	if err != nil {
		t.Fatal(err)
	}

	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(paths.WebRoot, IDZashboard, "v1.0.0", "local-marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := duplicate.Commit(); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "keep" {
		t.Fatalf("duplicate install replaced ready build: content=%q err=%v", content, err)
	}
	first.Cleanup()
	duplicate.Cleanup()
	entries, err := os.ReadDir(paths.PanelStaging)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging entries after duplicate install cleanup=%v", entries)
	}
}

func TestServicePreparedUpdateNoOpsWhenSameBuildBecomesReadyBeforeCommit(t *testing.T) {
	paths, server, _, _ := panelFixture(t)
	t.Cleanup(server.Close)
	service, err := Open(ServiceOptions{
		WebRoot: paths.WebRoot, WebActive: paths.WebActive, StagingDir: paths.PanelStaging,
		HTTPClient: server.Client(), AllowHTTP: true,
		Adapters: []Adapter{
			fixtureAdapter{id: IDZashboard, name: "Zashboard", build: "v2.0.0", asset: server.URL + "/v2.zip"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := service.PrepareUpdate(context.Background(), IDZashboard)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := service.PrepareUpdate(context.Background(), IDZashboard)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(paths.WebRoot, IDZashboard, "v2.0.0", "local-marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := duplicate.Commit(); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "keep" {
		t.Fatalf("duplicate update replaced ready build: content=%q err=%v", content, err)
	}
	first.Cleanup()
	duplicate.Cleanup()
	entries, err := os.ReadDir(paths.PanelStaging)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging entries after duplicate update cleanup=%v", entries)
	}
}

func TestServicePreparedUpdateCleanupRemovesStagingCandidate(t *testing.T) {
	paths, server, _, _ := panelFixture(t)
	defer server.Close()
	service, err := Open(ServiceOptions{
		WebRoot: paths.WebRoot, WebActive: paths.WebActive, StagingDir: paths.PanelStaging,
		HTTPClient: server.Client(), AllowHTTP: true,
		Adapters: []Adapter{
			fixtureAdapter{id: IDZashboard, name: "Zashboard", build: "v2.0.0", asset: server.URL + "/v2.zip"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := service.PrepareUpdate(context.Background(), IDZashboard)
	if err != nil {
		t.Fatal(err)
	}
	prepared.Cleanup()
	entries, err := os.ReadDir(paths.PanelStaging)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging entries after cleanup=%v", entries)
	}
	if _, err := os.Stat(filepath.Join(paths.WebRoot, IDZashboard, "v2.0.0")); !os.IsNotExist(err) {
		t.Fatalf("cleaned candidate promoted into web root: %v", err)
	}
}

func TestServicePrepareReinstallDoesNotMutateUntilCommit(t *testing.T) {
	paths, server, _, _ := panelFixture(t)
	defer server.Close()
	service, err := Open(ServiceOptions{
		WebRoot: paths.WebRoot, WebActive: paths.WebActive, StagingDir: paths.PanelStaging,
		HTTPClient: server.Client(), AllowHTTP: true,
		Adapters: []Adapter{
			fixtureAdapter{id: IDZashboard, name: "Zashboard", build: "v1.0.0", asset: server.URL + "/v1.zip"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Install(context.Background(), IDZashboard, ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(context.Background(), IDZashboard); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(paths.WebRoot, IDZashboard, "v1.0.0", "stale.txt")
	if err := os.WriteFile(marker, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	prepared, err := service.PrepareReinstall(context.Background(), IDZashboard)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Cleanup()
	if !prepared.Valid() {
		t.Fatal("prepared reinstall candidate is not valid")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("reinstall mutated existing tree during prepare: %v", err)
	}
	active, err := service.Active()
	if err != nil || active.Panel != IDZashboard || active.Build != "v1.0.0" {
		t.Fatalf("active changed during prepare: %#v err=%v", active, err)
	}

	if err := prepared.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("stale marker survived reinstall commit: %v", err)
	}
	active, err = service.Active()
	if err != nil || active.Panel != IDZashboard || active.Build != "v1.0.0" {
		t.Fatalf("active not restored after reinstall commit: %#v err=%v", active, err)
	}
	prepared.Cleanup()
	entries, err := os.ReadDir(paths.PanelStaging)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging entries after reinstall cleanup=%v", entries)
	}
}

func TestServiceUninstallUnknownPanel(t *testing.T) {
	paths, server, _, _ := panelFixture(t)
	defer server.Close()
	service, err := Open(ServiceOptions{
		WebRoot: paths.WebRoot, WebActive: paths.WebActive, StagingDir: paths.PanelStaging,
		HTTPClient: server.Client(), AllowHTTP: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Uninstall(context.Background(), "nope"); err == nil {
		t.Fatal("expected unknown panel rejection")
	}
}

func TestServiceRefuseDeleteActiveBuild(t *testing.T) {
	paths, server, _, _ := panelFixture(t)
	defer server.Close()
	service, err := Open(ServiceOptions{
		WebRoot: paths.WebRoot, WebActive: paths.WebActive, StagingDir: paths.PanelStaging,
		HTTPClient: server.Client(), AllowHTTP: true,
		Adapters: []Adapter{
			fixtureAdapter{id: IDZashboard, name: "Zashboard", build: "v1.0.0", asset: server.URL + "/v1.zip"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Install(context.Background(), IDZashboard, ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(context.Background(), IDZashboard); err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveBuild(IDZashboard, "v1.0.0"); err == nil {
		t.Fatal("expected active build removal to be refused")
	}
	if _, err := os.Stat(filepath.Join(paths.WebRoot, IDZashboard, "v1.0.0")); err != nil {
		t.Fatal("active build was removed")
	}
}

func TestServiceConcurrentInstallDoesNotActivateIncomplete(t *testing.T) {
	paths, server, _, _ := panelFixture(t)
	defer server.Close()

	gate := make(chan struct{})
	service, err := Open(ServiceOptions{
		WebRoot: paths.WebRoot, WebActive: paths.WebActive, StagingDir: paths.PanelStaging,
		HTTPClient: server.Client(), AllowHTTP: true,
		Adapters: []Adapter{
			fixtureAdapter{
				id: IDZashboard, name: "Zashboard",
				resolve: func(ctx context.Context) (string, string, error) {
					select {
					case <-gate:
					case <-ctx.Done():
						return "", "", ctx.Err()
					case <-time.After(2 * time.Second):
						return "", "", context.DeadlineExceeded
					}
					return "v1.0.0", server.URL + "/v1.zip", nil
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	errCh := make(chan error, 1)
	go func() {
		defer wg.Done()
		errCh <- service.Install(context.Background(), IDZashboard, "")
	}()

	// While install is blocked on resolve, activate must not succeed (nothing installed).
	if err := service.Activate(context.Background(), IDZashboard); err == nil {
		t.Fatal("activate must fail before install completes")
	}
	active, err := service.Active()
	if err != nil || active.Panel != "" {
		t.Fatalf("active leaked incomplete install: %#v err=%v", active, err)
	}

	close(gate)
	wg.Wait()
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(context.Background(), IDZashboard); err != nil {
		t.Fatal(err)
	}
}

func TestServiceListReportsInstalledAndActive(t *testing.T) {
	paths, server, _, _ := panelFixture(t)
	defer server.Close()
	service, err := Open(ServiceOptions{
		WebRoot: paths.WebRoot, WebActive: paths.WebActive, StagingDir: paths.PanelStaging,
		HTTPClient: server.Client(), AllowHTTP: true,
		Adapters: []Adapter{
			fixtureAdapter{id: IDZashboard, name: "Zashboard", build: "v1.0.0", asset: server.URL + "/v1.zip"},
			fixtureAdapter{id: IDMetaCubeXD, name: "MetaCubeXD", build: "abc123", asset: server.URL + "/v1.zip"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Install(context.Background(), IDZashboard, ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(context.Background(), IDZashboard); err != nil {
		t.Fatal(err)
	}
	statuses := service.List()
	if len(statuses) != 2 {
		t.Fatalf("len=%d", len(statuses))
	}
	var z PanelInfo
	for _, s := range statuses {
		if s.ID == IDZashboard {
			z = s
		}
	}
	if !z.Active || z.InstalledBuild != "v1.0.0" || z.Name != "Zashboard" {
		t.Fatalf("status=%#v", z)
	}
}

func panelFixture(t *testing.T) (platform.Paths, *httptest.Server, []byte, []byte) {
	t.Helper()
	root := t.TempDir()
	paths := platform.NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	zipV1 := mustZip(t, map[string]string{"index.html": "<html>v1</html>"})
	zipV2 := mustZip(t, map[string]string{"index.html": "<html>v2</html>"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.zip":
			_, _ = w.Write(zipV1)
		case "/v2.zip":
			_, _ = w.Write(zipV2)
		default:
			http.NotFound(w, r)
		}
	}))
	return paths, server, zipV1, zipV2
}

func mustZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
