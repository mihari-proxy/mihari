package cli

import (
	"testing"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

func TestExitCodeMapsStableErrorClasses(t *testing.T) {
	tests := []struct {
		code protocol.ErrorCode
		want int
	}{
		{protocol.CodeInvalidArgument, ExitUsage},
		{protocol.CodeDaemonUnavailable, ExitDaemonUnavailable},
		{protocol.CodeInvalidState, ExitInvalidState},
		{protocol.CodePermissionDenied, ExitPermission},
		{protocol.CodeRevisionConflict, ExitConflict},
		{protocol.CodeUpstreamFailure, ExitUpstream},
		{protocol.CodeNetworkFailure, ExitNetwork},
		{protocol.CodeDataFailure, ExitData},
		{protocol.CodeInternal, ExitInternal},
	}
	for _, test := range tests {
		t.Run(string(test.code), func(t *testing.T) {
			err := protocol.APIError{Code: test.code, Message: "failed"}
			if got := exitCode(err); got != test.want {
				t.Fatalf("got=%d want=%d", got, test.want)
			}
		})
	}
}
