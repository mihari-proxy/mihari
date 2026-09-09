package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/mihari-proxy/mihari/internal/platform"
	"golang.org/x/sys/windows/svc/mgr"
)

// ObserveReplacementService reads the registration and the exact staging path.
// It performs no service control or installation writes.
func (m *Manager) ObserveReplacementService(ctx context.Context) (out ServiceReplacementView, err error) {
	if err = ctx.Err(); err != nil {
		return out, err
	}
	manager, err := mgr.Connect()
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, manager.Disconnect()) }()
	svc, err := manager.OpenService(serviceName)
	if err != nil {
		if isServiceDoesNotExist(err) {
			return windowsReplacementServiceView(ctx, nil, "")
		}
		return out, err
	}
	defer func() { err = errors.Join(err, svc.Close()) }()
	config, err := svc.Config()
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	binary, err := platform.AbsoluteInstalledBinaryPath()
	if err != nil {
		return out, err
	}
	return windowsReplacementServiceView(ctx, &config, binary)
}

func windowsReplacementServiceView(ctx context.Context, config *mgr.Config, binary string) (ServiceReplacementView, error) {
	if err := ctx.Err(); err != nil {
		return ServiceReplacementView{}, err
	}
	// SCM Config excludes transient status. Keep its full persistent definition
	// inside the hash; neither command lines nor service account data are rendered.
	raw, err := json.Marshal(config)
	if err != nil {
		return ServiceReplacementView{}, err
	}
	sum := sha256.Sum256(raw)
	return ServiceReplacementView{Registered: config != nil, BinaryPath: binary, DefinitionSHA256: hex.EncodeToString(sum[:])}, nil
}
