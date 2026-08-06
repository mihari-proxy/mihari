package transport

import (
	"context"
	"io"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	transporttest "github.com/mihari-proxy/mihari/internal/control/transport/testutil"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestListenAndDialRoundTrip(t *testing.T) {
	endpoint := transporttest.Endpoint(t)
	listener, err := Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	done := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer connection.Close()
		_, err = connection.Write([]byte("pong"))
		done <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connection, err := DialContext(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(connection)
	connection.Close()
	if err != nil || string(body) != "pong" {
		t.Fatalf("body=%q err=%v", body, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEndpointIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 32 {
		endpoint := transporttest.Endpoint(t)
		if seen[endpoint] {
			t.Fatalf("duplicate endpoint %q", endpoint)
		}
		seen[endpoint] = true
	}
}

func TestDefaultCredentialPathUsesDataRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cred-data")
	t.Setenv("MIHARI_DATA", root)
	t.Setenv("MIHARI_CONTROL_CREDENTIAL", "")
	got := DefaultCredentialPath()
	want := filepath.Join(root, "control.token")
	if got != want {
		t.Fatalf("credential=%q want=%q", got, want)
	}
	if got != platform.DefaultPaths().ControlToken {
		t.Fatalf("credential diverged from platform paths: %q vs %q", got, platform.DefaultPaths().ControlToken)
	}
}

func TestDefaultCredentialPathHonorsExplicitOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.token")
	t.Setenv("MIHARI_CONTROL_CREDENTIAL", path)
	if got := DefaultCredentialPath(); got != path {
		t.Fatalf("got=%q want=%q", got, path)
	}
}

func TestDefaultEndpointHonorsOverride(t *testing.T) {
	const want = "custom-endpoint-value"
	t.Setenv("MIHARI_CONTROL_ENDPOINT", want)
	if got := DefaultEndpoint(); got != want {
		t.Fatalf("got=%q want=%q", got, want)
	}
}

func TestDefaultEndpointWithoutOverride(t *testing.T) {
	t.Setenv("MIHARI_CONTROL_ENDPOINT", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	root := filepath.Join(t.TempDir(), "sock-data")
	t.Setenv("MIHARI_DATA", root)
	got := DefaultEndpoint()
	if runtime.GOOS == "windows" {
		if got != `\\.\pipe\mihari-control` {
			t.Fatalf("windows endpoint=%q", got)
		}
		return
	}
	want := filepath.Join(root, "control.sock")
	if got != want {
		t.Fatalf("unix endpoint=%q want=%q", got, want)
	}
}
