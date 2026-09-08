package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/service"
)

var errInstallCrash = errors.New("simulated install crash")

// InstallLease is a borrowed install lock. RecoverLocked, ApplyLocked and
// StartLocked never acquire or release it. Unix production passes a wrapper
// around *platform.OwnedInstallLease; tests use an in-memory fake so the
// scheduler can run on every GOOS.
type InstallLease interface {
	Validate(ctx context.Context) error
}

// InstallEffects applies and observes data, binary, channel and service tokens.
// Adapters must not call public service entrypoints from here.
type InstallEffects interface {
	Observe(ctx context.Context, action JournalAction) (string, error)
	Apply(ctx context.Context, action JournalAction) error
	SourcePresent() bool
	Object(role string) []byte
}

// InstallArtifacts is the prepared candidate/backup set consumed by ApplyLocked.
// T15 owns real migration; T14 tests inject reconstructable bytes.
type InstallArtifacts struct {
	DataAction                        string
	Source, Target, Install, Endpoint string
	Credential, DataRoot              string
	CandidateHash, BackupHash         string
	OldRunning, OldEnabled            bool
	TargetRunning, TargetEnabled      bool
	SourceBytes, DataOld, DataNew     []byte
	ManagedOld, ManagedNew            []byte
	PathOld, PathNew                  []byte
	ChannelOld, ChannelNew            []byte
	DefinitionOld, DefinitionNew      []byte
	BootID                            string
}

// InstallTransaction is the R4 §9/§10 install/recover scheduler.
type InstallTransaction struct {
	Store     *InstallJournalStore
	Service   service.DefinitionAdapter
	Effects   InstallEffects
	Acquire   func(context.Context) (InstallLease, error)
	Now       func() time.Time
	NewID     func() string
	Artifacts InstallArtifacts
	Private   bool
	// StartAfterInstall is the shell apply install policy; CLI install leaves false.
	StartAfterInstall  bool
	preparedAuthority  *installPreparedAuthority
	serviceEffects     *installServiceEffects
	Validation         ValidationChild
	DataLease          DataEndpointLease
	AfterDataCommitted func(context.Context) error
	BeforeRollback     func(context.Context) error
	EUID               uint32
	ParentIdentity     ProcessStartIdentity
	NewPipe            func() (parent, child ValidationLease)

	recoveringSource bool
	journal          InstallJournal
	crash            *installCrashSpec
	stepN            int
	prepared         *preparedMigration
	migrate          *migrationOptions
	validation       ValidationSession
	validationReady  ValidationReady
	validationNonce  []byte
}

type installPreparedAuthority struct {
	Source, Target, Install         JournalObject
	ServiceBackup                   ServiceBackup
	OldDefinition, TargetDefinition service.Definition
}

type installCrashSpec struct {
	index int
	point string
}

// Apply acquires the install lock once, recovers, then applies or returns the recovered result.
func (x *InstallTransaction) Apply(ctx context.Context, req InstallRequest) (InstallResult, error) {
	if x == nil || x.Acquire == nil {
		return InstallResult{}, installBusy("install lock is unavailable")
	}
	lease, err := x.Acquire(ctx)
	if err != nil {
		return InstallResult{}, err
	}
	defer closeInstallLease(lease)
	if err := x.RecoverLocked(ctx, lease); err != nil {
		return InstallResult{}, err
	}
	if req.Operation == InstallOperationRecover {
		return x.resultFromJournal(ctx)
	}
	return x.ApplyLocked(ctx, lease, req)
}

// ApplyLocked runs the install under a borrowed lease. It does not flock or Close the lease.
func (x *InstallTransaction) ApplyLocked(ctx context.Context, lease InstallLease, req InstallRequest) (InstallResult, error) {
	if err := ctx.Err(); err != nil {
		return InstallResult{}, err
	}
	if lease == nil {
		return InstallResult{}, installBusy("install lock is unavailable")
	}
	if err := lease.Validate(ctx); err != nil {
		return InstallResult{}, err
	}
	if req.Operation == InstallOperationUpdate && x.Service != nil {
		def, err := x.Service.InspectDefinition(ctx)
		if err != nil {
			return InstallResult{}, err
		}
		if def.Status == service.StatusNotInstalled {
			return InstallResult{}, installBusy("binary-only update is not a service transaction")
		}
	}
	if err := x.ensurePrepared(ctx, req); err != nil {
		return InstallResult{}, err
	}
	art := x.preparedArtifacts(req)
	id := x.newTransactionID()
	marker, err := x.Store.CreateTransactionMarker(ctx, id)
	if err != nil {
		return InstallResult{}, err
	}
	journal, err := x.buildJournal(req, id, marker, art)
	if err != nil {
		return InstallResult{}, err
	}
	if _, err := x.Store.Save(ctx, journal); err != nil {
		return InstallResult{}, err
	}
	x.journal = journal
	x.stepN = 0

	if x.Service != nil {
		if err := x.Service.DisableAutostartAndStop(ctx); err != nil {
			return InstallResult{}, err
		}
		if err := x.Service.WaitOwnedTreeExit(ctx); err != nil {
			return InstallResult{}, err
		}
	}
	if err := x.setPhase(ctx, InstallPhaseStopped); err != nil {
		return InstallResult{}, err
	}

	if art.DataAction == InstallDataCreate {
		if publisher, ok := x.Effects.(interface {
			PublishDataLocked(context.Context, *InstallTransaction) error
		}); ok {
			if err := publisher.PublishDataLocked(ctx, x); err != nil {
				return InstallResult{}, err
			}
		} else if err := x.fileStep(ctx, JournalActionDataPublish, JournalRoleData, "absent", fileHash(art.DataNew)); err != nil {
			return InstallResult{}, err
		}
	}
	if x.AfterDataCommitted != nil {
		if err := x.AfterDataCommitted(ctx); err != nil {
			return InstallResult{}, err
		}
	}
	if err := x.setPhase(ctx, InstallPhaseDataCommitted); err != nil {
		return InstallResult{}, err
	}
	if err := x.fileStep(ctx, JournalActionManagedBinary, JournalRoleManagedBinary, fileHash(art.ManagedOld), fileHash(art.ManagedNew)); err != nil {
		return InstallResult{}, err
	}
	if err := x.fileStep(ctx, JournalActionPathBinary, JournalRolePathBinary, fileHash(art.PathOld), fileHash(art.PathNew)); err != nil {
		return InstallResult{}, err
	}
	if err := x.fileStep(ctx, JournalActionChannel, JournalRoleChannel, fileHash(art.ChannelOld), fileHash(art.ChannelNew)); err != nil {
		return InstallResult{}, err
	}
	if err := x.setPhase(ctx, InstallPhaseBinariesCommitted); err != nil {
		return InstallResult{}, err
	}
	// Native definitions are already in the private, hash-bound unit metadata.
	// The persistent manager barrier remains until target authority is durable.
	if x.serviceEffects == nil {
		if err := x.fileStep(ctx, JournalActionDefinition, JournalRoleDefinition, "absent", fileHash(art.DefinitionNew)); err != nil {
			return InstallResult{}, err
		}
	}
	if err := x.reloadStep(ctx); err != nil {
		return InstallResult{}, err
	}
	if err := x.setPhase(ctx, InstallPhaseDefinitionCommitted); err != nil {
		return InstallResult{}, err
	}
	if err := x.runValidation(ctx); err != nil {
		return InstallResult{}, err
	}
	if err := x.activate(ctx); err != nil {
		return InstallResult{}, err
	}
	if x.Service != nil {
		if x.serviceEffects != nil && x.preparedAuthority != nil {
			if err := x.removeObsoleteDefinitions(ctx); err != nil {
				return InstallResult{}, err
			}
		}
		def := service.Definition{}
		if x.preparedAuthority != nil {
			def = x.preparedAuthority.TargetDefinition
		}
		def.Enabled, def.Running = art.TargetEnabled, art.TargetRunning
		if err := x.Service.WriteDefinition(ctx, def); err != nil {
			return InstallResult{}, err
		}
		if art.TargetRunning {
			if err := x.Service.Start(ctx); err != nil {
				return InstallResult{}, err
			}
		}
	}
	if err := x.setPhase(ctx, InstallPhaseComplete); err != nil {
		return InstallResult{}, err
	}
	return x.resultFromJournal(ctx)
}

// StartLocked recovers under the borrowed lease and starts the target service when allowed.
func (x *InstallTransaction) StartLocked(ctx context.Context, lease InstallLease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if lease == nil {
		return installBusy("install lock is unavailable")
	}
	if err := lease.Validate(ctx); err != nil {
		return err
	}
	if err := x.RecoverLocked(ctx, lease); err != nil {
		return err
	}
	if x.Service == nil {
		return nil
	}
	def, err := x.Service.InspectDefinition(ctx)
	if err != nil {
		return err
	}
	if def.Status == service.StatusNotInstalled {
		return installBusy("service is not installed")
	}
	if def.Running {
		return nil
	}
	return x.Service.Start(ctx)
}

func (x *InstallTransaction) journaledHook(ctx context.Context, def service.DefinitionAction, apply func(context.Context) error) error {
	action := JournalAction{Kind: def.Kind, TargetRole: def.TargetRole, OldState: def.OldState, NewState: def.NewState}
	if x.serviceEffects != nil && def.Kind != service.DefinitionActionReload {
		var err error
		action, err = x.serviceEffects.prepare(ctx, def)
		if err != nil {
			return err
		}
		if action.OldState == action.NewState {
			return nil
		}
	}
	if def.Kind == service.DefinitionActionReload {
		n := x.currentReload(ctx)
		action.OldState = reloadToken(n)
		action.NewState = reloadToken(n + 1)
	}
	if x.recoveringSource {
		if kind := reverseKind(action.Kind); kind != "" {
			action.Kind = kind
		}
	}
	return x.step(ctx, action, func(ctx context.Context) error {
		if x.serviceEffects != nil && (def.Kind == service.DefinitionActionStart) && x.DataLease != nil {
			if err := x.DataLease.Release(ctx); err != nil {
				return err
			}
		}
		if x.serviceEffects != nil && def.Path != "" && x.serviceEffects.files != nil {
			return x.serviceEffects.apply(ctx, action)
		}
		return apply(ctx)
	})
}

func (x *InstallTransaction) step(ctx context.Context, action JournalAction, apply func(context.Context) error) error {
	x.stepN++
	hit := x.crash != nil && x.crash.index == x.stepN
	if hit && x.crash.point == "before-intent" {
		panic(errInstallCrash)
	}
	action.Status = JournalActionIntent
	durability, err := x.Store.RecordAction(ctx, &x.journal, action)
	if err != nil {
		return err
	}
	if !durability.Durable {
		return installBusy("install action journal is not durable")
	}
	if hit && x.crash.point == "after-intent" {
		panic(errInstallCrash)
	}
	if apply != nil {
		if err := apply(ctx); err != nil {
			return err
		}
	}
	if hit && x.crash.point == "after-effect" {
		panic(errInstallCrash)
	}
	done := x.journal.Actions[len(x.journal.Actions)-1]
	done.Status = JournalActionDone
	durability, err = x.Store.RecordAction(ctx, &x.journal, done)
	if err != nil {
		return err
	}
	if !durability.Durable {
		return installBusy("install action journal is not durable")
	}
	if hit && x.crash.point == "after-done" {
		panic(errInstallCrash)
	}
	return nil
}

func (x *InstallTransaction) fileStep(ctx context.Context, kind, role, oldState, newState string) error {
	if oldState == newState {
		return nil
	}
	action := JournalAction{Kind: kind, TargetRole: role, OldState: oldState, NewState: newState}
	return x.step(ctx, action, func(ctx context.Context) error {
		if kind == JournalActionDataPublish && x.prepared != nil {
			if err := x.prepared.recheckAndPublish(ctx); err != nil {
				return err
			}
		}
		if x.Effects == nil {
			return nil
		}
		return x.Effects.Apply(ctx, action)
	})
}

func (x *InstallTransaction) reloadStep(ctx context.Context) error {
	n := x.currentReload(ctx)
	action := JournalAction{Kind: JournalActionReload, TargetRole: JournalRoleDefinition, OldState: reloadToken(n), NewState: reloadToken(n + 1)}
	return x.step(ctx, action, func(ctx context.Context) error {
		if x.Effects == nil {
			return nil
		}
		return x.Effects.Apply(ctx, action)
	})
}

func (x *InstallTransaction) activate(ctx context.Context) error {
	action := JournalAction{Kind: JournalActionActivation, TargetRole: JournalRoleActivation, OldState: InstallAuthoritySource, NewState: InstallAuthorityTarget}
	return x.step(ctx, action, func(ctx context.Context) error {
		x.journal.RecoveryAuthority = InstallAuthorityTarget
		x.journal.Phase = InstallPhaseActivationCommitted
		if _, err := x.Store.Save(ctx, x.journal); err != nil {
			return err
		}
		if x.Effects == nil {
			return nil
		}
		return x.Effects.Apply(ctx, action)
	})
}

func (x *InstallTransaction) setPhase(ctx context.Context, phase string) error {
	x.journal.Phase = phase
	_, err := x.Store.Save(ctx, x.journal)
	return err
}

func (x *InstallTransaction) currentReload(ctx context.Context) int {
	if x.Effects == nil {
		return 0
	}
	state, err := x.Effects.Observe(ctx, JournalAction{Kind: JournalActionReload, OldState: reloadToken(0), NewState: reloadToken(1)})
	if err != nil {
		return 0
	}
	var n int
	_, _ = fmt.Sscanf(state, "reload:%d", &n)
	return n
}

func (x *InstallTransaction) preparedArtifacts(req InstallRequest) InstallArtifacts {
	if x.prepared != nil {
		art := x.prepared.artifacts()
		if art.DataAction == "" {
			art.DataAction = InstallDataCreate
		}
		return art
	}
	art := x.Artifacts
	if art.DataAction == "" {
		art.DataAction = InstallDataCreate
	}
	switch req.Operation {
	case InstallOperationInstall:
		art.TargetEnabled = true
		art.TargetRunning = x.StartAfterInstall
	case InstallOperationReinstall:
		art.TargetEnabled = true
		art.TargetRunning = true
	case InstallOperationUpdate:
		if !art.TargetEnabled && !art.TargetRunning {
			art.TargetEnabled = art.OldEnabled
			art.TargetRunning = art.OldRunning
		}
	}
	if art.BootID == "" {
		art.BootID = "11111111-1111-1111-1111-111111111111"
	}
	if art.Target == "" {
		art.Target = "/var/lib/mihari/data"
	}
	if art.Install == "" {
		art.Install = "/usr/local/lib/mihari"
	}
	if art.Endpoint == "" {
		art.Endpoint = "/var/lib/mihari/control.sock"
	}
	if art.Credential == "" {
		art.Credential = "/var/lib/mihari/control.token"
	}
	if art.DataRoot == "" {
		art.DataRoot = art.Target
	}
	if art.CandidateHash == "" {
		art.CandidateHash = fileHash(art.DefinitionNew)
		if art.CandidateHash == "absent" {
			art.CandidateHash = sha256Hex("candidate")
		}
	}
	if art.BackupHash == "" {
		art.BackupHash = fileHash(art.DefinitionOld)
		if art.BackupHash == "absent" {
			art.BackupHash = sha256Hex("backup")
		}
	}
	return art
}

func (x *InstallTransaction) buildJournal(req InstallRequest, id string, marker JournalObject, art InstallArtifacts) (InstallJournal, error) {
	now := time.Now().UTC()
	if x.Now != nil {
		now = x.Now().UTC()
	}
	if marker.Marker == "" {
		marker.Marker = id
	}
	if marker.SHA256 == "" {
		marker.SHA256 = sha256Hex(id)
	}
	journal := InstallJournal{
		Schema:            InstallJournalSchema,
		TransactionID:     id,
		Operation:         req.Operation,
		Phase:             InstallPhasePrepared,
		Mode:              req.Layout,
		SourcePath:        art.Source,
		TargetPath:        art.Target,
		InstallPath:       art.Install,
		EndpointPath:      art.Endpoint,
		CredentialPath:    art.Credential,
		DataRoot:          art.DataRoot,
		BootID:            art.BootID,
		SourceIdentity:    stampedObject(art.SourceBytes, "1001", art.BootID, id),
		InstallIdentity:   stampedObject(art.DefinitionOld, "2002", art.BootID, id),
		CandidateHash:     art.CandidateHash,
		BackupHash:        art.BackupHash,
		DataAction:        art.DataAction,
		ServiceBackup:     ServiceBackup{Ref: "transactions/" + id + "/unit", SHA256: art.BackupHash, Identity: "8:3003"},
		OldRunning:        art.OldRunning,
		OldEnabled:        art.OldEnabled,
		RecoveryAuthority: InstallAuthoritySource,
		CreatedAt:         now,
		TransactionMarker: marker,
		Actions:           []JournalAction{},
	}
	if authority := x.preparedAuthority; authority != nil {
		journal.SourceIdentity, journal.TargetIdentity, journal.InstallIdentity = authority.Source, authority.Target, authority.Install
		journal.ServiceBackup = authority.ServiceBackup
		journal.BackupHash = authority.ServiceBackup.SHA256
	}
	if journal.Mode == "" {
		journal.Mode = InstallLayoutSystem
	}
	if err := validateInstallJournal(journal); err != nil {
		return InstallJournal{}, err
	}
	return journal, nil
}

func (x *InstallTransaction) resultFromJournal(ctx context.Context) (InstallResult, error) {
	if x.journal.TransactionID == "" && x.Store != nil {
		journal, err := x.Store.Load(ctx)
		if err != nil {
			return InstallResult{}, err
		}
		x.journal = journal
	}
	status := InstallServiceStopped
	if x.Service != nil {
		def, err := x.Service.InspectDefinition(ctx)
		if err == nil {
			switch {
			case def.Status == service.StatusNotInstalled:
				status = InstallServiceNotInstalled
			case def.Running:
				status = InstallServiceRunning
			}
		}
	}
	retained := true
	if x.Effects != nil {
		retained = x.Effects.SourcePresent()
	}
	return InstallResult{
		Schema:         InstallResultSchema,
		Changed:        true,
		ServiceStatus:  status,
		TransactionID:  x.journal.TransactionID,
		SourceRetained: retained,
	}, nil
}

func (x *InstallTransaction) newTransactionID() string {
	if x.NewID != nil {
		if id := x.NewID(); validTransactionID(id) {
			return id
		}
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return sha256Hex(fmt.Sprintf("%d", time.Now().UnixNano()))[:32]
	}
	return hex.EncodeToString(raw[:])
}

func closeInstallLease(lease InstallLease) {
	if closer, ok := lease.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

func stampedObject(data []byte, ino, boot, id string) JournalObject {
	if len(data) == 0 {
		return JournalObject{}
	}
	return JournalObject{
		Present:  true,
		SHA256:   sha256HexBytes(data),
		Dev:      "8",
		Ino:      ino,
		MountID:  "42",
		BootID:   boot,
		Marker:   id,
		Identity: "8:" + ino,
	}
}

func fileHash(data []byte) string {
	if len(data) == 0 {
		return "absent"
	}
	return sha256HexBytes(data)
}

func reloadToken(n int) string {
	return "reload:" + strconv.Itoa(n)
}

func installBusy(message string) error {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: message}
}

func installClosed(err error) error {
	if errors.Is(err, os.ErrClosed) {
		return installBusy("install lock is unavailable")
	}
	return err
}
