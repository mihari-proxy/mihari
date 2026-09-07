package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

const (
	InstallJournalSchema      = "mihari.install-transaction/v1"
	MaxInstallJournalBytes    = 1 << 20
	installJournalFileName    = "install-transaction.json"
	installJournalFileMode    = uint32(0600)
	installTransactionDirMode = uint32(0700)

	InstallPhasePrepared            = "prepared"
	InstallPhaseStopped             = "stopped"
	InstallPhaseDataCommitted       = "data_committed"
	InstallPhaseBinariesCommitted   = "binaries_committed"
	InstallPhaseDefinitionCommitted = "definition_committed"
	InstallPhaseActivationCommitted = "activation_committed"
	InstallPhaseComplete            = "complete"

	InstallDataCreate = "create"
	InstallDataRetain = "retain"

	InstallAuthoritySource = "source"
	InstallAuthorityTarget = "target"

	JournalActionIntent = "intent"
	JournalActionDone   = "done"

	JournalActionMask                 = "mask"
	JournalActionUnmask               = "unmask"
	JournalActionDisable              = "disable"
	JournalActionEnable               = "enable"
	JournalActionStop                 = "stop"
	JournalActionStart                = "start"
	JournalActionDataPublish          = "data_publish"
	JournalActionDataIsolate          = "data_isolate"
	JournalActionManagedBinary        = "managed_binary"
	JournalActionPathBinary           = "path_binary"
	JournalActionChannel              = "channel"
	JournalActionDefinition           = "definition"
	JournalActionDropin               = "dropin"
	JournalActionDisabled             = "disabled"
	JournalActionReload               = "reload"
	JournalActionValidationStart      = "validation_start"
	JournalActionValidationStop       = "validation_stop"
	JournalActionActivation           = "activation"
	JournalActionRestoreMask          = "restore_mask"
	JournalActionRestoreUnmask        = "restore_unmask"
	JournalActionRestoreDisable       = "restore_disable"
	JournalActionRestoreEnable        = "restore_enable"
	JournalActionRestoreStop          = "restore_stop"
	JournalActionRestoreStart         = "restore_start"
	JournalActionRestoreData          = "restore_data"
	JournalActionRestoreManagedBinary = "restore_managed_binary"
	JournalActionRestorePathBinary    = "restore_path_binary"
	JournalActionRestoreChannel       = "restore_channel"
	JournalActionRestoreDefinition    = "restore_definition"
	JournalActionRestoreDropin        = "restore_dropin"
	JournalActionRestoreDisabled      = "restore_disabled"
	JournalActionRestoreReload        = "restore_reload"
	JournalActionRestoreValidation    = "restore_validation"

	JournalRoleSource        = "source"
	JournalRoleTarget        = "target"
	JournalRoleData          = "data"
	JournalRoleInstall       = "install"
	JournalRoleEndpoint      = "endpoint"
	JournalRoleCredential    = "credential"
	JournalRoleChannel       = "channel"
	JournalRoleManagedBinary = "managed_binary"
	JournalRolePathBinary    = "path_binary"
	JournalRoleDefinition    = "definition"
	JournalRoleDropin        = "dropin"
	JournalRoleValidation    = "validation"
	JournalRoleActivation    = "activation"
)

// JournalObject is a trusted observation, not authority to skip re-resolution.
type JournalObject struct {
	Present  bool   `json:"present"`
	SHA256   string `json:"sha256"`
	Dev      string `json:"dev"`
	Ino      string `json:"ino"`
	MountID  string `json:"mount_id"`
	BootID   string `json:"boot_id"`
	Marker   string `json:"marker"`
	Identity string `json:"identity"`
}

// ServiceBackup records original service definition bytes by hash and private ref.
type ServiceBackup struct {
	Ref      string `json:"ref"`
	SHA256   string `json:"sha256"`
	Identity string `json:"identity"`
}

// JournalAction is one external or reverse-recovery step with intent/done.
type JournalAction struct {
	Seq          int    `json:"seq"`
	Kind         string `json:"kind"`
	TargetRole   string `json:"target_role"`
	OldState     string `json:"old_state"`
	BackupRef    string `json:"backup_ref"`
	NewState     string `json:"new_state"`
	CandidateRef string `json:"candidate_ref"`
	Status       string `json:"status"`
}

// InstallJournal is the R4 §10 write-ahead transaction document.
type InstallJournal struct {
	Schema             string          `json:"schema"`
	TransactionID      string          `json:"transaction_id"`
	Operation          string          `json:"operation"`
	Phase              string          `json:"phase"`
	Mode               string          `json:"mode"`
	SourcePath         string          `json:"source_path"`
	TargetPath         string          `json:"target_path"`
	InstallPath        string          `json:"install_path"`
	EndpointPath       string          `json:"endpoint_path"`
	CredentialPath     string          `json:"credential_path"`
	DataRoot           string          `json:"data_root"`
	BootID             string          `json:"boot_id"`
	SourceIdentity     JournalObject   `json:"source_identity"`
	TargetIdentity     JournalObject   `json:"target_identity"`
	InstallIdentity    JournalObject   `json:"install_identity"`
	EndpointIdentity   JournalObject   `json:"endpoint_identity"`
	CredentialIdentity JournalObject   `json:"credential_identity"`
	CandidateHash      string          `json:"candidate_hash"`
	BackupHash         string          `json:"backup_hash"`
	DataAction         string          `json:"data_action"`
	ServiceBackup      ServiceBackup   `json:"service_backup"`
	OldRunning         bool            `json:"old_running"`
	OldEnabled         bool            `json:"old_enabled"`
	RecoveryAuthority  string          `json:"recovery_authority"`
	CreatedAt          time.Time       `json:"created_at"`
	TransactionMarker  JournalObject   `json:"transaction_marker"`
	Actions            []JournalAction `json:"actions"`
}

// JournalDurability reports whether a journal write became visible.
// Published means rename completed; Durable means parent fsync completed.
// A parent-sync error may still have Published=true.
type JournalDurability struct {
	Published bool
	Durable   bool
	Object    JournalObject
}

// ActionProgress is derived from recorded actions and observed state, never phase.
type ActionProgress int

const (
	ActionNotStarted ActionProgress = iota
	ActionStarted
)

type journalFiles interface {
	inspect(context.Context, string) (JournalObject, error)
	read(context.Context, string, int64) ([]byte, error)
	write(context.Context, string, []byte, JournalObject) (JournalDurability, error)
}

// InstallJournalStore persists the install WAL through a trusted publisher.
type InstallJournalStore struct {
	files journalFiles
}

// DecodeJournal strictly decodes a bounded install journal.
func DecodeJournal(reader io.Reader) (InstallJournal, error) {
	var journal InstallJournal
	if _, err := decodeStrictJSON(reader, MaxInstallJournalBytes, &journal); err != nil {
		return InstallJournal{}, invalidInstallJournal()
	}
	if err := validateInstallJournal(journal); err != nil {
		return InstallJournal{}, err
	}
	return journal, nil
}

// EncodeJournal validates and marshals a journal. Env values are never encoded.
func EncodeJournal(journal InstallJournal) ([]byte, error) {
	if err := validateInstallJournal(journal); err != nil {
		return nil, err
	}
	if journal.Actions == nil {
		journal.Actions = []JournalAction{}
	}
	return json.Marshal(journal)
}

// Load reads the published journal without using caller-supplied paths as authority.
func (s *InstallJournalStore) Load(ctx context.Context) (InstallJournal, error) {
	if s == nil || s.files == nil {
		return InstallJournal{}, invalidInstallJournal()
	}
	raw, err := s.files.read(ctx, installJournalFileName, MaxInstallJournalBytes)
	if err != nil {
		return InstallJournal{}, err
	}
	journal, err := DecodeJournal(bytes.NewReader(raw))
	if err != nil {
		return InstallJournal{}, err
	}
	marker, err := s.files.inspect(ctx, transactionMarkerPath(journal.TransactionID))
	if err != nil {
		return InstallJournal{}, err
	}
	if err := MatchJournalObject(journal.TransactionMarker, marker); err != nil {
		return InstallJournal{}, err
	}
	return journal, nil
}

// CreateTransactionMarker publishes transactions/<id>/transaction-id before
// the first recoverable Load. The marker bytes are the transaction id.
func (s *InstallJournalStore) CreateTransactionMarker(ctx context.Context, id string) (JournalObject, error) {
	if s == nil || s.files == nil || !validTransactionID(id) {
		return JournalObject{}, invalidInstallJournal()
	}
	path := transactionMarkerPath(id)
	existing, err := s.files.inspect(ctx, path)
	if err != nil {
		return JournalObject{}, err
	}
	if existing.Present {
		if existing.Marker != id || existing.SHA256 != sha256Hex(id) {
			return JournalObject{}, unknownInstallState()
		}
		return existing, nil
	}
	durability, err := s.files.write(ctx, path, []byte(id), JournalObject{})
	object := durability.Object
	if object.Present {
		object.Marker = id
		if object.SHA256 == "" {
			object.SHA256 = sha256Hex(id)
		}
	}
	if err != nil && !durability.Published {
		return JournalObject{}, err
	}
	if !object.Present {
		return JournalObject{}, invalidInstallJournal()
	}
	return object, err
}

// Save publishes a validated journal. Callers must inspect JournalDurability
// when the error is non-nil; publication may already have occurred.
func (s *InstallJournalStore) Save(ctx context.Context, journal InstallJournal) (JournalDurability, error) {
	if s == nil || s.files == nil {
		return JournalDurability{}, invalidInstallJournal()
	}
	raw, err := EncodeJournal(journal)
	if err != nil {
		return JournalDurability{}, err
	}
	old, err := s.files.inspect(ctx, installJournalFileName)
	if err != nil {
		return JournalDurability{}, err
	}
	return s.files.write(ctx, installJournalFileName, raw, old)
}

// RecordAction appends or completes one action and attempts a durable save.
func (s *InstallJournalStore) RecordAction(ctx context.Context, journal *InstallJournal, action JournalAction) (JournalDurability, error) {
	if s == nil || journal == nil {
		return JournalDurability{}, invalidInstallJournal()
	}
	next := *journal
	next.Actions = append([]JournalAction(nil), journal.Actions...)
	switch action.Status {
	case JournalActionIntent:
		action.Seq = len(next.Actions) + 1
		if err := validateJournalAction(action); err != nil {
			return JournalDurability{}, err
		}
		next.Actions = append(next.Actions, action)
	case JournalActionDone:
		updated := false
		for i, existing := range next.Actions {
			if existing.Seq != action.Seq || existing.Kind != action.Kind || existing.TargetRole != action.TargetRole {
				continue
			}
			if existing.Status != JournalActionIntent {
				return JournalDurability{}, invalidInstallJournal()
			}
			if action.OldState == "" {
				action.OldState = existing.OldState
			}
			if action.NewState == "" {
				action.NewState = existing.NewState
			}
			if action.BackupRef == "" {
				action.BackupRef = existing.BackupRef
			}
			if action.CandidateRef == "" {
				action.CandidateRef = existing.CandidateRef
			}
			if err := validateJournalAction(action); err != nil {
				return JournalDurability{}, err
			}
			next.Actions[i] = action
			updated = true
			break
		}
		if !updated {
			return JournalDurability{}, invalidInstallJournal()
		}
	default:
		return JournalDurability{}, invalidInstallJournal()
	}
	durability, err := s.Save(ctx, next)
	if durability.Published {
		*journal = next
	}
	return durability, err
}

// IncompleteActions returns actions that are not done. Phase is ignored.
func IncompleteActions(journal InstallJournal) []JournalAction {
	var pending []JournalAction
	for _, action := range journal.Actions {
		if action.Status != JournalActionDone {
			pending = append(pending, action)
		}
	}
	return pending
}

// ClassifyObservedAction compares actual state to the action's old/new values.
func ClassifyObservedAction(action JournalAction, actualState string) (ActionProgress, error) {
	if actualState == action.OldState {
		return ActionNotStarted, nil
	}
	if actualState == action.NewState {
		return ActionStarted, nil
	}
	return 0, unknownInstallState()
}

// MatchJournalObject re-checks hash and private marker. Same-boot compares
// dev/ino/mount; cross-boot does not require mount ID equality.
func MatchJournalObject(recorded, actual JournalObject) error {
	if !recorded.Present && !actual.Present {
		return nil
	}
	if !recorded.Present || !actual.Present {
		return unknownInstallState()
	}
	if recorded.SHA256 != actual.SHA256 || recorded.Marker != actual.Marker {
		return unknownInstallState()
	}
	if recorded.BootID != actual.BootID {
		return nil
	}
	if recorded.Dev != actual.Dev || recorded.Ino != actual.Ino || recorded.MountID != actual.MountID || recorded.Identity != actual.Identity {
		return unknownInstallState()
	}
	return nil
}

// ResolveObject re-opens a trusted object and matches it against a recording.
func (s *InstallJournalStore) ResolveObject(ctx context.Context, name string, recorded JournalObject) (JournalObject, error) {
	if s == nil || s.files == nil {
		return JournalObject{}, invalidInstallJournal()
	}
	actual, err := s.files.inspect(ctx, name)
	if err != nil {
		return JournalObject{}, err
	}
	if err := MatchJournalObject(recorded, actual); err != nil {
		return actual, err
	}
	return actual, nil
}

func validateInstallJournal(journal InstallJournal) error {
	if journal.Schema != InstallJournalSchema || !validTransactionID(journal.TransactionID) || journal.BootID == "" || journal.CreatedAt.IsZero() {
		return invalidInstallJournal()
	}
	switch journal.Operation {
	case InstallOperationInstall, InstallOperationReinstall, InstallOperationUpdate, InstallOperationRecover:
	default:
		return invalidInstallJournal()
	}
	switch journal.Phase {
	case InstallPhasePrepared, InstallPhaseStopped, InstallPhaseDataCommitted, InstallPhaseBinariesCommitted, InstallPhaseDefinitionCommitted, InstallPhaseActivationCommitted, InstallPhaseComplete:
	default:
		return invalidInstallJournal()
	}
	if journal.Mode != InstallLayoutSystem && journal.Mode != InstallLayoutPrivate {
		return invalidInstallJournal()
	}
	if journal.DataAction != InstallDataCreate && journal.DataAction != InstallDataRetain {
		return invalidInstallJournal()
	}
	if journal.RecoveryAuthority != InstallAuthoritySource && journal.RecoveryAuthority != InstallAuthorityTarget {
		return invalidInstallJournal()
	}
	for _, path := range []string{journal.TargetPath, journal.InstallPath, journal.EndpointPath, journal.CredentialPath, journal.DataRoot} {
		if !validAbsPath(path) {
			return invalidInstallJournal()
		}
	}
	if journal.SourcePath != "" && !validAbsPath(journal.SourcePath) {
		return invalidInstallJournal()
	}
	if err := validateJournalObject(journal.SourceIdentity, journal.SourcePath != ""); err != nil {
		return err
	}
	if err := validateJournalObject(journal.TargetIdentity, false); err != nil {
		return err
	}
	if err := validateJournalObject(journal.InstallIdentity, false); err != nil {
		return err
	}
	if err := validateJournalObject(journal.EndpointIdentity, false); err != nil {
		return err
	}
	if err := validateJournalObject(journal.CredentialIdentity, false); err != nil {
		return err
	}
	if !validSHA256(journal.CandidateHash) || !validSHA256(journal.BackupHash) {
		return invalidInstallJournal()
	}
	if journal.ServiceBackup.Ref == "" || strings.Contains(strings.ToLower(journal.ServiceBackup.Ref), "env") || !validSHA256(journal.ServiceBackup.SHA256) || journal.ServiceBackup.Identity == "" {
		return invalidInstallJournal()
	}
	if err := validateJournalObject(journal.TransactionMarker, true); err != nil {
		return err
	}
	if journal.TransactionMarker.Marker != journal.TransactionID || journal.TransactionMarker.SHA256 != sha256Hex(journal.TransactionID) {
		return invalidInstallJournal()
	}
	if journal.Actions == nil {
		journal.Actions = []JournalAction{}
	}
	for i, action := range journal.Actions {
		if action.Seq != i+1 {
			return invalidInstallJournal()
		}
		if err := validateJournalAction(action); err != nil {
			return err
		}
	}
	return nil
}

func validateJournalObject(object JournalObject, required bool) error {
	if !object.Present {
		if object != (JournalObject{}) {
			return invalidInstallJournal()
		}
		if required {
			return invalidInstallJournal()
		}
		return nil
	}
	if !validSHA256(object.SHA256) || object.Dev == "" || object.Ino == "" || object.MountID == "" || object.BootID == "" || object.Marker == "" || object.Identity == "" {
		return invalidInstallJournal()
	}
	return nil
}

func validateJournalAction(action JournalAction) error {
	if action.Seq < 1 || action.OldState == "" || action.NewState == "" || action.OldState == action.NewState {
		return invalidInstallJournal()
	}
	if action.Status != JournalActionIntent && action.Status != JournalActionDone {
		return invalidInstallJournal()
	}
	if !validJournalActionKind(action.Kind) || !validJournalRole(action.TargetRole) {
		return invalidInstallJournal()
	}
	return nil
}

func validJournalActionKind(kind string) bool {
	switch kind {
	case JournalActionMask, JournalActionUnmask, JournalActionDisable, JournalActionEnable, JournalActionStop, JournalActionStart,
		JournalActionDataPublish, JournalActionDataIsolate, JournalActionManagedBinary, JournalActionPathBinary, JournalActionChannel,
		JournalActionDefinition, JournalActionDropin, JournalActionDisabled, JournalActionReload, JournalActionValidationStart, JournalActionValidationStop,
		JournalActionActivation, JournalActionRestoreMask, JournalActionRestoreUnmask, JournalActionRestoreDisable, JournalActionRestoreEnable,
		JournalActionRestoreStop, JournalActionRestoreStart, JournalActionRestoreData, JournalActionRestoreManagedBinary, JournalActionRestorePathBinary,
		JournalActionRestoreChannel, JournalActionRestoreDefinition, JournalActionRestoreDropin, JournalActionRestoreDisabled, JournalActionRestoreReload, JournalActionRestoreValidation:
		return true
	default:
		return false
	}
}

func validJournalRole(role string) bool {
	switch role {
	case JournalRoleSource, JournalRoleTarget, JournalRoleData, JournalRoleInstall, JournalRoleEndpoint, JournalRoleCredential,
		JournalRoleChannel, JournalRoleManagedBinary, JournalRolePathBinary, JournalRoleDefinition, JournalRoleDropin, JournalRoleValidation, JournalRoleActivation:
		return true
	default:
		return false
	}
}

func sha256Hex(value string) string {
	return sha256HexBytes([]byte(value))
}

func sha256HexBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func transactionMarkerPath(id string) string {
	return "transactions/" + id + "/transaction-id"
}

func invalidInstallJournal() error {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid install journal"}
}

func unknownInstallState() error {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: "install recovery identity is unknown"}
}
