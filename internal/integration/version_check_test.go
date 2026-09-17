package integration

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/server"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/panel"
	"github.com/mihari-proxy/mihari/internal/panel/metacubexd"
	"github.com/mihari-proxy/mihari/internal/panel/zashboard"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

func TestVersionChecks_RealAdaptersThroughControlClientWithoutInstallation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body string
		switch r.URL.Path {
		case "/repos/MetaCubeX/mihomo/releases/latest":
			body = `{"tag_name":"v1.20.0","assets":[{"name":"mihomo-linux-amd64-v1.20.0.gz"}]}`
		case "/repos/Zephyruso/zashboard/releases/latest":
			body = `{"tag_name":"v2.0.0","assets":[{"name":"dist.zip","browser_download_url":"https://example.invalid/dist.zip"}]}`
		case "/repos/MetaCubeX/metacubexd/branches/gh-pages":
			body = `{"commit":{"sha":"abc123456789ffff"}}`
		default:
			t.Errorf("unexpected request/download: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Error(err)
		}
	}))
	defer upstream.Close()
	root := t.TempDir()
	staging := filepath.Join(root, "staging")
	panels, err := panel.Open(panel.ServiceOptions{WebRoot: filepath.Join(root, "web"), WebActive: filepath.Join(root, "active.json"), StagingDir: staging, Adapters: []panel.Adapter{zashboard.New(upstream.Client(), upstream.URL), metacubexd.New(upstream.Client(), upstream.URL)}})
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(state.Snapshot{Revision: 17})
	manager := runtimeapi.New(runtimeapi.Options{Store: store, Installer: core.Installer{HTTPClient: upstream.Client(), APIBase: upstream.URL, GOOS: "linux", GOARCH: "amd64"}, Panels: panels})
	local := httptest.NewServer(server.New(server.Options{Token: "fixture-token", Store: store, Runtime: manager}).Handler())
	defer local.Close()
	client := controlclient.NewHTTP(local.URL, "fixture-token", local.Client())
	result, err := client.CheckCoreVersion(t.Context())
	if err != nil || result.Latest != "v1.20.0" || result.Channel != "stable" {
		t.Fatalf("core=%+v err=%v", result, err)
	}
	for id, want := range map[string]string{panel.IDZashboard: "v2.0.0", panel.IDMetaCubeXD: "abc123456789"} {
		result, err := client.CheckPanelVersion(t.Context(), id)
		if err != nil || result.Latest != want {
			t.Fatalf("panel=%s result=%+v err=%v", id, result, err)
		}
	}
	for _, info := range panels.List() {
		if info.InstalledBuild != "" || info.Active {
			t.Fatalf("check installed panel: %+v", info)
		}
	}
	entries, err := os.ReadDir(staging)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging=%v err=%v", entries, err)
	}
	if store.Load().Revision != 17 {
		t.Fatal("check changed business revision")
	}
}
