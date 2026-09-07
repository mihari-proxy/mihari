package app

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/service"
)

// binaryUpdateTarget owns a trusted binary-parent lease and private candidate.
// Publish reports rename completion independently of sync/cleanup errors.
type binaryUpdateTarget interface {
	Stage(context.Context, InstallRequest) error
	Publish(context.Context) (bool, error)
	Close() error
}

func applyBinaryOnly(ctx context.Context, req InstallRequest, currentChannel string, open func(context.Context) (binaryUpdateTarget, error)) (result InstallResult, err error) {
	result = InstallResult{Schema: InstallResultSchema, ServiceStatus: InstallServiceNotInstalled, TransactionID: (&InstallTransaction{}).newTransactionID()}
	if req.Operation != InstallOperationUpdate || req.Bundle != "" || req.Source != "" || req.Data != "" || req.PathBinary != "" || req.Channel != currentChannel || req.Endpoint != "" || req.Credential != "" || req.InstallRoot != "" {
		return result, invalidInstallRequest()
	}
	target, err := open(ctx)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, target.Close()) }()
	if err = target.Stage(ctx, req); err != nil {
		return result, err
	}
	result.Changed, err = target.Publish(ctx)
	return result, err
}

func dispatchInstall(ctx context.Context, req InstallRequest, channel string, inspect func(context.Context) (service.Definition, error), serviceApply func(context.Context, InstallRequest, service.Definition) (InstallResult, error), binaryOpen func(context.Context) (binaryUpdateTarget, error)) (InstallResult, error) {
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
		return applyBinaryOnly(ctx, req, channel, binaryOpen)
	}
	return serviceApply(ctx, req, def)
}
