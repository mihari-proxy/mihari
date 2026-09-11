package app

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func installationStatusReader(inspect func(context.Context) (InstallationStatus, error)) func(context.Context) (protocol.InstallationStatus, error) {
	if inspect == nil {
		return nil
	}
	return func(ctx context.Context) (protocol.InstallationStatus, error) {
		s, err := inspect(ctx)
		if err != nil {
			return protocol.InstallationStatus{}, err
		}
		return protocol.InstallationStatus{Schema: s.Schema, Kind: s.Kind, ServiceState: s.ServiceState, StartFailed: s.StartFailed, Reason: s.Reason, ID: s.ID}, nil
	}
}
