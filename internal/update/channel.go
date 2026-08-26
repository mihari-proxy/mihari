package update

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

const (
	// ChannelMain tracks official GitHub latest releases.
	ChannelMain = "main"
	// ChannelDev tracks canonical prerelease tags vX.Y.Z-dev.N.
	ChannelDev = "dev"
)

var (
	canonicalStable = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	canonicalDev    = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-dev\.(0|[1-9][0-9]*)$`)
)

type canonicalTag struct {
	major, minor, patch, dev int
	isDev                    bool
}

func parseCanonicalTag(tag string) (canonicalTag, bool) {
	if m := canonicalStable.FindStringSubmatch(tag); m != nil {
		return canonicalTag{major: atoi(m[1]), minor: atoi(m[2]), patch: atoi(m[3])}, true
	}
	if m := canonicalDev.FindStringSubmatch(tag); m != nil {
		return canonicalTag{major: atoi(m[1]), minor: atoi(m[2]), patch: atoi(m[3]), dev: atoi(m[4]), isDev: true}, true
	}
	return canonicalTag{}, false
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func compareCanonicalTags(left, right string) (int, bool) {
	a, okA := parseCanonicalTag(left)
	b, okB := parseCanonicalTag(right)
	if !okA || !okB {
		return 0, false
	}
	if c := a.major - b.major; c != 0 {
		return sign(c), true
	}
	if c := a.minor - b.minor; c != 0 {
		return sign(c), true
	}
	if c := a.patch - b.patch; c != 0 {
		return sign(c), true
	}
	if a.isDev != b.isDev {
		if a.isDev {
			return -1, true
		}
		return 1, true
	}
	if a.isDev {
		return sign(a.dev - b.dev), true
	}
	return 0, true
}

func sign(n int) int {
	if n < 0 {
		return -1
	}
	if n > 0 {
		return 1
	}
	return 0
}

func classifyUpdate(current, latest string) (available, ahead bool) {
	if sameTag(current, latest) {
		return false, false
	}
	normalized := strings.TrimSpace(current)
	if _, ok := parseCanonicalTag(normalized); !ok {
		if normalized == "" || strings.HasPrefix(strings.ToLower(normalized), "v") {
			return true, false
		}
		normalized = "v" + normalized
		if _, ok := parseCanonicalTag(normalized); !ok {
			return true, false
		}
	}
	cmp, ok := compareCanonicalTags(latest, normalized)
	if !ok {
		return true, false
	}
	if cmp > 0 {
		return true, false
	}
	if cmp < 0 {
		return false, true
	}
	return false, false
}

func normalizeChannel(channel string) (string, error) {
	switch strings.TrimSpace(channel) {
	case "", ChannelMain:
		return ChannelMain, nil
	case ChannelDev:
		return ChannelDev, nil
	default:
		return "", protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "mihari channel must be main or dev"}
	}
}

// LoadChannel reads the Mihari release channel from path.
// A missing file defaults to ChannelMain. An invalid first line is an error.
func LoadChannel(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ChannelMain, nil
	}
	if err != nil {
		return "", protocol.APIError{Code: protocol.CodeDataFailure, Message: "read mihari channel"}
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	switch strings.TrimSpace(line) {
	case ChannelMain, ChannelDev:
		return strings.TrimSpace(line), nil
	default:
		return "", protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid mihari channel file"}
	}
}

// SaveChannel atomically writes channel to path. Only main and dev are accepted.
func SaveChannel(path, channel string) error {
	switch channel {
	case ChannelMain, ChannelDev:
	default:
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "mihari channel must be main or dev"}
	}
	parent := filepath.Dir(path)
	_, statErr := os.Lstat(parent)
	newParent := errors.Is(statErr, os.ErrNotExist)
	if err := config.AtomicWrite(path, []byte(channel+"\n"), 0o600); err != nil {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "write mihari channel"}
	}
	return platform.OwnChannelWrite(path, newParent)
}
