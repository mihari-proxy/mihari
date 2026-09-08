package app

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func runUnixStartup(ctx context.Context, root bool, inspect func(context.Context) (string, bool, error), bootstrap func(context.Context) error, run func(context.Context, string) error) error {
	phase, present, err := inspect(ctx)
	if err != nil {
		return err
	}
	if present || !root {
		return run(ctx, phase)
	}
	return bootstrap(ctx)
}

func startupJournalPhase(j InstallJournal, layout platform.ResolvedLayout) (string, error) {
	if err := CheckDaemonInstallJournal(j); err != nil {
		return "", err
	}
	if j.RecoveryAuthority != InstallAuthorityTarget || (j.Phase != InstallPhaseActivationCommitted && j.Phase != InstallPhaseComplete) || j.Mode != string(layout.Mode) || j.TargetPath != layout.Data.Root || j.DataRoot != layout.Data.Root || j.InstallPath != layout.InstallRoot || j.EndpointPath != layout.ControlEndpoint || j.CredentialPath != layout.CredentialPath {
		return "", installBusy("install activation does not match this instance")
	}
	return j.Phase, nil
}

func startupJournalScope(layout platform.ResolvedLayout, service bool, uid uint32, defaults platform.LayoutDefaults) (string, uint32, error) {
	if service {
		if uid != 0 {
			return "", 0, errValidationNotRoot
		}
		return defaults.BaseDir, 0711, nil
	}
	if layout.Mode == platform.SystemMode {
		return layout.BaseDir, 0711, nil
	}
	return layout.BaseDir, 0700, nil
}

func runUnixServiceStartup(ctx context.Context, inspect func(context.Context) (string, bool, error), run func(context.Context, string) error) error {
	phase, present, err := inspect(ctx)
	if err != nil {
		return err
	}
	if !present || (phase != InstallPhaseActivationCommitted && phase != InstallPhaseComplete) {
		return installBusy("system service activation is required")
	}
	return run(ctx, phase)
}
