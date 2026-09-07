//go:build unix_security && (linux || darwin)

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/credential"
	"github.com/mihari-proxy/mihari/internal/control/server"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/securitytest"
	"github.com/mihari-proxy/mihari/internal/state"
)

func TestSecurityTwoUIDControl(t *testing.T)       { securityControl(t, true) }
func TestSecurityPrivateDataDenied(t *testing.T)   { securityControl(t, false) }
func TestSecurityOtherUserLogsDenied(t *testing.T) { securityControl(t, false) }

func securityControl(t *testing.T, evidence bool) {
	layout, uids, gids := securitytest.Parent(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	lease, err := platform.AcquireDaemonLease(ctx, layout)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lease.Close(); err != nil {
			t.Error(err)
		}
	}()
	token, err := credential.LoadOrCreateOwned(ctx, layout, lease)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := transport.ListenOwned(ctx, layout, lease)
	if err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{layout.CredentialPath: 0644, layout.ControlEndpoint: 0666, layout.Data.Root: 0700} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("native mode mismatch: %v", err)
		}
	}
	if err := os.WriteFile(layout.Data.Settings, []byte("root-private-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	srv := server.New(server.Options{Token: token, Store: state.NewStore(state.Snapshot{Version: "native-security"})})
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, listener) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	anchor := os.Getenv("MIHARI_SECURITY_ROOT")
	users := [2]string{filepath.Join(anchor, "users", "a"), filepath.Join(anchor, "users", "b")}
	for index := range uids {
		result := securitytest.Run(t, ctx, "TestSecurityControlChild", uids[index], gids[index], securitytest.Input{Defaults: platform.SystemLayoutDefaults(), Layout: layout, Own: users[index], Other: users[1-index]})
		if !result.Authenticated || !result.PrivateDenied || !result.OtherDenied || !result.OwnLog {
			t.Fatalf("actual child boundary failed: %+v", result)
		}
		if evidence {
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("security_child=%s", raw)
		}
	}
}

func TestSecurityControlChild(t *testing.T) {
	input := securitytest.ReadInput(t)
	uid := uint32(os.Geteuid())
	locator, err := input.Layout.Locator(uid)
	if err != nil {
		t.Fatal(err)
	}
	client := controlclient.WithCredentialProvider(locator, credential.NewProvider(locator))
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result := securitytest.Result{EUID: uid, GID: uint32(os.Getegid()), Authenticated: status.DaemonVersion == "native-security"}
	// Authentication is the positive control for each denial. The ordinary UID
	// cannot read or create inside D, but can use its independently owned U.
	private, readErr := os.Open(input.Layout.Data.Settings)
	if private != nil {
		_ = private.Close()
	}
	createErr := os.WriteFile(filepath.Join(input.Layout.Data.Root, "uid-attempt"), []byte("forbidden"), 0600)
	result.PrivateDenied = errors.Is(readErr, os.ErrPermission) && errors.Is(createErr, os.ErrPermission)
	own, err := platform.OpenTrustedRoot(context.Background(), input.Own, platform.RootPolicy{Owner: uid, Mode: 0700})
	if err != nil {
		t.Fatal(err)
	}
	if err := own.Close(); err != nil {
		t.Fatal(err)
	}
	ownLog := filepath.Join(input.Own, "mihari-tui.log")
	if err := os.WriteFile(ownLog, []byte("own-user-log"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(ownLog)
	result.OwnLog = err == nil && string(raw) == "own-user-log"
	other, otherErr := os.Open(input.Other)
	if other != nil {
		_ = other.Close()
	}
	otherWrite := os.WriteFile(filepath.Join(input.Other, "mihari-tui.log"), []byte("forbidden"), 0600)
	result.OtherDenied = errors.Is(otherErr, os.ErrPermission) && errors.Is(otherWrite, os.ErrPermission)
	securitytest.Emit(t, result)
}
