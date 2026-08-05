package cli

import (
	"errors"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

const (
	ExitOK                = 0
	ExitUsage             = 2
	ExitDaemonUnavailable = 3
	ExitInvalidState      = 4
	ExitPermission        = 5
	ExitConflict          = 6
	ExitUpstream          = 7
	ExitNetwork           = 8
	ExitData              = 9
	ExitInternal          = 10
)

func exitCode(err error) int {
	var apiError protocol.APIError
	if !errors.As(err, &apiError) {
		return ExitInternal
	}
	switch apiError.Code {
	case protocol.CodeInvalidArgument, protocol.CodeSystemProxyNotOwned:
		return ExitUsage
	case protocol.CodeDaemonUnavailable:
		return ExitDaemonUnavailable
	case protocol.CodeInvalidState:
		return ExitInvalidState
	case protocol.CodePermissionDenied:
		return ExitPermission
	case protocol.CodeRevisionConflict, protocol.CodeSystemProxyConflict:
		return ExitConflict
	case protocol.CodeUpstreamFailure:
		return ExitUpstream
	case protocol.CodeNetworkFailure:
		return ExitNetwork
	case protocol.CodeDataFailure:
		return ExitData
	default:
		return ExitInternal
	}
}
