package integration

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/config"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	transporttest "github.com/mihari-proxy/mihari/internal/control/transport/testutil"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/mihomo"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestUnifiedLogging_ControlWebExternalSilentAndRestart(t *testing.T) {
	t.Setenv("MIHARI_FAKE_MIHOMO", "1")
	t.Setenv("MIHARI_FAKE_LOG_STREAM_KEEP_OPEN", "1")
	paths := platform.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	copyExecutable(t, paths.CoreBinary)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fs.Close(); err != nil {
			t.Error(err)
		}
	})
	fileLogger, err := logging.Open(t.Context(), logging.RuntimeOptions{BasePath: paths.DaemonLog, Component: "daemon", PrivateFS: fs, Config: logging.DefaultConfig()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fileLogger.Close(); err != nil {
			t.Error(err)
		}
	})
	group := logging.NewGroup(paths.LogDir, logging.DefaultConfig(), fileLogger)
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("ab", 32)
	addresses := reserveLoopbackAddresses(t, 3)
	settings.ControllerAddr, settings.MixedAddr, settings.WebAddr = addresses[0], addresses[1], addresses[2]
	assembly, err := app.BuildRuntimeWithOptions(paths, settings, "logging-test", io.Discard, io.Discard, app.RuntimeBuildOptions{Logging: group})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	endpoint, ready, done := transporttest.Endpoint(t), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, daemon.Options{Endpoint: endpoint, Token: "fixture-control", Ready: ready, Store: assembly.Store, Runtime: assembly.Manager})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("daemon did not join")
		}
	})
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon stopped: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("IPC not ready")
	}
	client := controlclient.New(endpoint, "fixture-control")
	running := waitForCore(t, client, done, func(s protocol.CoreStatus) bool { return s.Status == "running" })
	level := "debug"
	status, err := client.UpdateLogging(ctx, protocol.LoggingUpdateRequest{OperationID: "ipc-debug", Level: &level})
	if err != nil || status.Level != level || status.CoreLevel != level || status.SyncState != "applied" {
		t.Fatalf("logging=%+v err=%v", status, err)
	}
	fileLogger.Logger().Debug("debug-file-record")
	content, err := os.ReadFile(paths.DaemonLog)
	if err != nil || !bytes.Contains(content, []byte("debug-file-record")) {
		t.Fatalf("debug capture missing: %v", err)
	}

	streamCtx, stopStream := context.WithTimeout(ctx, 20*time.Second)
	defer stopStream()
	streamHeaders := http.Header{"Authorization": {"Bearer " + assembly.Web.Auth.WebCredential}}
	existingStream, _, err := websocket.Dial(streamCtx, "ws://"+assembly.Web.ListenAddr()+"/logs?level=debug&format=structured", &websocket.DialOptions{HTTPHeader: streamHeaders})
	if err != nil {
		t.Fatal(err)
	}
	defer existingStream.CloseNow()
	if _, frame, err := existingStream.Read(streamCtx); err != nil || !bytes.Contains(frame, []byte(`"type":"debug"`)) {
		t.Fatalf("initial stream=%s err=%v", frame, err)
	}
	coreClient := mihomo.NewClient("http://"+settings.ControllerAddr, settings.ControllerSecret, nil)
	if err := coreClient.PatchConfigs(ctx, map[string]any{"log-level": "silent"}); err != nil {
		t.Fatal(err)
	}
	if err := assembly.Manager.SyncLogging(ctx); err != nil {
		t.Fatal(err)
	}
	status, err = client.Logging(ctx)
	if err != nil || status.Level != "silent" || group.Config().Level != logging.LevelSilent {
		t.Fatalf("silent status=%+v err=%v", status, err)
	}
	fileLogger.Logger().Error("silent-must-not-append")
	after, err := os.ReadFile(paths.DaemonLog)
	if err != nil || !bytes.Equal(content, after) {
		t.Fatal("silent changed historical file")
	}
	saved, err := config.Load(paths.Settings)
	if err != nil || saved.EffectiveLogging().Level != "silent" {
		t.Fatalf("saved silent missing: %v", err)
	}
	// Ask the fake source for a fresh frame on the connection opened before
	// the global level changed. The gateway must preserve its subscription.
	if err := existingStream.Write(streamCtx, websocket.MessageText, []byte("next fixture frame")); err != nil {
		t.Fatal(err)
	}
	if _, frame, err := existingStream.Read(streamCtx); err != nil || !bytes.Contains(frame, []byte(`"type":"debug"`)) || !bytes.Contains(frame, []byte(`"format":"structured"`)) {
		t.Fatalf("existing silent stream=%s err=%v", frame, err)
	}
	if err := existingStream.Close(websocket.StatusNormalClosure, "fixture complete"); err != nil {
		t.Fatal(err)
	}

	// The same gateway preserves debug subscriptions while files are silent.
	base := "http://" + assembly.Web.ListenAddr()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/logs?level=debug&format=structured", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+assembly.Web.Auth.WebCredential)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if readErr != nil || !bytes.Contains(body, []byte(`"type":"debug"`)) || !bytes.Contains(body, []byte(`"format":"structured"`)) {
		t.Fatalf("HTTP debug stream=%s err=%v", body, readErr)
	}
	ws, _, err := websocket.Dial(ctx, "ws://"+assembly.Web.ListenAddr()+"/logs?level=debug", &websocket.DialOptions{HTTPHeader: request.Header})
	if err != nil {
		t.Fatal(err)
	}
	_, body, readErr = ws.Read(ctx)
	_ = ws.CloseNow() // Read assertion owns failures; release the fixture connection.
	if readErr != nil || !bytes.Contains(body, []byte(`"type":"debug"`)) {
		t.Fatalf("WS debug stream=%s err=%v", body, readErr)
	}

	if _, err := client.RestartCore(ctx, protocol.MutationRequest{OperationID: "restart-silent"}); err != nil {
		t.Fatal(err)
	}
	waitForCore(t, client, done, func(s protocol.CoreStatus) bool { return s.Status == "running" && s.PID != running.PID })
	live, err := coreClient.Configs(ctx)
	if err != nil || live["log-level"] != "silent" {
		t.Fatalf("restart level=%v err=%v", live["log-level"], err)
	}
	request, err = http.NewRequestWithContext(ctx, http.MethodPatch, base+"/configs", strings.NewReader(`{"log-level":"warning"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+assembly.Web.Auth.WebCredential)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("web mutation=%d", response.StatusCode)
	}
	status, err = client.Logging(ctx)
	if err != nil || status.Level != "warn" || status.CoreLevel != "warn" {
		t.Fatalf("web adoption=%+v err=%v", status, err)
	}
}
