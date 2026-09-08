package main

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func runNativeInstallValidation(context.Context, string, string) error {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: "install validation requires a Unix installer lease"}
}
