package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mihari-proxy/mihari/internal/app"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/update"
)

type selfUpdateServiceController struct {
	status service.StatusKind
	stops  int
	starts int
}

func (*selfUpdateServiceController) Install() error   { return nil }
func (*selfUpdateServiceController) Uninstall() error { return nil }
func (c *selfUpdateServiceController) Start() error {
	c.starts++
	return nil
}
func (c *selfUpdateServiceController) Stop() error {
	c.stops++
	return nil
}
func (*selfUpdateServiceController) Restart() error { return nil }
func (c *selfUpdateServiceController) Status() (service.StatusKind, error) {
	return c.status, nil
}
func (*selfUpdateServiceController) Run() error { return nil }

func TestSelfUpdateSynchronizesDifferentServiceBinaryAndVerifiesDaemonVersion(t *testing.T) {
	installRoot := t.TempDir()
	t.Setenv("MIHARI_INSTALL_ROOT", installRoot)
	tuiBinary := filepath.Join(t.TempDir(), platform.InstalledBinaryName())
	if err := os.WriteFile(tuiBinary, []byte("old-tui"), 0o755); err != nil {
		t.Fatal(err)
	}
	serviceBinary, err := platform.AbsoluteInstalledBinaryPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(serviceBinary, []byte("old-service"), 0o755); err != nil {
		t.Fatal(err)
	}

	controller := &selfUpdateServiceController{status: service.StatusRunning}
	serviceManager := service.New(service.Options{
		Executable: tuiBinary,
		NewController: func(service.RunFunc, string, []string) (service.Controller, error) {
			return controller, nil
		},
	})
	statusRequests := 0
	statusServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" {
			http.NotFound(w, r)
			return
		}
		statusRequests++
		_ = json.NewEncoder(w).Encode(protocol.Status{DaemonVersion: "v9.9.9"})
	}))
	defer statusServer.Close()
	completion := app.NewSelfUpdateServiceCompletion(
		serviceManager,
		controlclient.NewHTTP(statusServer.URL, "test-token", statusServer.Client()),
	)

	payload := []byte("new-mihari")
	assetName := "mihari-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		assetName += ".exe"
	}
	var releaseServer *httptest.Server
	releaseServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/mihari-proxy/mihari/releases/latest":
			_ = json.NewEncoder(w).Encode(update.Release{
				TagName: "v9.9.9",
				Assets:  []update.Asset{{Name: assetName, URL: releaseServer.URL + "/asset", Size: int64(len(payload))}},
			})
		case "/asset":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer releaseServer.Close()

	updater := update.SelfUpdater{
		HTTPClient:   releaseServer.Client(),
		APIBase:      releaseServer.URL,
		GOOS:         runtime.GOOS,
		GOARCH:       runtime.GOARCH,
		AfterReplace: completion.AfterReplace,
	}
	result, err := updater.Update(context.Background(), tuiBinary, "v1.0.0")
	if err != nil || !result.Updated || result.Version != "v9.9.9" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	for name, path := range map[string]string{"TUI": tuiBinary, "service": serviceBinary} {
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s binary: %v", name, readErr)
		}
		if string(got) != string(payload) {
			t.Fatalf("%s binary=%q want=%q", name, got, payload)
		}
	}
	if controller.stops != 1 || controller.starts != 1 || statusRequests != 1 {
		t.Fatalf("stops=%d starts=%d status requests=%d", controller.stops, controller.starts, statusRequests)
	}
}
