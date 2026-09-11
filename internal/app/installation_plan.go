package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// InstallationPlanRequest selects a repair or fresh installation preview.
type InstallationPlanRequest struct {
	Mode   string
	Binary string
	Enable *bool
	Start  *bool
}

// InstallationExecuteRequest submits a previously bound plan.
type InstallationExecuteRequest struct {
	Plan           InstallationPlan
	ResetConfirmed bool
}

// InstallationEntry is one preserved or reset-managed path.
type InstallationEntry struct {
	Path     string `json:"path"`
	Category string `json:"category"`
}

// InstallationPlanInstance is the verified instance scope bound by a plan digest.
type InstallationPlanInstance struct {
	RecordID           string                          `json:"record_id"`
	RecordSHA256       string                          `json:"record_sha256"`
	DataRoot           string                          `json:"data_root"`
	DataIdentity       *InstallationIdentity           `json:"data_identity"`
	DataParentIdentity *InstallationDataParentIdentity `json:"data_parent_identity"`
	SourceScope        *InstallationSourceScope        `json:"source_scope"`
	Enabled            bool                            `json:"enabled"`
	RunAfterInstall    bool                            `json:"run_after_install"`
}

// InstallationPlan is the stable preview and confirmation contract.
type InstallationPlan struct {
	Schema          string                   `json:"schema"`
	Mode            string                   `json:"mode"`
	Instance        InstallationPlanInstance `json:"instance"`
	CandidateSHA256 string                   `json:"candidate_sha256"`
	Preserve        []InstallationEntry      `json:"preserve"`
	Delete          []InstallationEntry      `json:"delete"`
	PlanSHA256      string                   `json:"plan_sha256"`
}

// InstallationOutcome is emitted only after a successful installation command.
type InstallationOutcome struct {
	Schema               string `json:"schema"`
	InstallationComplete bool   `json:"installation_complete"`
	ServiceState         string `json:"service_state"`
	StartFailed          bool   `json:"start_failed"`
	ID                   string `json:"id"`
}

// VerifiedInstallationPlanInput contains already-normalized, capability-verified plan facts.
type VerifiedInstallationPlanInput struct {
	Mode            string
	Instance        InstallationPlanInstance
	CandidateSHA256 string
	Preserve        []InstallationEntry
	Delete          []InstallationEntry
}

// BindInstallationPlan binds verified plan facts to a deterministic digest.
func BindInstallationPlan(input VerifiedInstallationPlanInput) (InstallationPlan, error) {
	if err := validateInstallationPlanInput(input); err != nil {
		return InstallationPlan{}, err
	}
	plan := InstallationPlan{
		Schema:          InstallationPlanSchema,
		Mode:            input.Mode,
		Instance:        cloneInstallationPlanInstance(input.Instance),
		CandidateSHA256: input.CandidateSHA256,
		Preserve:        cloneInstallationEntries(input.Preserve),
		Delete:          cloneInstallationEntries(input.Delete),
	}
	digest, err := installationPlanDigest(plan)
	if err != nil {
		return InstallationPlan{}, err
	}
	plan.PlanSHA256 = digest
	if err := validateInstallationPlanWireSize(plan); err != nil {
		return InstallationPlan{}, err
	}
	return plan, nil
}

// VerifyInstallationPlan verifies a plan's fields and digest.
func VerifyInstallationPlan(plan InstallationPlan) error {
	if plan.Schema != InstallationPlanSchema || !validSHA256(plan.PlanSHA256) {
		return invalidInstallationPlan()
	}
	if err := validateInstallationPlanWireSize(plan); err != nil {
		return err
	}
	if err := validateInstallationPlanInput(VerifiedInstallationPlanInput{
		Mode:            plan.Mode,
		Instance:        plan.Instance,
		CandidateSHA256: plan.CandidateSHA256,
		Preserve:        plan.Preserve,
		Delete:          plan.Delete,
	}); err != nil {
		return err
	}
	digest, err := installationPlanDigest(plan)
	if err != nil || digest != plan.PlanSHA256 {
		return invalidInstallationPlan()
	}
	return nil
}

func validateInstallationPlanWireSize(plan InstallationPlan) error {
	raw, err := json.Marshal(plan)
	if err != nil || len(raw) > MaxInstallationPlanBytes {
		return invalidInstallationPlan()
	}
	return nil
}

type installationPlanDigestDocument struct {
	Schema          string                   `json:"schema"`
	Mode            string                   `json:"mode"`
	Instance        InstallationPlanInstance `json:"instance"`
	CandidateSHA256 string                   `json:"candidate_sha256"`
	Preserve        []InstallationEntry      `json:"preserve"`
	Delete          []InstallationEntry      `json:"delete"`
}

func installationPlanDigest(plan InstallationPlan) (string, error) {
	raw, err := json.Marshal(installationPlanDigestDocument{
		Schema:          plan.Schema,
		Mode:            plan.Mode,
		Instance:        plan.Instance,
		CandidateSHA256: plan.CandidateSHA256,
		Preserve:        plan.Preserve,
		Delete:          plan.Delete,
	})
	if err != nil {
		return "", invalidInstallationPlan()
	}
	if len(raw) > MaxInstallationPlanBytes {
		return "", invalidInstallationPlan()
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func validateInstallationPlanInput(input VerifiedInstallationPlanInput) error {
	if input.Mode != InstallationModeRepair && input.Mode != InstallationModeFresh {
		return invalidInstallationPlan()
	}
	if !validSHA256(input.CandidateSHA256) || !validInstallationPlanInstance(input.Instance) {
		return invalidInstallationPlan()
	}
	if input.Preserve == nil || input.Delete == nil || len(input.Delete) > 32 {
		return invalidInstallationPlan()
	}
	if input.Mode == InstallationModeRepair && len(input.Delete) != 0 {
		return invalidInstallationPlan()
	}
	seen := map[string]bool{}
	for _, entries := range [][]InstallationEntry{input.Preserve, input.Delete} {
		for _, entry := range entries {
			key := installationPathKey(entry.Path)
			if !validInstallationEntry(entry) || seen[key] {
				return invalidInstallationPlan()
			}
			seen[key] = true
		}
	}
	return nil
}

func validInstallationPlanInstance(instance InstallationPlanInstance) bool {
	if (instance.RecordID == "") != (instance.RecordSHA256 == "") {
		return false
	}
	if instance.RecordID != "" && (!validTransactionID(instance.RecordID) || !validSHA256(instance.RecordSHA256)) {
		return false
	}
	if !validAbsPath(instance.DataRoot) || !validInstallationPath(instance.DataRoot) {
		return false
	}
	hasRoot := instance.DataIdentity != nil
	hasParent := instance.DataParentIdentity != nil
	if hasRoot == hasParent {
		return false
	}
	if hasRoot && !validInstallationIdentity(*instance.DataIdentity) {
		return false
	}
	if hasParent {
		parent := instance.DataParentIdentity
		if !validDataParentIdentity(*parent) || filepath.Clean(filepath.Join(parent.Path, parent.Relative)) != filepath.Clean(instance.DataRoot) {
			return false
		}
	}
	return instance.SourceScope == nil || validInstallationSourceScope(*instance.SourceScope)
}

func validInstallationEntry(entry InstallationEntry) bool {
	if !validAbsPath(entry.Path) || !validInstallationPath(entry.Path) {
		return false
	}
	switch entry.Category {
	case InstallationCategoryConfig, InstallationCategorySubscriptions, InstallationCategoryPreferences, InstallationCategoryLogs, InstallationCategoryRuntime, InstallationCategoryBinaries, InstallationCategoryAssets, InstallationCategoryWeb, InstallationCategoryStaging, InstallationCategoryCredential, InstallationCategoryUnknown:
		return true
	default:
		return false
	}
}

func cloneInstallationPlanInstance(instance InstallationPlanInstance) InstallationPlanInstance {
	clone := instance
	if instance.DataIdentity != nil {
		identity := *instance.DataIdentity
		clone.DataIdentity = &identity
	}
	if instance.DataParentIdentity != nil {
		parent := *instance.DataParentIdentity
		clone.DataParentIdentity = &parent
	}
	if instance.SourceScope != nil {
		scope := *instance.SourceScope
		clone.SourceScope = &scope
	}
	return clone
}

func cloneInstallationEntries(entries []InstallationEntry) []InstallationEntry {
	clone := make([]InstallationEntry, len(entries))
	copy(clone, entries)
	return clone
}

func invalidInstallationPlan() error {
	return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid installation plan"}
}
