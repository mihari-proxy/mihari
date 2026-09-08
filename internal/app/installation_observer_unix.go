//go:build linux || darwin

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
)

// UnixInstallationServiceObserver is the read-only part of a native Unix
// service adapter used by installation status inspection.
type UnixInstallationServiceObserver interface {
	InspectDefinition(context.Context) (service.Definition, error)
}

// UnixInstallationReadyProbe checks the verified definition's local control
// endpoint and credential path. An unavailable daemon is a not-ready
// observation, never installation recovery.
type UnixInstallationReadyProbe func(ctx context.Context, endpoint, credentialPath string) (bool, error)

// UnixInstallationObserverOptions supplies read-only OS service observations.
type UnixInstallationObserverOptions struct {
	Service UnixInstallationServiceObserver
	Ready   UnixInstallationReadyProbe
}

type unixInstallationControl interface {
	ReadState(context.Context) ([]byte, string, error)
	ProbeOperation(context.Context) (bool, error)
	Close() error
}

type unixInstallationResources interface {
	Verify(context.Context, InstallationManifest, service.Definition) (bool, error)
	VerifiedLegacy(context.Context) (*InstallationManifest, error)
}

type unixInstallationObserverDeps struct {
	openControl func(context.Context) (unixInstallationControl, error)
	service     UnixInstallationServiceObserver
	ready       UnixInstallationReadyProbe
	readyScope  func(service.Definition) (string, string, bool)
	resources   unixInstallationResources
}

type unixInstallationObserver struct{ deps unixInstallationObserverDeps }

// NewUnixInstallationObserver constructs a fixed-global-K read-only observer.
func NewUnixInstallationObserver(options UnixInstallationObserverOptions) InstallationObserver {
	return newUnixInstallationObserver(unixInstallationObserverDeps{
		openControl: func(ctx context.Context) (unixInstallationControl, error) {
			return platform.OpenInstallControlReadOnly(ctx)
		},
		service: options.Service,
		ready:   options.Ready,
		readyScope: func(definition service.Definition) (string, string, bool) {
			return verifiedUnixDefinitionControlScope(definition, runtime.GOOS, platform.SystemLayoutDefaults())
		},
		resources: nativeUnixInstallationResources{
			goos:     runtime.GOOS,
			defaults: platform.SystemLayoutDefaults(),
			boot:     installBootIdentity,
			owner:    0,
		},
	})
}

func newUnixInstallationObserver(deps unixInstallationObserverDeps) InstallationObserver {
	if deps.readyScope == nil {
		deps.readyScope = unixDefinitionControlScope
	}
	return &unixInstallationObserver{deps: deps}
}

func (o *unixInstallationObserver) Snapshot(ctx context.Context) (snapshot InstallationSnapshot, returnErr error) {
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	if o == nil || o.deps.openControl == nil {
		return snapshot, ErrInstallationObservationUnknown
	}
	control, err := o.deps.openControl(ctx)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return snapshot, ErrInstallationPermissionRequired
		}
		if errors.Is(err, os.ErrNotExist) {
			return o.snapshotLegacy(ctx)
		}
		return snapshot, unixInstallationObservationError(err)
	}
	if control == nil {
		return snapshot, ErrInstallationObservationUnknown
	}
	defer func() {
		if err := control.Close(); err != nil {
			snapshot = InstallationSnapshot{}
			returnErr = unixInstallationObservationError(errors.Join(returnErr, err))
		}
	}()
	locked, err := control.ProbeOperation(ctx)
	if err != nil {
		return snapshot, unixInstallationObservationError(err)
	}
	state, digest, err := control.ReadState(ctx)
	if err != nil {
		return snapshot, unixInstallationObservationError(err)
	}
	if digest != installationSHA256(state) {
		return snapshot, ErrInstallationObservationUnknown
	}
	return InstallationSnapshot{Present: true, OperationLocked: locked, State: append([]byte(nil), state...)}, nil
}

func (o *unixInstallationObserver) snapshotLegacy(ctx context.Context) (InstallationSnapshot, error) {
	if o.deps.resources == nil {
		return InstallationSnapshot{}, nil
	}
	legacy, err := o.deps.resources.VerifiedLegacy(ctx)
	if err != nil {
		return InstallationSnapshot{}, unixInstallationObservationError(err)
	}
	if legacy == nil {
		return InstallationSnapshot{}, nil
	}
	if err := validateInstallationManifest(*legacy, false); err != nil || !legacy.Installed {
		return InstallationSnapshot{}, ErrInstallationObservationUnknown
	}
	return InstallationSnapshot{Legacy: cloneInstallationManifestPointer(legacy)}, nil
}

func (o *unixInstallationObserver) VerifyManifest(ctx context.Context, manifest InstallationManifest) (InstallationResourceObservation, error) {
	if err := ctx.Err(); err != nil {
		return InstallationResourceObservation{}, err
	}
	if o == nil || o.deps.service == nil || o.deps.resources == nil || validateInstallationManifest(manifest, false) != nil {
		return InstallationResourceObservation{}, ErrInstallationObservationUnknown
	}
	definition, err := o.deps.service.InspectDefinition(ctx)
	if err != nil {
		return InstallationResourceObservation{}, unixInstallationObservationError(err)
	}
	matches, err := o.deps.resources.Verify(ctx, cloneInstallationManifest(manifest), definition)
	if err != nil {
		return InstallationResourceObservation{}, unixInstallationObservationError(err)
	}
	return InstallationResourceObservation{Matches: matches}, nil
}

func (o *unixInstallationObserver) ObserveService(ctx context.Context) (InstallationServiceObservation, error) {
	if err := ctx.Err(); err != nil {
		return InstallationServiceObservation{}, err
	}
	if o == nil || o.deps.service == nil {
		return InstallationServiceObservation{}, ErrInstallationObservationUnknown
	}
	definition, err := o.deps.service.InspectDefinition(ctx)
	if err != nil {
		return InstallationServiceObservation{}, unixInstallationObservationError(err)
	}
	observation := InstallationServiceObservation{State: unixInstallationServiceState(definition.Status), Enabled: definition.Enabled}
	if observation.State != InstallServiceRunning || o.deps.ready == nil {
		return observation, nil
	}
	endpoint, credential, ok := o.deps.readyScope(definition)
	if !ok {
		return observation, nil
	}
	ready, err := o.deps.ready(ctx, endpoint, credential)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return InstallationServiceObservation{}, err
		}
		return observation, nil
	}
	observation.Ready = ready
	return observation, nil
}

func unixInstallationObservationError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, ErrInstallationPermissionRequired):
		return ErrInstallationPermissionRequired
	case errors.Is(err, ErrInstallationObservationUnknown):
		return ErrInstallationObservationUnknown
	case errors.Is(err, os.ErrPermission):
		return ErrInstallationPermissionRequired
	default:
		return ErrInstallationObservationUnknown
	}
}

func unixInstallationServiceState(status service.StatusKind) string {
	switch status {
	case service.StatusRunning:
		return InstallServiceRunning
	case service.StatusStopped:
		return InstallServiceStopped
	case service.StatusNotInstalled:
		return InstallServiceNotInstalled
	default:
		return InstallServiceUnknown
	}
}

func unixDefinitionControlScope(definition service.Definition) (endpoint, credential string, ok bool) {
	for _, item := range definition.Env {
		key, value, present := strings.Cut(item, "=")
		if !present || value == "" {
			continue
		}
		switch key {
		case "MIHARI_CONTROL_ENDPOINT":
			if endpoint != "" {
				return "", "", false
			}
			endpoint = value
		case "MIHARI_CONTROL_CREDENTIAL":
			if credential != "" {
				return "", "", false
			}
			credential = value
		}
	}
	return endpoint, credential, endpoint != "" && credential != ""
}

func verifiedUnixDefinitionControlScope(definition service.Definition, goos string, defaults platform.LayoutDefaults) (endpoint, credential string, ok bool) {
	values := make(map[string]string, len(definition.Env))
	for _, item := range definition.Env {
		key, value, present := strings.Cut(item, "=")
		if !present || value == "" {
			return "", "", false
		}
		switch key {
		case "MIHARI_CONTROL_ENDPOINT", "MIHARI_CONTROL_CREDENTIAL", "MIHARI_INSTALL_ROOT", "MIHARI_DATA":
		default:
			return "", "", false
		}
		if values[key] != "" {
			return "", "", false
		}
		values[key] = value
	}
	input := platform.LayoutInput{
		CWD:         "/",
		Data:        values["MIHARI_DATA"],
		Endpoint:    values["MIHARI_CONTROL_ENDPOINT"],
		Credential:  values["MIHARI_CONTROL_CREDENTIAL"],
		InstallRoot: values["MIHARI_INSTALL_ROOT"],
		EUID:        0,
	}
	if input.Endpoint == "" || input.Credential == "" || input.InstallRoot == "" || defaults.OS != goos {
		return "", "", false
	}
	layout, err := platform.ResolveLayout(input, defaults)
	if err != nil {
		return "", "", false
	}
	expected, err := service.BuildUnixDefinition(layout, goos)
	if err != nil || !unixInstallationStaticDefinitionMatches(expected, definition) {
		return "", "", false
	}
	return layout.ControlEndpoint, layout.CredentialPath, true
}

type nativeUnixInstallationResources struct {
	goos     string
	defaults platform.LayoutDefaults
	boot     func() (string, error)
	owner    uint32
}

func (nativeUnixInstallationResources) VerifiedLegacy(context.Context) (*InstallationManifest, error) {
	return nil, nil
}

func (r nativeUnixInstallationResources) Verify(ctx context.Context, manifest InstallationManifest, actual service.Definition) (matches bool, returnErr error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if r.goos != "linux" && r.goos != "darwin" || r.boot == nil || manifest.Binary == nil || manifest.DataIdentity == nil {
		return false, ErrInstallationObservationUnknown
	}
	layout, err := resolveUnixInstallationManifestLayout(manifest, r.defaults, r.goos)
	if err != nil {
		return false, nil
	}
	expected, err := service.BuildUnixDefinition(layout, r.goos)
	if err != nil {
		return false, ErrInstallationObservationUnknown
	}
	definitionSHA256, err := unixInstallationDefinitionSHA256(expected)
	if err != nil {
		return false, ErrInstallationObservationUnknown
	}
	if definitionSHA256 != manifest.DefinitionSHA256 || !unixInstallationStaticDefinitionMatches(expected, actual) {
		return false, nil
	}

	binarySHA256, err := trustedUnixInstallationBinaryHash(ctx, manifest.Binary.Path, r.owner)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if binarySHA256 != manifest.Binary.SHA256 {
		return false, nil
	}

	data, err := platform.OpenTrustedRoot(ctx, layout.Data.Root, platform.RootPolicy{Owner: r.owner, Mode: 0700})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer func() { returnErr = errors.Join(returnErr, data.Close()) }()
	_, dataIdentity, _, _, err := data.Snapshot(ctx)
	if err != nil {
		return false, err
	}
	locks, err := data.OpenDir(ctx, "locks", platform.RootPolicy{Owner: r.owner, Mode: 0700})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer func() { returnErr = errors.Join(returnErr, locks.Close()) }()
	marker, _, err := locks.OpenFile(ctx, "install-data-id", 0600)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	markerRaw, readErr := readInstallFile(ctx, marker, 128)
	err = errors.Join(readErr, marker.Close())
	if err != nil {
		return false, err
	}
	if sha256HexBytes(markerRaw) != manifest.DataIdentity.Marker {
		return false, nil
	}
	bootID, err := r.boot()
	if err != nil {
		return false, err
	}
	if bootID == manifest.DataIdentity.BootID && dataIdentity != manifest.DataIdentity.Key {
		return false, nil
	}

	locator, err := layout.Locator(r.owner)
	if err != nil {
		return false, ErrInstallationObservationUnknown
	}
	credential, err := platform.ReadControlCredential(ctx, locator)
	defer func() {
		for i := range credential {
			credential[i] = 0
		}
	}()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, platform.ErrControlData) {
			return false, nil
		}
		return false, err
	}
	if !validUnixInstallationCredential(credential) {
		return false, nil
	}
	return true, nil
}

func trustedUnixInstallationBinaryHash(ctx context.Context, path string, owner uint32) (hashValue string, returnErr error) {
	if owner == 0 {
		return trustedValidationBinaryHash(ctx, path)
	}
	root, err := platform.OpenTrustedRoot(ctx, filepath.Dir(path), platform.RootPolicy{Owner: owner, Mode: 0755})
	if err != nil {
		return "", err
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	file, _, err := root.OpenFile(ctx, filepath.Base(path), 0755)
	if err != nil {
		return "", err
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, migrationBinaryMax+1))
	if err != nil {
		return "", err
	}
	if n > migrationBinaryMax {
		return "", ErrInstallationObservationUnknown
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func resolveUnixInstallationManifestLayout(manifest InstallationManifest, defaults platform.LayoutDefaults, goos string) (platform.ResolvedLayout, error) {
	if defaults.OS != goos || manifest.DataRoot == "" || manifest.InstallRoot == "" || manifest.Endpoint == "" || manifest.Credential == "" {
		return platform.ResolvedLayout{}, os.ErrInvalid
	}
	input := platform.LayoutInput{
		CWD:         "/",
		Endpoint:    manifest.Endpoint,
		Credential:  manifest.Credential,
		InstallRoot: manifest.InstallRoot,
		EUID:        0,
	}
	if filepath.Clean(manifest.DataRoot) != filepath.Join(defaults.BaseDir, "data") {
		input.Data = manifest.DataRoot
	}
	layout, err := platform.ResolveLayout(input, defaults)
	if err != nil {
		return platform.ResolvedLayout{}, err
	}
	if layout.Data.Root != manifest.DataRoot || layout.InstallRoot != manifest.InstallRoot || layout.ControlEndpoint != manifest.Endpoint || layout.CredentialPath != manifest.Credential || manifest.Binary == nil || manifest.Binary.Path != filepath.Join(layout.InstallRoot, "mihari") {
		return platform.ResolvedLayout{}, os.ErrInvalid
	}
	return layout, nil
}

const unixInstallationDefinitionSchema = "mihari.unix-definition/v1"

type unixInstallationDefinitionDocument struct {
	Schema  string                                   `json:"schema"`
	Account string                                   `json:"account"`
	Binary  string                                   `json:"binary"`
	Args    []string                                 `json:"args"`
	Env     []string                                 `json:"env"`
	Files   []unixInstallationDefinitionFileDocument `json:"files"`
}

type unixInstallationDefinitionFileDocument struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Owner  uint32 `json:"owner"`
	Mode   uint32 `json:"mode"`
	SHA256 string `json:"sha256"`
}

func unixInstallationDefinitionSHA256(definition service.Definition) (string, error) {
	if definition.Binary == "" || len(definition.Args) == 0 || len(definition.Files) == 0 {
		return "", os.ErrInvalid
	}
	document := unixInstallationDefinitionDocument{
		Schema:  unixInstallationDefinitionSchema,
		Account: "root",
		Binary:  definition.Binary,
		Args:    append([]string(nil), definition.Args...),
		Env:     append([]string(nil), definition.Env...),
		Files:   make([]unixInstallationDefinitionFileDocument, 0, len(definition.Files)),
	}
	sort.Strings(document.Env)
	for _, file := range definition.Files {
		if file.Path == "" || file.Kind == "" {
			return "", os.ErrInvalid
		}
		document.Files = append(document.Files, unixInstallationDefinitionFileDocument{
			Path: file.Path, Kind: file.Kind, Owner: file.Owner, Mode: file.Mode, SHA256: sha256HexBytes(file.Bytes),
		})
	}
	sort.Slice(document.Files, func(i, j int) bool { return document.Files[i].Path < document.Files[j].Path })
	raw, err := json.Marshal(document)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func unixInstallationStaticDefinitionMatches(expected, actual service.Definition) bool {
	if expected.Binary != actual.Binary || !reflect.DeepEqual(expected.Args, actual.Args) || len(expected.Env) != len(actual.Env) || len(expected.Files) != len(actual.Files) {
		return false
	}
	expectedEnv := append([]string(nil), expected.Env...)
	actualEnv := append([]string(nil), actual.Env...)
	sort.Strings(expectedEnv)
	sort.Strings(actualEnv)
	if !reflect.DeepEqual(expectedEnv, actualEnv) {
		return false
	}
	expectedFiles := append([]service.DefinitionFile(nil), expected.Files...)
	actualFiles := append([]service.DefinitionFile(nil), actual.Files...)
	sort.Slice(expectedFiles, func(i, j int) bool { return expectedFiles[i].Path < expectedFiles[j].Path })
	sort.Slice(actualFiles, func(i, j int) bool { return actualFiles[i].Path < actualFiles[j].Path })
	for i := range expectedFiles {
		left, right := expectedFiles[i], actualFiles[i]
		if left.Path != right.Path || left.Kind != right.Kind || left.Owner != right.Owner || left.Mode != right.Mode || !bytes.Equal(left.Bytes, right.Bytes) {
			return false
		}
	}
	return true
}

func validUnixInstallationCredential(raw []byte) bool {
	if len(raw) == 65 && raw[64] == '\n' {
		raw = raw[:64]
	}
	if len(raw) != 64 {
		return false
	}
	var decoded [32]byte
	_, err := hex.Decode(decoded[:], raw)
	return err == nil
}
