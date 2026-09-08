package app

import (
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

const (
	InstallationStateSchema   = "mihari.install-state/v1"
	InstallationStatusSchema  = "mihari.install-status/v1"
	InstallationPlanSchema    = "mihari.install-plan/v1"
	InstallationOutcomeSchema = "mihari.install-outcome/v1"

	MaxInstallationStateBytes = 64 << 10
	MaxInstallationPlanBytes  = 64 << 10

	InstallationKindNotInstalled       = "not_installed"
	InstallationKindInProgress         = "in_progress"
	InstallationKindInterrupted        = "interrupted"
	InstallationKindInstalled          = "installed"
	InstallationKindUnknown            = "unknown"
	InstallationKindPermissionRequired = "permission_required"

	InstallationStateApplying = "applying"
	InstallationStateComplete = "complete"

	InstallationModeRepair = "repair"
	InstallationModeFresh  = "fresh"

	InstallServiceUnknown = "unknown"

	InstallationOperationInstall   = "install"
	InstallationOperationUpdate    = "update"
	InstallationOperationRepair    = "repair"
	InstallationOperationFresh     = "fresh"
	InstallationOperationUninstall = "uninstall"
	InstallationOperationMigrate   = "migrate"

	InstallationDataRetain = "retain"
	InstallationDataReset  = "reset"

	InstallationReasonInstalling            = "installing"
	InstallationReasonOperationInterrupted  = "operation_interrupted"
	InstallationReasonRecordInvalid         = "record_invalid"
	InstallationReasonResourceMismatch      = "resource_mismatch"
	InstallationReasonPermissionRequired    = "permission_required"
	InstallationReasonServicePolicyMismatch = "service_policy_mismatch"
	InstallationReasonServiceStartFailed    = "service_start_failed"
	InstallationReasonServiceNotReady       = "service_not_ready"
	InstallationReasonLegacyRecordAbsent    = "legacy_record_absent"
	InstallationReasonMigrationIncomplete   = "migration_incomplete"
	InstallationReasonProcessTreeUnproven   = "process_tree_unproven"

	InstallationCategoryConfig        = "config"
	InstallationCategorySubscriptions = "subscriptions"
	InstallationCategoryPreferences   = "preferences"
	InstallationCategoryLogs          = "logs"
	InstallationCategoryRuntime       = "runtime"
	InstallationCategoryBinaries      = "binaries"
	InstallationCategoryAssets        = "assets"
	InstallationCategoryWeb           = "web"
	InstallationCategoryStaging       = "staging"
	InstallationCategoryCredential    = "credential"
	InstallationCategoryUnknown       = "unknown"
)

// InstallationIdentity identifies a protected installation resource.
type InstallationIdentity struct {
	BootID string `json:"boot_id"`
	Key    string `json:"key"`
	Marker string `json:"marker"`
}

// InstallationDataParentIdentity binds a not-yet-created data root to a trusted parent.
type InstallationDataParentIdentity struct {
	Path     string               `json:"path"`
	Identity InstallationIdentity `json:"identity"`
	Relative string               `json:"relative"`
}

// InstallationBinary identifies the managed executable.
type InstallationBinary struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Version string `json:"version"`
}

// InstallationManifest describes one complete installation target.
type InstallationManifest struct {
	Installed          bool                            `json:"installed"`
	DataRoot           string                          `json:"data_root"`
	InstallRoot        string                          `json:"install_root"`
	Endpoint           string                          `json:"endpoint"`
	Credential         string                          `json:"credential"`
	DataIdentity       *InstallationIdentity           `json:"data_identity"`
	DataParentIdentity *InstallationDataParentIdentity `json:"data_parent_identity"`
	Binary             *InstallationBinary             `json:"binary"`
	DefinitionSHA256   string                          `json:"definition_sha256"`
	Enabled            bool                            `json:"enabled"`
	RunAfterInstall    bool                            `json:"run_after_install"`
}

// InstallationOwner identifies the process publishing an applying state.
type InstallationOwner struct {
	BootID string `json:"boot_id"`
	PID    int64  `json:"pid"`
	Start  string `json:"start"`
}

// InstallationStateRef binds a replacement state to its predecessor.
type InstallationStateRef struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}

// InstallationSourceScope records a protected migration source.
type InstallationSourceScope struct {
	DataRoot       string               `json:"data_root"`
	DataIdentity   InstallationIdentity `json:"data_identity"`
	Selection      string               `json:"selection"`
	EvidenceSHA256 string               `json:"evidence_sha256"`
}

// InstallationState is the private installation-control state document.
type InstallationState struct {
	Schema       string                   `json:"schema"`
	ID           string                   `json:"id"`
	State        string                   `json:"state"`
	Operation    string                   `json:"operation"`
	Owner        *InstallationOwner       `json:"owner"`
	Base         *InstallationManifest    `json:"base"`
	Target       InstallationManifest     `json:"target"`
	DataPolicy   string                   `json:"data_policy"`
	ResetEntries []string                 `json:"reset_entries"`
	Supersedes   *InstallationStateRef    `json:"supersedes"`
	SourceScope  *InstallationSourceScope `json:"source_scope"`
}

// InstallationStatus is the redacted read-only status contract.
type InstallationStatus struct {
	Schema       string `json:"schema"`
	Kind         string `json:"kind"`
	ServiceState string `json:"service_state"`
	StartFailed  bool   `json:"start_failed"`
	Reason       string `json:"reason"`
	ID           string `json:"id"`
}

// DecodeInstallationState strictly decodes a bounded state document.
func DecodeInstallationState(reader io.Reader) (InstallationState, error) {
	var state InstallationState
	if _, err := decodeInstallationJSON(reader, MaxInstallationStateBytes, &state); err != nil {
		return InstallationState{}, invalidInstallationState()
	}
	if err := validateInstallationState(state); err != nil {
		return InstallationState{}, err
	}
	return state, nil
}

// EncodeInstallationState validates and encodes a state document.
func EncodeInstallationState(state InstallationState) ([]byte, error) {
	if err := validateInstallationState(state); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(state)
	if err != nil || len(raw) > MaxInstallationStateBytes {
		return nil, invalidInstallationState()
	}
	return raw, nil
}

func validateInstallationState(state InstallationState) error {
	if state.Schema != InstallationStateSchema || !validTransactionID(state.ID) {
		return invalidInstallationState()
	}
	switch state.Operation {
	case InstallationOperationInstall, InstallationOperationUpdate, InstallationOperationRepair, InstallationOperationFresh, InstallationOperationUninstall, InstallationOperationMigrate:
	default:
		return invalidInstallationState()
	}
	switch state.State {
	case InstallationStateApplying:
		if state.Owner == nil || !validInstallationOwner(*state.Owner) {
			return invalidInstallationState()
		}
	case InstallationStateComplete:
		if state.Owner != nil {
			return invalidInstallationState()
		}
	default:
		return invalidInstallationState()
	}
	if state.Base != nil {
		if err := validateInstallationManifest(*state.Base, false); err != nil {
			return err
		}
	}
	allowMissingRoot := state.State == InstallationStateApplying && (state.Base == nil || emptyInstallationManifest(*state.Base))
	if err := validateInstallationManifest(state.Target, allowMissingRoot); err != nil {
		return err
	}
	if state.Operation == InstallationOperationUninstall {
		if state.Target.Installed {
			return invalidInstallationState()
		}
	} else if !state.Target.Installed {
		return invalidInstallationState()
	}
	if state.ResetEntries == nil || len(state.ResetEntries) > 32 {
		return invalidInstallationState()
	}
	if state.Operation == InstallationOperationFresh {
		if state.DataPolicy != InstallationDataReset {
			return invalidInstallationState()
		}
	} else if state.DataPolicy != InstallationDataRetain {
		return invalidInstallationState()
	}
	switch state.DataPolicy {
	case InstallationDataRetain:
		if len(state.ResetEntries) != 0 {
			return invalidInstallationState()
		}
	case InstallationDataReset:
		if state.Operation != InstallationOperationFresh || len(state.ResetEntries) == 0 {
			return invalidInstallationState()
		}
	default:
		return invalidInstallationState()
	}
	seenEntries := map[string]bool{}
	for _, entry := range state.ResetEntries {
		if !validResetEntry(entry) || seenEntries[entry] {
			return invalidInstallationState()
		}
		seenEntries[entry] = true
	}
	if state.Supersedes != nil && (!validTransactionID(state.Supersedes.ID) || !validSHA256(state.Supersedes.SHA256)) {
		return invalidInstallationState()
	}
	if state.SourceScope != nil && !validInstallationSourceScope(*state.SourceScope) {
		return invalidInstallationState()
	}
	return nil
}

func emptyInstallationManifest(manifest InstallationManifest) bool {
	return !manifest.Installed && manifest.DataRoot == "" && manifest.InstallRoot == "" && manifest.Endpoint == "" && manifest.Credential == "" && manifest.DataIdentity == nil && manifest.DataParentIdentity == nil && manifest.Binary == nil && manifest.DefinitionSHA256 == "" && !manifest.Enabled && !manifest.RunAfterInstall
}

func validateInstallationManifest(manifest InstallationManifest, allowMissingRoot bool) error {
	for _, path := range []string{manifest.DataRoot, manifest.InstallRoot, manifest.Credential} {
		if path != "" && (!validAbsPath(path) || !validInstallationPath(path)) {
			return invalidInstallationState()
		}
	}
	if manifest.Endpoint != "" && (!validAbsPath(manifest.Endpoint) || !validInstallationPath(manifest.Endpoint)) {
		return invalidInstallationState()
	}
	if manifest.DataRoot == "" {
		if manifest.DataIdentity != nil || manifest.DataParentIdentity != nil {
			return invalidInstallationState()
		}
	} else {
		hasRoot := manifest.DataIdentity != nil
		hasParent := manifest.DataParentIdentity != nil
		if hasRoot == hasParent || hasParent && !allowMissingRoot {
			return invalidInstallationState()
		}
		if hasRoot && !validInstallationIdentity(*manifest.DataIdentity) {
			return invalidInstallationState()
		}
		if hasParent && !validDataParentIdentity(*manifest.DataParentIdentity) {
			return invalidInstallationState()
		}
		if hasParent && filepath.Clean(filepath.Join(manifest.DataParentIdentity.Path, manifest.DataParentIdentity.Relative)) != filepath.Clean(manifest.DataRoot) {
			return invalidInstallationState()
		}
	}
	if manifest.Installed {
		if manifest.DataRoot == "" || manifest.InstallRoot == "" || manifest.Endpoint == "" || manifest.Credential == "" || manifest.Binary == nil || !validInstallationBinary(*manifest.Binary) || !validSHA256(manifest.DefinitionSHA256) {
			return invalidInstallationState()
		}
		return nil
	}
	if manifest.Binary != nil || manifest.DefinitionSHA256 != "" {
		return invalidInstallationState()
	}
	nonEmptyPaths := 0
	for _, path := range []string{manifest.DataRoot, manifest.InstallRoot, manifest.Endpoint, manifest.Credential} {
		if path != "" {
			nonEmptyPaths++
		}
	}
	if nonEmptyPaths != 0 && nonEmptyPaths != 4 {
		return invalidInstallationState()
	}
	return nil
}

func validInstallationOwner(owner InstallationOwner) bool {
	return owner.PID > 1 && validInstallationText(owner.BootID, 128, true) && validInstallationText(owner.Start, 128, false)
}

func validInstallationIdentity(identity InstallationIdentity) bool {
	return validInstallationText(identity.BootID, 128, false) && validInstallationText(identity.Key, 512, false) && validInstallationText(identity.Marker, 512, false)
}

func validDataParentIdentity(parent InstallationDataParentIdentity) bool {
	return validAbsPath(parent.Path) && validInstallationPath(parent.Path) && validInstallationIdentity(parent.Identity) && parent.Relative != "" && len(parent.Relative) <= 255 && parent.Relative != "." && parent.Relative != ".." && !strings.ContainsAny(parent.Relative, `/\\`) && !strings.ContainsRune(parent.Relative, 0)
}

func validInstallationBinary(binary InstallationBinary) bool {
	return validAbsPath(binary.Path) && validInstallationPath(binary.Path) && validSHA256(binary.SHA256) && validInstallationText(binary.Version, 128, false)
}

func validInstallationSourceScope(scope InstallationSourceScope) bool {
	if !validAbsPath(scope.DataRoot) || !validInstallationPath(scope.DataRoot) || !validInstallationIdentity(scope.DataIdentity) || !validSHA256(scope.EvidenceSHA256) {
		return false
	}
	switch scope.Selection {
	case "service_definition", "explicit_request", "legacy_v1":
		return true
	default:
		return false
	}
}

func validInstallationPath(path string) bool {
	return len(path) <= 4096 && utf8.ValidString(path) && !strings.ContainsRune(path, 0)
}

func validInstallationText(value string, max int, allowEmpty bool) bool {
	return (allowEmpty || value != "") && len(value) <= max && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func validResetEntry(entry string) bool {
	switch entry {
	case "mihari.yaml", "onboarding.json", "subscriptions", "preferences", "runtime", "logs", "logs-export", "bin", "geoip", "web", "staging":
		return true
	default:
		return false
	}
}

func invalidInstallationState() error {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid installation state"}
}
