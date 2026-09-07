//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/update"
)

// UnixInstaller is the callable Unix install and self-update use case. Constructing
// it performs no IO; platform entrypoint assembly chooses when to inject it.
// Every Apply/RunService owns and closes one outer install session. Prepare owns
// only an inert downloaded candidate, which the caller must Close after Apply.
type UnixInstaller struct {
	layout                                platform.ResolvedLayout
	binary, version, channel, offlineRoot string
	client                                *http.Client
	downloader                            update.SelfUpdater
	adapter                               func(service.ActionHook) service.RecoveryAdapter
}

// NewUnixInstaller wires real retained capabilities, WAL recovery, immutable
// release verification, validation child and the native system service adapter.
// It does not change the process's default layout selection.
func NewUnixInstaller(layout platform.ResolvedLayout, binary, version string) (*UnixInstaller, error) {
	if (layout.Mode != platform.SystemMode && layout.Mode != platform.PrivateMode) || !filepath.IsAbs(binary) {
		return nil, invalidInstallRequest()
	}
	channel := update.ChannelMain
	if strings.Contains(version, "-dev.") {
		channel = update.ChannelDev
	}
	return &UnixInstaller{layout: layout, binary: filepath.Clean(binary), version: version, channel: channel, offlineRoot: filepath.Join(layout.InstallRoot, "install-trust"), adapter: newNativeInstallAdapter}, nil
}

// Channel inspects the service before opening B. Standalone updates use the
// running binary's release channel and never access an installation sidecar.
func (i *UnixInstaller) Channel(ctx context.Context) (channel string, err error) {
	defer func() { err = ClassifyUnixLocalError(err) }()
	def, err := i.adapter(nil).InspectDefinition(ctx)
	if err != nil {
		return "", err
	}
	if def.Status == service.StatusNotInstalled {
		return i.channel, nil
	}
	raw, err := platform.ReadInstallChannel(ctx, i.layout)
	if errors.Is(err, os.ErrNotExist) {
		return i.channel, nil
	}
	if err != nil {
		return "", err
	}
	channel = strings.TrimSpace(string(raw))
	if channel != update.ChannelMain && channel != update.ChannelDev {
		return "", migrateData("invalid installed release channel")
	}
	return channel, nil
}

// Prepare downloads and verifies fixed-tag inert bytes without stopping a service.
func (i *UnixInstaller) Prepare(ctx context.Context, binary, current, channel string) (update.PreparedUpdate, error) {
	if filepath.Clean(binary) != i.binary {
		return update.PreparedUpdate{}, invalidInstallRequest()
	}
	return i.downloader.Prepare(ctx, binary, current, channel)
}

// Check preserves the existing update-check contract without installation IO.
func (i *UnixInstaller) Check(ctx context.Context, current, channel string) (update.CheckResult, error) {
	return i.downloader.Check(ctx, current, channel)
}

// ApplyPrepared re-verifies candidate authority after the UI has closed workers,
// logging and filesystem owners; it never trusts PreparedUpdate.SHA256 alone.
func (i *UnixInstaller) ApplyPrepared(ctx context.Context, prepared update.PreparedUpdate) (update.Result, error) {
	result := update.Result{Version: prepared.Version, Ahead: prepared.Ahead, Channel: prepared.Channel}
	if !prepared.Available {
		return result, nil
	}
	def, err := i.adapter(nil).InspectDefinition(ctx)
	if err != nil {
		return result, err
	}
	req := i.request(InstallOperationUpdate, prepared.CandidatePath, prepared.Version, prepared.Channel)
	req.ArtifactSHA256 = prepared.SHA256
	if def.Status == service.StatusNotInstalled {
		req.Layout = InstallLayoutSystem
		req.Data = ""
		req.Endpoint = ""
		req.Credential = ""
		req.InstallRoot = ""
		req.PathBinary = ""
	}
	applied, err := i.applyWithStart(ctx, req, false)
	result.Updated = applied.Changed
	return result, err
}

// Update is the CLI equivalent of Prepare, owned cleanup, then ApplyPrepared.
func (i *UnixInstaller) Update(ctx context.Context, binary, current, channel string) (result update.Result, err error) {
	prepared, err := i.Prepare(ctx, binary, current, channel)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, ClassifyUnixLocalError(prepared.Close())) }()
	return i.ApplyPrepared(ctx, prepared)
}

// Apply is the shell/JSON service apply entrypoint; install requests start the
// service after validation. CLI install calls RunService and registers it stopped.
func (i *UnixInstaller) Apply(ctx context.Context, req InstallRequest) (InstallResult, error) {
	return i.applyWithStart(ctx, req, true)
}
func (i *UnixInstaller) applyWithStart(ctx context.Context, req InstallRequest, start bool) (InstallResult, error) {
	if os.Geteuid() != 0 {
		return InstallResult{}, errValidationNotRoot
	}
	if _, err := EncodeInstallRequest(req); err != nil {
		return InstallResult{}, err
	}
	return dispatchInstall(ctx, req, i.channel, i.adapter(nil).InspectDefinition, func(ctx context.Context, req InstallRequest, def service.Definition) (InstallResult, error) {
		return i.applyService(ctx, req, def, start)
	}, func(ctx context.Context) (binaryUpdateTarget, error) {
		return openUnixBinaryTarget(ctx, i.binary)
	})
}
func (i *UnixInstaller) request(operation, binary, tag, channel string) InstallRequest {
	req := InstallRequest{Schema: InstallRequestSchema, Operation: operation, Binary: binary, ReleaseTag: tag, Channel: channel, Layout: string(i.layout.Mode)}
	if i.layout.Mode == platform.PrivateMode {
		req.Data = i.layout.Data.Root
	}
	defaults, err := platform.ResolveLayout(platform.LayoutInput{EUID: 0}, platform.SystemLayoutDefaults())
	if err == nil {
		if i.layout.ControlEndpoint != defaults.ControlEndpoint {
			req.Endpoint = i.layout.ControlEndpoint
		}
		if i.layout.CredentialPath != defaults.CredentialPath {
			req.Credential = i.layout.CredentialPath
		}
		if i.layout.InstallRoot != defaults.InstallRoot {
			req.InstallRoot = i.layout.InstallRoot
		}
	}
	if filepath.Join(i.layout.InstallRoot, "mihari") != i.binary {
		req.PathBinary = i.binary
	}
	return req
}
func (i *UnixInstaller) applyService(ctx context.Context, req InstallRequest, observed service.Definition, start bool) (result InstallResult, err error) {
	layout := i.layout
	if req.Operation != InstallOperationRecover {
		layout, err = platform.ResolveLayout(platform.LayoutInput{CWD: "/", Data: req.Data, Endpoint: req.Endpoint, Credential: req.Credential, InstallRoot: req.InstallRoot, EUID: 0}, platform.SystemLayoutDefaults())
		if err != nil {
			return result, err
		}
	}
	session, err := openNativeInstallSession(ctx, layout, i.adapter)
	if err != nil {
		return result, ClassifyUnixLocalError(err)
	}
	defer func() { err = errors.Join(err, ClassifyUnixLocalError(session.Close())) }()
	existing, err := session.loadState(ctx)
	if err != nil {
		return result, err
	}
	if existing {
		if err := session.tx.RecoverLocked(ctx, session); err != nil {
			return result, err
		}
	}
	if req.Operation == InstallOperationRecover {
		if !existing {
			return InstallResult{Schema: InstallResultSchema, ServiceStatus: installStatus(observed), TransactionID: session.tx.newTransactionID(), SourceRetained: true}, nil
		}
		return session.tx.resultFromJournal(ctx)
	}
	if session.layout != layout {
		if err := session.lease.BindInstallLayout(ctx, layout); err != nil {
			return result, err
		}
		session.layout = layout
	}
	observed, err = session.tx.Service.InspectDefinition(ctx)
	if err != nil {
		return result, err
	}
	source, err := selectInstallSource(req, observed, layout)
	if err != nil {
		return result, err
	}
	inputs, err := prepareNativeReleaseInputs(ctx, req, source, i.offlineRoot, i.client)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, inputs.Close()) }()
	if err := session.prepare(ctx, req, observed, inputs, start); err != nil {
		return result, err
	}
	result, err = session.tx.ApplyLocked(ctx, session, req)
	if err != nil {
		// Recover under the same outer lease after validation cleanup has joined.
		recoveryErr := session.tx.RecoverLocked(context.WithoutCancel(ctx), session)
		err = errors.Join(err, recoveryErr)
		if session.tx.journal.RecoveryAuthority == InstallAuthorityTarget {
			result.Changed = true
		}
	}
	return result, err
}
func installStatus(def service.Definition) string {
	switch def.Status {
	case service.StatusNotInstalled:
		return InstallServiceNotInstalled
	case service.StatusRunning:
		return InstallServiceRunning
	default:
		return InstallServiceStopped
	}
}

// RunService funnels CLI lifecycle operations through the same recovery session.
func (i *UnixInstaller) RunService(ctx context.Context, operation string) (err error) {
	if operation == InstallOperationInstall || operation == InstallOperationReinstall {
		channel, err := i.Channel(ctx)
		if err != nil {
			return err
		}
		_, err = i.applyWithStart(ctx, i.request(operation, i.binary, i.version, channel), false)
		return err
	}
	if os.Geteuid() != 0 {
		return errValidationNotRoot
	}
	_, err = i.adapter(nil).InspectDefinition(ctx)
	if err != nil {
		return err
	}
	session, err := openNativeInstallSession(ctx, i.layout, i.adapter)
	if err != nil {
		return ClassifyUnixLocalError(err)
	}
	defer func() { err = errors.Join(err, ClassifyUnixLocalError(session.Close())) }()
	existing, err := session.loadState(ctx)
	if err != nil {
		return err
	}
	if existing {
		if err := session.tx.RecoverLocked(ctx, session); err != nil {
			return err
		}
	}
	switch operation {
	case "recover":
		return nil
	case "start", "stop", "restart", "uninstall":
		return session.runLifecycle(ctx, operation)
	default:
		return invalidInstallRequest()
	}
}

// Status uses a bounded read-only service query for legacy controller consumers.
func (i *UnixInstaller) Status() (status service.StatusKind, err error) {
	defer func() { err = ClassifyUnixLocalError(err) }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	def, err := i.adapter(nil).InspectDefinition(ctx)
	return def.Status, err
}
func (i *UnixInstaller) Install() error   { return i.RunService(context.Background(), "install") }
func (i *UnixInstaller) Reinstall() error { return i.RunService(context.Background(), "reinstall") }
func (i *UnixInstaller) Start() error     { return i.RunService(context.Background(), "start") }
func (i *UnixInstaller) Stop() error      { return i.RunService(context.Background(), "stop") }
func (i *UnixInstaller) Restart() error   { return i.RunService(context.Background(), "restart") }
func (i *UnixInstaller) Uninstall() error { return i.RunService(context.Background(), "uninstall") }
