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
		if sha, ok := strings.CutPrefix(candidate, "alpha-"); ok && isHex(sha) {
			return candidate, nil
		}
	}
	return "", protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid mihomo version output"}
}

func ParseAlphaSHA(name string) string {
	sha, ok := parseAlphaAsset(name)
	if !ok {
		return ""
	}
	return sha
}

func parseAlphaAsset(name string) (string, bool) {
	base := name
	switch lower := strings.ToLower(name); {
	case strings.HasSuffix(lower, ".gz"):
		base = name[:len(name)-len(".gz")]
	case strings.HasSuffix(lower, ".zip"):
		base = name[:len(name)-len(".zip")]
	}
	parts := strings.Split(base, "-")
	if len(parts) != 5 || parts[0] != "mihomo" || parts[3] != "alpha" || !isHex(parts[4]) {
		return "", false
	}
	return parts[4], true
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.Is(unicode.ASCII_Hex_Digit, r) {
			return false
		}
	}
	return true
}
