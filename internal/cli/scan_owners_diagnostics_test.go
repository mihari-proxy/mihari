package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func TestOwnerScan_CLIClassifiersPreserveCausesWithoutChangingPublicErrors(t *testing.T) {
	cause := errors.New("private transport or installation cause")
	remote := diagnostics.Wrap(protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "upstream unavailable"}, cause)
	for name, err := range map[string]error{
		"runtime remote":       classifyRuntimeError(remote),
		"runtime unclassified": classifyRuntimeError(cause),
		"installation":         classifyInstallationError(cause),
	} {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(err, cause) {
				t.Fatalf("cause was lost: %v", err)
			}
			if strings.Contains(err.Error(), cause.Error()) {
				t.Fatalf("public CLI error exposed cause: %v", err)
			}
		})
	}
}
