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

	"github.com/LeeShunEE/mihari/internal/platform"
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
