package main

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"io"
)

func runNativeInstallValidation(context.Context, string, string, io.Writer) error {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: "install validation requires a Unix installer lease"}
}
