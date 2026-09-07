package app

import "github.com/mihari-proxy/mihari/internal/service"

func newNativeInstallAdapter(hook service.ActionHook) service.RecoveryAdapter {
	return service.NewSystemdAdapter(nil, hook)
}
