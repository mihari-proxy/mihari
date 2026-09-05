package platform

import (
	"fmt"
	pathpkg "path"
	"runtime"
	"strings"
)

const windowsControlEndpoint = `\\.\pipe\mihari-control`

// LayoutMode identifies the ownership and discovery rules for a resolved layout.
type LayoutMode string

const (
	SystemMode  LayoutMode = "system"
	PrivateMode LayoutMode = "private"
	WindowsMode LayoutMode = "windows"
)

// LayoutInput contains process-captured values used to resolve a layout.
type LayoutInput struct {
	CWD         string
	Data        string
	Endpoint    string
	Credential  string
	InstallRoot string
	Home        string
	XDGState    string
	EUID        uint32
}

// LayoutDefaults contains trusted target-platform defaults.
type LayoutDefaults struct {
	OS          string
	BaseDir     string
	InstallRoot string
	TrustedHome string
	SocketLimit int
}

// ResolvedLayout is the pure, absolute path model consumed by later platform capabilities.
type ResolvedLayout struct {
	Mode            LayoutMode
	BaseDir         string
	Data            Paths
	ControlEndpoint string
	CredentialPath  string
	ChannelPath     string
	InstallRoot     string
	ClientLogs      Paths
}

// ResolveLayout resolves process-captured inputs without filesystem access.
func ResolveLayout(input LayoutInput, defaults LayoutDefaults) (ResolvedLayout, error) {
	if defaults.OS != "linux" && defaults.OS != "darwin" && defaults.OS != "windows" {
		return ResolvedLayout{}, fmt.Errorf("invalid argument: unsupported layout OS %q", defaults.OS)
	}

	baseDir, err := absoluteLayoutPath(defaults.OS, "base directory", defaults.BaseDir, "")
	if err != nil {
		return ResolvedLayout{}, err
	}
	installRoot := input.InstallRoot
	if installRoot == "" {
		installRoot = defaults.InstallRoot
	}
	installRoot, err = absoluteLayoutPath(defaults.OS, "install root", installRoot, input.CWD)
	if err != nil {
		return ResolvedLayout{}, err
	}

	mode := SystemMode
	dataRoot := targetJoin(defaults.OS, baseDir, "data")
	clientRoot := ""
	if defaults.OS == "windows" {
		mode = WindowsMode
		dataRoot = baseDir
		clientRoot = dataRoot
	}
	if input.Data != "" {
		dataRoot, err = absoluteLayoutPath(defaults.OS, "data root", input.Data, input.CWD)
		if err != nil {
			return ResolvedLayout{}, err
		}
		if defaults.OS == "windows" {
			baseDir = dataRoot
			clientRoot = dataRoot
		} else {
			mode = PrivateMode
			if pathsOverlap(defaults.OS, dataRoot, baseDir) || pathsOverlap(defaults.OS, dataRoot, targetJoin(defaults.OS, baseDir, "data")) {
				return ResolvedLayout{}, fmt.Errorf("invalid argument: private data root overlaps system layout")
			}
			baseDir = dataRoot
			clientRoot = dataRoot
		}
	}

	if mode == SystemMode {
		clientRoot, err = systemClientRoot(input, defaults)
		if err != nil {
			return ResolvedLayout{}, err
		}
	}

	endpoint := input.Endpoint
	if endpoint == "" {
		if defaults.OS == "windows" {
			endpoint = windowsControlEndpoint
		} else {
			endpoint = targetJoin(defaults.OS, baseDir, "control.sock")
		}
	} else {
		endpoint, err = absoluteLayoutPath(defaults.OS, "control endpoint", endpoint, input.CWD)
		if err != nil {
			return ResolvedLayout{}, err
		}
	}
	if defaults.OS != "windows" {
		if defaults.SocketLimit <= 0 {
			return ResolvedLayout{}, fmt.Errorf("invalid argument: socket path limit must be positive")
		}
		if len(endpoint) > defaults.SocketLimit {
			return ResolvedLayout{}, fmt.Errorf("invalid argument: control endpoint is %d bytes; limit is %d", len(endpoint), defaults.SocketLimit)
		}
	}

	credential := input.Credential
	if credential == "" {
		credential = targetJoin(defaults.OS, baseDir, "control.token")
	} else {
		credential, err = absoluteLayoutPath(defaults.OS, "credential path", credential, input.CWD)
		if err != nil {
			return ResolvedLayout{}, err
		}
	}

	data := newTargetPaths(defaults.OS, dataRoot)
	clientLogs := newTargetPaths(defaults.OS, clientRoot)
	return ResolvedLayout{
		Mode:            mode,
		BaseDir:         baseDir,
		Data:            data,
		ControlEndpoint: endpoint,
		CredentialPath:  credential,
		ChannelPath:     targetJoin(defaults.OS, baseDir, "mihari-channel"),
		InstallRoot:     installRoot,
		ClientLogs:      clientLogs,
	}, nil
}

func systemClientRoot(input LayoutInput, defaults LayoutDefaults) (string, error) {
	if defaults.OS == "linux" && input.EUID != 0 && input.XDGState != "" {
		if strings.IndexByte(input.XDGState, 0) >= 0 {
			return "", fmt.Errorf("invalid argument: XDG state path contains NUL")
		}
		if targetIsAbs(defaults.OS, input.XDGState) {
			return targetJoin(defaults.OS, targetClean(defaults.OS, input.XDGState), "mihari"), nil
		}
	}
	home, err := absoluteLayoutPath(defaults.OS, "trusted home", defaults.TrustedHome, "")
	if err != nil {
		return "", err
	}
	if defaults.OS == "linux" {
		return targetJoin(defaults.OS, home, ".local", "state", "mihari"), nil
	}
	return targetJoin(defaults.OS, home, "Library", "Logs", "mihari"), nil
}

func absoluteLayoutPath(osName, name, value, cwd string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("invalid argument: %s is empty", name)
	}
	if strings.IndexByte(value, 0) >= 0 {
		return "", fmt.Errorf("invalid argument: %s contains NUL", name)
	}
	if !targetIsAbs(osName, value) {
		if cwd == "" || strings.IndexByte(cwd, 0) >= 0 || !targetIsAbs(osName, cwd) {
			return "", fmt.Errorf("invalid argument: relative %s requires an absolute initial working directory", name)
		}
		value = targetJoin(osName, cwd, value)
	}
	value = targetClean(osName, value)
	if !targetIsAbs(osName, value) {
		return "", fmt.Errorf("invalid argument: %s is not absolute", name)
	}
	return value, nil
}

func pathsOverlap(osName, left, right string) bool {
	left = targetClean(osName, left)
	right = targetClean(osName, right)
	if osName == "windows" {
		left = strings.ToLower(left)
		right = strings.ToLower(right)
	}
	if left == right {
		return true
	}
	separator := "/"
	if osName == "windows" {
		separator = `\`
	}
	return strings.HasPrefix(left, strings.TrimRight(right, separator)+separator) ||
		strings.HasPrefix(right, strings.TrimRight(left, separator)+separator)
}

func targetJoin(osName string, elements ...string) string {
	if osName != "windows" {
		return pathpkg.Join(elements...)
	}
	joined := ""
	for _, element := range elements {
		if element == "" {
			continue
		}
		if joined == "" || targetIsAbs("windows", element) {
			joined = element
			continue
		}
		joined = strings.TrimRight(joined, `/\`) + `\` + strings.TrimLeft(element, `/\`)
	}
	return targetClean("windows", joined)
}

func targetClean(osName, value string) string {
	if osName != "windows" {
		return pathpkg.Clean(value)
	}
	value = strings.ReplaceAll(value, "/", `\`)
	if strings.HasPrefix(value, `\\.\`) || strings.HasPrefix(value, `\\?\`) {
		prefix := value[:4]
		tail := cleanWindowsTail(value[4:])
		return prefix + tail
	}
	if strings.HasPrefix(value, `\\`) {
		return `\\` + cleanWindowsTail(strings.TrimLeft(value, `\`))
	}
	wasDriveAbsolute := len(value) >= 3 && value[1] == ':' && value[2] == '\\'
	cleaned := strings.ReplaceAll(pathpkg.Clean(strings.ReplaceAll(value, `\`, "/")), "/", `\`)
	if wasDriveAbsolute && len(cleaned) == 2 && cleaned[1] == ':' {
		return cleaned + `\`
	}
	return cleaned
}

func cleanWindowsTail(value string) string {
	cleaned := pathpkg.Clean(strings.ReplaceAll(value, `\`, "/"))
	if cleaned == "." {
		return ""
	}
	return strings.ReplaceAll(cleaned, "/", `\`)
}

func targetIsAbs(osName, value string) bool {
	if osName != "windows" {
		return pathpkg.IsAbs(value)
	}
	value = strings.ReplaceAll(value, "/", `\`)
	if strings.HasPrefix(value, `\\`) {
		return true
	}
	return len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && value[2] == '\\'
}

func newTargetPaths(osName, root string) Paths {
	if osName == runtime.GOOS {
		return NewPaths(root)
	}
	coreName := "mihomo"
	if osName == "windows" {
		coreName += ".exe"
	}
	return Paths{
		Root:                root,
		ControlToken:        targetJoin(osName, root, "control.token"),
		Bin:                 targetJoin(osName, root, "bin"),
		CoreBinary:          targetJoin(osName, root, "bin", coreName),
		RuntimeConfig:       targetJoin(osName, root, "runtime", "config.yaml"),
		Settings:            targetJoin(osName, root, "mihari.yaml"),
		Onboarding:          targetJoin(osName, root, "onboarding.json"),
		LogDir:              targetJoin(osName, root, "logs"),
		DaemonLog:           targetJoin(osName, root, "logs", "mihari-daemon.log"),
		TUILog:              targetJoin(osName, root, "logs", "mihari-tui.log"),
		MihomoLog:           targetJoin(osName, root, "logs", "mihomo.log"),
		LogExportDir:        targetJoin(osName, root, "logs-export"),
		Staging:             targetJoin(osName, root, "staging"),
		Subscriptions:       targetJoin(osName, root, "subscriptions"),
		SubscriptionCatalog: targetJoin(osName, root, "subscriptions", "catalog.yaml"),
		SubscriptionCache:   targetJoin(osName, root, "subscriptions", "cache"),
		SubscriptionStaging: targetJoin(osName, root, "staging", "subscriptions"),
		TUIPreferences:      targetJoin(osName, root, "preferences", "tui.json"),
		GeoIPCountry:        targetJoin(osName, root, "geoip", "GeoLite2-Country.mmdb"),
		GeoIPASN:            targetJoin(osName, root, "geoip", "GeoLite2-ASN.mmdb"),
		GeoIPStaging:        targetJoin(osName, root, "staging", "geoip"),
		WebRoot:             targetJoin(osName, root, "web"),
		WebActive:           targetJoin(osName, root, "web", "active.json"),
		WebCredential:       targetJoin(osName, root, "web", "credential"),
		PanelStaging:        targetJoin(osName, root, "staging", "panels"),
	}
}
