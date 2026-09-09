package app

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/update"
)

// binaryUpdateTarget owns a trusted binary-parent lease and private candidate.
// Publish reports rename completion independently of sync/cleanup errors.
type binaryUpdateTarget interface {
	Stage(context.Context, InstallRequest) error
	Publish(context.Context) (bool, error)
	Close() error
}

func applyBinaryOnly(ctx context.Context, req InstallRequest, currentChannel string, open func(context.Context) (binaryUpdateTarget, error), guards ...*installReplacementGuard) (result InstallResult, err error) {
	result = InstallResult{Schema: InstallResultSchema, ServiceStatus: InstallServiceNotInstalled, TransactionID: (&InstallTransaction{}).newTransactionID()}
	if req.Operation != InstallOperationUpdate || req.Bundle != "" || req.Source != "" || req.Data != "" || req.PathBinary != "" || req.Channel != currentChannel || req.Endpoint != "" || req.Credential != "" || req.InstallRoot != "" {
		return result, invalidInstallRequest()
	}
	if len(guards) > 0 && guards[0] != nil {
		if err := update.ValidateReplacementConsent(guards[0].preview, guards[0].consent); err != nil {
			return result, err
		}
	}
	target, err := open(ctx)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, target.Close()) }()
	var guard *installReplacementGuard
	if len(guards) > 0 {
		guard = guards[0]
	}
	return guard.run(ctx, func(ctx context.Context) (InstallResult, error) {
		if err = target.Stage(ctx, req); err != nil {
			return result, err
		}
		result.Changed, err = target.Publish(ctx)
		return result, err
	})
}

func dispatchInstall(ctx context.Context, req InstallRequest, channel string, inspect func(context.Context) (service.Definition, error), serviceApply func(context.Context, InstallRequest, service.Definition) (InstallResult, error), binaryOpen func(context.Context) (binaryUpdateTarget, error), guards ...*installReplacementGuard) (InstallResult, error) {
	if len(guards) > 0 && guards[0] != nil {
		if _, err := guards[0].run(ctx, func(context.Context) (InstallResult, error) { return InstallResult{}, nil }); err != nil {
			return InstallResult{}, err
		}
	}
	def, err := inspect(ctx)
	if err != nil {
		return InstallResult{}, err
	}
	switch def.Status {
	case service.StatusNotInstalled, service.StatusStopped, service.StatusRunning:
	default:
		return InstallResult{}, installBusy("service status is unknown")
	}
	if req.Operation == InstallOperationUpdate && def.Status == service.StatusNotInstalled {
		return applyBinaryOnly(ctx, req, channel, binaryOpen, guards...)
	}
	return serviceApply(ctx, req, def)
}
