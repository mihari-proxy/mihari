package update

import (
	"cmp"
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
	m := canonicalStable.FindStringSubmatch(tag)
	isDev := false
	if m == nil {
		m = canonicalDev.FindStringSubmatch(tag)
		isDev = true
	}
	if m == nil {
		return canonicalTag{}, false
	}
	var fields [4]int
	for i, part := range m[1:] {
		value, err := strconv.Atoi(part)
		if err != nil {
			return canonicalTag{}, false
		}
		fields[i] = value
	}
	return canonicalTag{major: fields[0], minor: fields[1], patch: fields[2], dev: fields[3], isDev: isDev}, true
}

func compareCanonicalTags(left, right string) (int, bool) {
	a, okA := parseCanonicalTag(left)
	b, okB := parseCanonicalTag(right)
	if !okA || !okB {
		return 0, false
	}
	if c := cmp.Compare(a.major, b.major); c != 0 {
		return c, true
	}
	if c := cmp.Compare(a.minor, b.minor); c != 0 {
		return c, true
	}
	if c := cmp.Compare(a.patch, b.patch); c != 0 {
		return c, true
	}
	if a.isDev != b.isDev {
		if a.isDev {
			return -1, true
		}
		return 1, true
	}
	if a.isDev {
		return cmp.Compare(a.dev, b.dev), true
	}
	return 0, true
}

func classifyUpdate(current, latest string) (available, ahead bool) {
	if sameTag(current, latest) {
		return false, false
	}
	normalized := strings.TrimSpace(current)
	currentTag, ok := parseCanonicalTag(normalized)
	if !ok {
		if normalized == "" || strings.HasPrefix(strings.ToLower(normalized), "v") {
			return true, false
		}
		normalized = "v" + normalized
		currentTag, ok = parseCanonicalTag(normalized)
		if !ok {
			return true, false
		}
	}
	latestTag, latestOK := parseCanonicalTag(latest)
	if currentTag.isDev && latestOK && !latestTag.isDev {
		return true, false
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
