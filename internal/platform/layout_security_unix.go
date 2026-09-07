//go:build unix_security && (linux || darwin)

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"syscall"
)

// This supplier is compiled only into isolated native-security test binaries.
// No environment or inherited-descriptor override exists in ordinary builds.
var securityDefaultsOnce sync.Once
var securityDefaults LayoutDefaults
var securityDefaultsError error
var securityValidationFixture string

type securityIdentity struct {
	Dev  uint64 `json:"dev"`
	Ino  uint64 `json:"ino"`
	UID  uint32 `json:"uid"`
	Mode uint32 `json:"mode"`
}
type securityRun struct {
	RunID           string           `json:"run_id"`
	Root            string           `json:"root"`
	Results         string           `json:"results"`
	RootIdentity    securityIdentity `json:"root_identity"`
	ResultsIdentity securityIdentity `json:"results_identity"`
	UIDs            []uint32         `json:"uids"`
	Accounts        []struct {
		Name string `json:"name"`
		UID  uint32 `json:"uid"`
		GID  uint32 `json:"gid"`
	} `json:"accounts"`
}
type securityMarker struct {
	Schema string      `json:"schema"`
	Role   string      `json:"role"`
	RunID  string      `json:"run_id"`
	Run    securityRun `json:"run"`
}

func platformLayoutDefaults(home string) LayoutDefaults {
	if os.Getenv("MIHARI_UNIX_SECURITY_CHILD") != "1" && os.Getenv("MIHARI_UNIX_SECURITY_VALIDATION") != "1" && os.Getenv("MIHARI_ISOLATED_SECURITY_CI") != "1" {
		return nativeLayoutDefaults(home)
	}
	securityDefaultsOnce.Do(func() { securityDefaults, securityDefaultsError = loadSecurityDefaults() })
	if securityDefaultsError != nil {
		panic("invalid isolated security defaults: " + securityDefaultsError.Error())
	}
	result := securityDefaults
	result.TrustedHome = home
	return result
}

func decodeSecurityJSON(reader io.Reader, destination any) error {
	raw, err := io.ReadAll(io.LimitReader(reader, 65537))
	if err != nil {
		return err
	}
	if len(raw) > 65536 {
		return os.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return os.ErrInvalid
	}
	return nil
}

func loadSecurityDefaults() (LayoutDefaults, error) {
	defaults := nativeLayoutDefaults("")
	if os.Getenv("MIHARI_UNIX_SECURITY_CHILD") == "1" || os.Getenv("MIHARI_UNIX_SECURITY_VALIDATION") == "1" {
		descriptor := uintptr(3)
		if os.Getenv("MIHARI_UNIX_SECURITY_VALIDATION") == "1" {
			if os.Geteuid() != 0 || os.Getenv("MIHARI_UNIX_SECURITY_CHILD") == "1" {
				return defaults, os.ErrPermission
			}
			descriptor = 4
		}
		file := os.NewFile(descriptor, "parent-security-defaults")
		if file == nil {
			return defaults, os.ErrInvalid
		}
		info, err := file.Stat()
		if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
			_ = file.Close()
			return defaults, os.ErrPermission
		}
		var envelope struct {
			Schema            string         `json:"schema"`
			Defaults          LayoutDefaults `json:"defaults"`
			ValidationFixture string         `json:"validation_fixture,omitempty"`
		}
		decodeErr := decodeSecurityJSON(file, &envelope)
		if err := errors.Join(decodeErr, file.Close()); err != nil {
			return defaults, err
		}
		if envelope.Schema != "mihari.unix-security-defaults/v1" {
			return defaults, os.ErrPermission
		}
		if envelope.ValidationFixture != "" && (descriptor != 4 || envelope.ValidationFixture != "hold-after-eof") {
			return defaults, os.ErrPermission
		}
		securityValidationFixture = envelope.ValidationFixture
		defaults = envelope.Defaults
	} else {
		if os.Geteuid() != 0 || os.Getenv("CI") != "true" || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" {
			return defaults, os.ErrPermission
		}
		root, results := os.Getenv("MIHARI_SECURITY_ROOT"), os.Getenv("MIHARI_SECURITY_RESULTS")
		a, err := readSecurityMarker(root, "anchor")
		if err != nil {
			return defaults, err
		}
		b, err := readSecurityMarker(results, "results")
		if err != nil {
			return defaults, err
		}
		ar, _ := json.Marshal(a.Run)
		br, _ := json.Marshal(b.Run)
		if string(ar) != string(br) || a.RunID != b.RunID || a.Run.Root != root || a.Run.Results != results {
			return defaults, os.ErrPermission
		}
		resultPrefix := "/var/lib/mihari-security-results-"
		if runtime.GOOS == "darwin" {
			resultPrefix = "/Library/MihariSecurityResults-"
		}
		if results != resultPrefix+a.RunID || len(a.Run.UIDs) != 2 || a.Run.UIDs[0] == 0 || a.Run.UIDs[1] == 0 || a.Run.UIDs[0] == a.Run.UIDs[1] {
			return defaults, os.ErrPermission
		}
		if err := checkSecurityIdentity(root, a.Run.RootIdentity); err != nil {
			return defaults, err
		}
		if err := checkSecurityIdentity(results, a.Run.ResultsIdentity); err != nil {
			return defaults, err
		}
		defaults.BaseDir = filepath.Join(root, "system")
		defaults.InstallRoot = filepath.Join(root, "install")
	}
	anchor := filepath.Dir(defaults.BaseDir)
	prefix := "/var/lib/mihari-security-"
	if runtime.GOOS == "darwin" {
		prefix = "/Library/MihariSecurity-"
	}
	matched, _ := regexp.MatchString("^"+regexp.QuoteMeta(prefix)+"[a-f0-9]{12}$", anchor)
	native := nativeLayoutDefaults("")
	if !matched || defaults.OS != runtime.GOOS || defaults.BaseDir != filepath.Join(anchor, "system") || defaults.InstallRoot != filepath.Join(anchor, "install") || defaults.SocketLimit != native.SocketLimit || defaults.TrustedHome != "" {
		return defaults, os.ErrPermission
	}
	return defaults, nil
}

func checkSecurityIdentity(path string, want securityIdentity) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || uint64(st.Dev) != want.Dev || st.Ino != want.Ino || st.Uid != 0 || want.UID != 0 || uint32(info.Mode().Perm()) != want.Mode {
		return os.ErrPermission
	}
	return nil
}
func readSecurityMarker(path, role string) (result securityMarker, resultErr error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return result, os.ErrPermission
	}
	mode := uint32(0711)
	if role == "results" {
		mode = 0755
	}
	root, err := OpenTrustedRoot(context.Background(), path, RootPolicy{Owner: 0, Mode: mode})
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	file, _, err := root.OpenFile(context.Background(), ".mihari-security-owner.json", 0600)
	if err != nil {
		return result, err
	}
	decodeErr := decodeSecurityJSON(file, &result)
	if err := errors.Join(decodeErr, file.Close()); err != nil {
		return result, err
	}
	if result.Schema != "mihari.unix-security-owner/v1" || result.Role != role || result.RunID != result.Run.RunID {
		return result, os.ErrPermission
	}
	return result, nil
}

// SecurityValidationFixture returns the immutable, parent-provided validation
// callback fixture mode. It exists only in isolated security builds.
func SecurityValidationFixture() string {
	_ = SystemLayoutDefaults()
	return securityValidationFixture
}
