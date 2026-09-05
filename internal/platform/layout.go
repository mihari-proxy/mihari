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
	ClientLogs      Paths // zero value means diagnostics must remain in memory
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
		clientRoot = systemClientRoot(input, defaults)
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
	clientLogs := Paths{}
	if clientRoot != "" {
		clientLogs = newTargetPaths(defaults.OS, clientRoot)
	}
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

func systemClientRoot(input LayoutInput, defaults LayoutDefaults) string {
	if defaults.OS == "linux" && input.EUID != 0 && input.XDGState != "" {
		if strings.IndexByte(input.XDGState, 0) < 0 && targetIsAbs(defaults.OS, input.XDGState) {
			return targetJoin(defaults.OS, targetClean(defaults.OS, input.XDGState), "mihari")
		}
	}
	if defaults.TrustedHome == "" || strings.IndexByte(defaults.TrustedHome, 0) >= 0 || !targetIsAbs(defaults.OS, defaults.TrustedHome) {
		return ""
	}
	home := targetClean(defaults.OS, defaults.TrustedHome)
	if defaults.OS == "linux" {
		return targetJoin(defaults.OS, home, ".local", "state", "mihari")
	}
	return targetJoin(defaults.OS, home, "Library", "Logs", "mihari")
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
	if root, tail, ok := splitWindowsRoot(value); ok {
		parts := make([]string, 0, strings.Count(tail, `\`)+1)
		for _, part := range strings.Split(tail, `\`) {
			switch part {
			case "", ".":
				continue
			case "..":
				if len(parts) > 0 {
					parts = parts[:len(parts)-1]
				}
			default:
				parts = append(parts, part)
			}
		}
		if len(parts) == 0 {
			return root + `\`
		}
		return root + `\` + strings.Join(parts, `\`)
	}
	return strings.ReplaceAll(pathpkg.Clean(strings.ReplaceAll(value, `\`, "/")), "/", `\`)
}

func splitWindowsRoot(value string) (root, tail string, ok bool) {
	if len(value) >= 3 && isASCIILetter(value[0]) && value[1] == ':' && value[2] == '\\' {
		return value[:2], strings.TrimLeft(value[3:], `\`), true
	}
	if !strings.HasPrefix(value, `\\`) {
		return "", "", false
	}
	rest := strings.TrimLeft(value, `\`)
	serverEnd := strings.IndexByte(rest, '\\')
	if serverEnd <= 0 {
		return "", "", false
	}
	server := rest[:serverEnd]
	rest = strings.TrimLeft(rest[serverEnd+1:], `\`)
	if rest == "" {
		return "", "", false
	}
	shareEnd := strings.IndexByte(rest, '\\')
	share := rest
	if shareEnd >= 0 {
		share = rest[:shareEnd]
		tail = strings.TrimLeft(rest[shareEnd+1:], `\`)
	}
	if share == "" {
		return "", "", false
	}
	return `\\` + server + `\` + share, tail, true
}

func targetIsAbs(osName, value string) bool {
	if osName != "windows" {
		return pathpkg.IsAbs(value)
	}
	value = strings.ReplaceAll(value, "/", `\`)
	_, _, ok := splitWindowsRoot(value)
	return ok
}

func isASCIILetter(value byte) bool {
	return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z')
}

func newTargetPaths(osName, root string) Paths {
	if osName == runtime.GOOS {
		return NewPaths(root)
	}
	coreName := "mihomo"
	if osName == "windows" {
		coreName += ".exe"
	}
	return buildPaths(root, func(elements ...string) string {
		return targetJoin(osName, elements...)
	}, coreName)
}
