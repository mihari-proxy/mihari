package core

import (
	"context"
	"strings"
	"unicode"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func DetectVersion(ctx context.Context, runner CommandRunner, binaryPath string) (string, error) {
	if runner == nil {
		runner = OSCommandRunner{}
	}
	output, err := runner.Run(ctx, binaryPath, "-v")
	if err != nil {
		return "", protocol.APIError{Code: protocol.CodeDataFailure, Message: "read mihomo version failed"}
	}
	return ParseVersion(string(output))
}

func ParseVersion(output string) (string, error) {
	for _, field := range strings.Fields(output) {
		candidate := strings.Trim(field, " ,;()[]")
		if len(candidate) >= 4 && candidate[0] == 'v' && unicode.IsDigit(rune(candidate[1])) && strings.Contains(candidate, ".") {
			return candidate, nil
		}
	}
	return "", protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid mihomo version output"}
}
