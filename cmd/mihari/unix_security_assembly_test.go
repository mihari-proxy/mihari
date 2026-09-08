//go:build unix_security && (linux || darwin)

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/config"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/credential"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/securitytest"
)

// The T19 runner validates both root markers and supplies tagged platform
// defaults before invoking this test. Ordinary builds have no such seam.
func TestUnixSecurity_FullAssembly(t *testing.T) {
	defaults := platform.SystemLayoutDefaults()
	anchor := os.Getenv("MIHARI_SECURITY_ROOT")
	if os.Geteuid() != 0 || !filepath.IsAbs(anchor) || defaults.BaseDir != filepath.Join(anchor, "system") {
		t.Fatal("validated T19 defaults required before fixture IO")
	}
	var uids, gids [2]uint32
	for n, key := range []string{"MIHARI_SECURITY_UID_A", "MIHARI_SECURITY_UID_B"} {
		uid, err := strconv.ParseUint(os.Getenv(key), 10, 32)
		if err != nil || uid == 0 {
			t.Fatal("validated distinct user IDs required")
		}
		account, err := user.LookupId(strconv.FormatUint(uid, 10))
		if err != nil {
			t.Fatal(err)
		}
		gid, err := strconv.ParseUint(account.Gid, 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		uids[n], gids[n] = uint32(uid), uint32(gid)
	}
	if uids[0] == uids[1] {
		t.Fatal("fixture users must differ")
	}
	layout, err := platform.ResolveLayout(platform.LayoutInput{EUID: 0}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(layout.BaseDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("full assembly requires a fresh fixture B; existing state is preserved")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base, err := platform.OpenTrustedRoot(ctx, layout.BaseDir, platform.RootPolicy{Owner: 0, Mode: 0711, AllowCreate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := base.Close(); err != nil {
			t.Error(err)
		}
	}()
	data, err := base.OpenDir(ctx, "data", platform.RootPolicy{Owner: 0, Mode: 0700, AllowCreate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := data.Close(); err != nil {
			t.Error(err)
		}
	}()
	// Ports are fixture-owned random loopback candidates; no host service or
	// actual core/subscription is used by this assembly test.
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("c", 64) // Isolated fixture value, never a host credential.
	slots := []*string{&settings.MixedAddr, &settings.ControllerAddr, &settings.WebAddr}
	for _, slot := range slots {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		*slot = ln.Addr().String()
		if err := ln.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.Save(layout.Data.Settings, settings); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "app", "testdata", "install", "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	journal, err := app.DecodeJournal(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	journal.Phase = app.InstallPhaseComplete
	journal.RecoveryAuthority = app.InstallAuthorityTarget
	journal.TargetPath = layout.Data.Root
	journal.DataRoot = layout.Data.Root
	journal.InstallPath = layout.InstallRoot
	journal.EndpointPath = layout.ControlEndpoint
	journal.CredentialPath = layout.CredentialPath
	journal.Actions = nil
	encoded, err := app.EncodeJournal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := base.WriteFile(ctx, "install-transaction.json", encoded, 0600, nil); err != nil {
		t.Fatal(err)
	}
	installLease, err := platform.AcquireInstallLease(ctx, layout)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := installLease.Close(); err != nil {
			t.Error(err)
		}
	}()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	installer, err := app.NewUnixInstaller(layout, binary, "test")
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	productionRun := runDaemon
	runDaemon = func(ctx context.Context, options daemon.Options) error {
		manager, ok := options.Runtime.(*runtimeapi.Manager)
		if !ok {
			return errors.New("full root runtime assembly was degraded")
		}
		// Keep the actual Manager/control mutation surface. Only its background
		// process/network scheduler is suppressed at the explicit fixture boundary.
		options.Runtime = assemblyFixtureRuntime{manager}
		options.Ready = ready
		return daemon.Run(ctx, options)
	}
	defer func() { runDaemon = productionRun }()
	daemonCtx, stopDaemon := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- app.RunUnixStartup(daemonCtx, layout, func(context.Context) (bool, error) { return false, errors.New("activated startup must not bootstrap") }, func(ctx context.Context, phase string) error { return runUnixDaemon(ctx, layout, 0, phase, installer) })
	}()
	joined := false
	defer func() {
		stopDaemon()
		if !joined {
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	}()
	select {
	case <-ready:
	case err := <-done:
		joined = true
		t.Fatalf("assembly before ready: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	users := securitytest.UserRoots(t)
	for n, path := range users {
		userRoot, err := platform.OpenTrustedRoot(ctx, path, platform.RootPolicy{Owner: uids[n], Mode: 0700})
		if err != nil {
			t.Fatal(err)
		}
		if err := userRoot.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for n := range users {
		input := assemblyChildInput{Defaults: defaults, Layout: layout, UserRoot: users[n], OtherRoot: users[1-n]}
		result := runAssemblyChild(t, ctx, binary, uids[n], gids[n], input)
		if result.EUID != uids[n] || result.GID != gids[n] || !result.Authenticated || !result.SettingsDenied || !result.OtherUserDenied || !result.V2Export {
			t.Fatalf("native assembly child evidence=%+v", result)
		}
		t.Logf("unix_assembly_result=%s", mustAssemblyJSON(t, result))
	}
	stopDaemon()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	joined = true
	lease, err := platform.AcquireDaemonLease(ctx, layout)
	if err != nil {
		t.Fatalf("daemon returned before releasing lifetime: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

type assemblyFixtureRuntime struct{ *runtimeapi.Manager }

func (assemblyFixtureRuntime) Run(ctx context.Context) error { <-ctx.Done(); return nil }

type assemblyChildInput struct {
	Defaults            platform.LayoutDefaults
	Layout              platform.ResolvedLayout
	UserRoot, OtherRoot string
}
type assemblyChildResult struct {
	EUID                                                     uint32
	GID                                                      uint32
	Authenticated, SettingsDenied, OtherUserDenied, V2Export bool
}

func mustAssemblyJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func runAssemblyChild(t *testing.T, ctx context.Context, binary string, uid, gid uint32, input assemblyChildInput) assemblyChildResult {
	t.Helper()
	defaultsRead, defaultsWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = defaultsRead.Close(); _ = defaultsWrite.Close() }()
	trustedExecutable, err := platform.OpenTrustedParent(ctx, filepath.Dir(binary), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := trustedExecutable.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(binary)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0755 || st.Uid != 0 || st.Nlink != 1 {
		t.Fatal("untrusted child executable identity")
	}
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestUnixSecurity_AssemblyChild$")
	cmd.Env = []string{"MIHARI_UNIX_SECURITY_CHILD=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(mustAssemblyJSON(t, input))
	cmd.ExtraFiles = []*os.File{defaultsRead}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{}}}
	var output, stderr securitytest.BoundedBuffer
	cmd.Stdout = &output
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := defaultsRead.Close(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	envelope := struct {
		Schema   string                  `json:"schema"`
		Defaults platform.LayoutDefaults `json:"defaults"`
	}{"mihari.unix-security-defaults/v1", input.Defaults}
	_, writeErr := defaultsWrite.Write(mustAssemblyJSON(t, envelope))
	closeErr := defaultsWrite.Close()
	if err := errors.Join(writeErr, closeErr, cmd.Wait()); err != nil {
		t.Fatalf("fixture child failed: %v\n%s", err, securitytest.ChildFailureSites(output.Bytes()))
	}
	if output.Len() > 64<<10 {
		t.Fatal("child result exceeded limit")
	}
	var result assemblyChildResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestUnixSecurity_AssemblyChild(t *testing.T) {
	if os.Getenv("MIHARI_UNIX_SECURITY_CHILD") != "1" {
		t.Skip("root fixture invokes this child adapter explicitly")
	}
	var input assemblyChildInput
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	if platform.SystemLayoutDefaults() != input.Defaults || os.Geteuid() == 0 {
		t.Fatal("controlled child defaults/identity missing")
	}
	uid := uint32(os.Geteuid())
	locator, err := input.Layout.Locator(uid)
	if err != nil {
		t.Fatal(err)
	}
	client := controlclient.WithCredentialProvider(locator, credential.NewProvider(locator))
	input.Layout.ClientLogs = platform.NewPaths(input.UserRoot)
	fs, err := platform.OpenClientLogFS(context.Background(), input.Layout)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := openTUILogging(context.Background(), input.Layout.ClientLogs, "", fs, os.Stderr)
	if err != nil {
		_ = resources.Close()
		t.Fatal(err)
	}
	if err := client.SetRedactor(resources.Redactor); err != nil {
		_ = resources.Close()
		t.Fatal(err)
	}
	status, err := client.Status(context.Background())
	if err != nil {
		_ = resources.Close()
		t.Fatal(err)
	}
	result := assemblyChildResult{EUID: uid, GID: uint32(os.Getegid()), Authenticated: slices.Contains(status.Capabilities, protocol.MachineLogSnapshotCapability)}
	file, openErr := os.Open(input.Layout.Data.Settings)
	result.SettingsDenied = errors.Is(openErr, os.ErrPermission)
	if file != nil {
		_ = file.Close()
	}
	other, otherErr := os.Open(input.OtherRoot)
	result.OtherUserDenied = errors.Is(otherErr, os.ErrPermission)
	if other != nil {
		_ = other.Close()
	}
	resources.Runtime.Logger().Info("current user assembly fixture")
	exported, err := exportAssembledLogs(context.Background(), logging.ExportRequest{Range: logging.ExportRange{Kind: logging.RangeAll}, Now: time.Now().UTC()}, assembledExportOptions{Scope: logging.ExportScopeMachineAndCurrentUser, UserLogs: input.Layout.ClientLogs, Resources: resources, OpenMachineSnapshot: client.OpenMachineSnapshot})
	if err != nil {
		_ = resources.Close()
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(exported.Path)
	if err != nil {
		_ = resources.Close()
		t.Fatal(err)
	}
	for _, entry := range archive.File {
		if entry.Name == "manifest.json" {
			reader, err := entry.Open()
			if err != nil {
				t.Fatal(err)
			}
			var manifest struct {
				Schema string `json:"schema"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(reader, 64<<10)).Decode(&manifest)
			if err := errors.Join(decodeErr, reader.Close()); err != nil {
				t.Fatal(err)
			}
			result.V2Export = manifest.Schema == "mihari-logs-export/v2"
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := resources.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stdout.Write(mustAssemblyJSON(t, result)); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}
