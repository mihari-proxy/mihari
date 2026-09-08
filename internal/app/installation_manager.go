package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

// InstallationPublication reports whether a state publication became visible
// and whether its durability was proven.
type InstallationPublication = platform.InstallPublication

// InstallationBackend prepares a verified candidate without taking the
// installation operation lock.
type InstallationBackend interface {
	Prepare(context.Context, InstallationPlanRequest) (InstallationPrepared, error)
}

// InstallationPrepared retains native candidate capabilities between
// preparation and an execution session.
type InstallationPrepared interface {
	Describe(context.Context) (VerifiedInstallationPlanInput, InstallationManifest, error)
	Begin(context.Context) (InstallationExecutionSession, error)
	Close() error
}

// InstallationLockedPreparation is a fresh observation made while holding the
// installation operation lock.
type InstallationLockedPreparation struct {
	Input       VerifiedInstallationPlanInput
	Target      InstallationManifest
	RawState    []byte
	StateSHA256 string
	Legacy      *InstallationManifest
	Owner       InstallationOwner
}

// InstallationExecutionSession owns the installation operation lock.
type InstallationExecutionSession interface {
	Revalidate(context.Context) (InstallationLockedPreparation, error)
	ArchiveState(context.Context, string) error
	PublishState(context.Context, string, []byte) (InstallationPublication, error)
	Quiesce(context.Context) (InstallationQuiesced, error)
	StartAndCheck(context.Context, InstallationManifest) (string, error)
	Close() error
}

// InstallationQuiesced owns the platform startup gate after the managed tree
// has been proven stopped.
type InstallationQuiesced interface {
	OpenData(context.Context) (InstallationDataSession, error)
	Close() error
}

// InstallationDataSession owns the data and endpoint capabilities used by the
// native deployment implementation.
type InstallationDataSession interface {
	BindRoot(context.Context) (InstallationManifest, error)
	Apply(context.Context) (InstallationManifest, error)
	Close() error
}

type authorizedInstallationPlan struct {
	request InstallationPlanRequest
	plan    InstallationPlan
	target  InstallationManifest
}

func newInstallationManager(options InstallationManagerOptions) *InstallationManager {
	random := options.Random
	if random == nil {
		random = rand.Reader
	}
	return &InstallationManager{observer: options.Observer, backend: options.Backend, random: random, plans: map[string]authorizedInstallationPlan{}}
}

// Plan prepares and records a read-only installation authorization.
func (m *InstallationManager) Plan(ctx context.Context, request InstallationPlanRequest) (InstallationPlan, error) {
	if err := ctx.Err(); err != nil {
		return InstallationPlan{}, err
	}
	if m == nil || m.backend == nil || !validInstallationPlanRequest(request) {
		return InstallationPlan{}, invalidInstallationPlan()
	}
	prepared, err := m.backend.Prepare(ctx, cloneInstallationPlanRequest(request))
	if err != nil {
		return InstallationPlan{}, fmt.Errorf("prepare installation plan: %w", err)
	}
	if prepared == nil {
		return InstallationPlan{}, invalidInstallationExecution("installation preparation is unavailable")
	}
	input, target, describeErr := prepared.Describe(ctx)
	input = cloneVerifiedInstallationPlanInput(input)
	target = cloneInstallationManifest(target)
	closeErr := prepared.Close()
	if describeErr != nil {
		return InstallationPlan{}, errors.Join(fmt.Errorf("describe installation plan: %w", describeErr), wrapInstallationClose("close installation preparation", closeErr))
	}
	if closeErr != nil {
		return InstallationPlan{}, fmt.Errorf("close installation preparation: %w", closeErr)
	}
	if err := validateInstallationPrepared(input, target); err != nil {
		return InstallationPlan{}, err
	}
	if err := validateInstallationRequestBinding(request, input); err != nil {
		return InstallationPlan{}, err
	}
	plan, err := BindInstallationPlan(input)
	if err != nil {
		return InstallationPlan{}, err
	}
	authorized := authorizedInstallationPlan{request: cloneInstallationPlanRequest(request), plan: cloneInstallationPlan(plan), target: cloneInstallationManifest(target)}
	m.plansMu.Lock()
	if m.plans == nil {
		m.plans = map[string]authorizedInstallationPlan{}
	}
	m.plans[plan.PlanSHA256] = authorized
	m.plansMu.Unlock()
	return cloneInstallationPlan(plan), nil
}

// Execute consumes a plan previously produced by this manager.
func (m *InstallationManager) Execute(ctx context.Context, request InstallationExecuteRequest) (outcome InstallationOutcome, returnErr error) {
	if err := ctx.Err(); err != nil {
		return InstallationOutcome{}, err
	}
	if m == nil || m.backend == nil || VerifyInstallationPlan(request.Plan) != nil {
		return InstallationOutcome{}, invalidInstallationPlan()
	}
	if request.Plan.Mode == InstallationModeFresh && !request.ResetConfirmed {
		return InstallationOutcome{}, invalidInstallationExecution("fresh installation reset is not confirmed")
	}
	authorized, ok := m.claimInstallationPlan(request.Plan)
	if !ok {
		return InstallationOutcome{}, invalidInstallationExecution("installation plan is stale")
	}
	if err := ctx.Err(); err != nil {
		return InstallationOutcome{}, err
	}

	prepared, err := m.backend.Prepare(ctx, cloneInstallationPlanRequest(authorized.request))
	if err != nil {
		return InstallationOutcome{}, fmt.Errorf("prepare installation execution: %w", err)
	}
	if prepared == nil {
		return InstallationOutcome{}, invalidInstallationExecution("installation preparation is unavailable")
	}
	defer func() {
		if err := prepared.Close(); err != nil {
			outcome = InstallationOutcome{}
			returnErr = errors.Join(returnErr, fmt.Errorf("close installation preparation: %w", err))
		}
	}()

	input, target, err := prepared.Describe(ctx)
	if err != nil {
		return InstallationOutcome{}, fmt.Errorf("describe installation execution: %w", err)
	}
	if err := validateInstallationPrepared(input, target); err != nil || !sameInstallationPlanInput(input, authorized.plan) || !reflect.DeepEqual(target, authorized.target) {
		return InstallationOutcome{}, invalidInstallationExecution("installation plan changed")
	}

	session, err := prepared.Begin(ctx)
	if err != nil {
		return InstallationOutcome{}, fmt.Errorf("begin installation operation: %w", err)
	}
	if session == nil {
		return InstallationOutcome{}, invalidInstallationExecution("installation operation is unavailable")
	}
	defer func() {
		if err := session.Close(); err != nil {
			outcome = InstallationOutcome{}
			returnErr = errors.Join(returnErr, fmt.Errorf("close installation operation: %w", err))
		}
	}()

	locked, current, err := revalidateInstallationExecution(ctx, session, authorized)
	if err != nil {
		return InstallationOutcome{}, err
	}
	resetEntries, err := installationResetEntryNames(authorized.plan)
	if err != nil {
		return InstallationOutcome{}, err
	}
	id, err := newInstallationID(m.random)
	if err != nil {
		return InstallationOutcome{}, fmt.Errorf("create installation id: %w", err)
	}
	applying := newApplyingInstallationState(id, locked, current, resetEntries)
	applyingRaw, err := EncodeInstallationState(applying)
	if err != nil {
		return InstallationOutcome{}, err
	}
	if len(locked.RawState) != 0 {
		if err := session.ArchiveState(ctx, locked.StateSHA256); err != nil {
			return InstallationOutcome{}, fmt.Errorf("archive installation state: %w", err)
		}
	}
	if err := publishDurableInstallationState(ctx, session, locked.StateSHA256, applyingRaw); err != nil {
		return InstallationOutcome{}, err
	}

	quiesced, err := session.Quiesce(ctx)
	if err != nil {
		return InstallationOutcome{}, fmt.Errorf("quiesce installation: %w", err)
	}
	if quiesced == nil {
		return InstallationOutcome{}, invalidInstallationExecution("quiesced installation session is unavailable")
	}
	defer func() {
		if quiesced != nil {
			if err := quiesced.Close(); err != nil {
				outcome = InstallationOutcome{}
				returnErr = errors.Join(returnErr, fmt.Errorf("close startup gate: %w", err))
			}
		}
	}()

	data, err := quiesced.OpenData(ctx)
	if err != nil {
		return InstallationOutcome{}, fmt.Errorf("open installation data: %w", err)
	}
	if data == nil {
		return InstallationOutcome{}, invalidInstallationExecution("installation data session is unavailable")
	}
	defer func() {
		if data != nil {
			if err := data.Close(); err != nil {
				outcome = InstallationOutcome{}
				returnErr = errors.Join(returnErr, fmt.Errorf("close installation data: %w", err))
			}
		}
	}()

	boundTarget, err := data.BindRoot(ctx)
	if err != nil {
		return InstallationOutcome{}, fmt.Errorf("bind installation data root: %w", err)
	}
	rootChanged, err := validateInstallationRootBinding(applying.Target, boundTarget)
	if err != nil {
		return InstallationOutcome{}, err
	}
	if rootChanged {
		applying.Target = cloneInstallationManifest(boundTarget)
		boundRaw, encodeErr := EncodeInstallationState(applying)
		if encodeErr != nil {
			return InstallationOutcome{}, encodeErr
		}
		if err := publishDurableInstallationState(ctx, session, installationSHA256(applyingRaw), boundRaw); err != nil {
			return InstallationOutcome{}, err
		}
		applyingRaw = boundRaw
	}

	finalTarget, err := data.Apply(ctx)
	if err != nil {
		return InstallationOutcome{}, fmt.Errorf("apply installation: %w", err)
	}
	if err := validateInstallationFinalTarget(boundTarget, finalTarget); err != nil {
		return InstallationOutcome{}, err
	}

	complete := applying
	complete.State = InstallationStateComplete
	complete.Owner = nil
	complete.Target = cloneInstallationManifest(finalTarget)
	completeRaw, err := EncodeInstallationState(complete)
	if err != nil {
		return InstallationOutcome{}, err
	}
	if err := publishDurableInstallationState(ctx, session, installationSHA256(applyingRaw), completeRaw); err != nil {
		return InstallationOutcome{}, err
	}
	if err := data.Close(); err != nil {
		data = nil
		return InstallationOutcome{}, fmt.Errorf("close installation data: %w", err)
	}
	data = nil
	if err := quiesced.Close(); err != nil {
		quiesced = nil
		return InstallationOutcome{}, fmt.Errorf("close startup gate: %w", err)
	}
	quiesced = nil

	serviceState, err := session.StartAndCheck(ctx, finalTarget)
	if err != nil {
		return InstallationOutcome{}, installationStartFailure(id)
	}
	if !validInstallationServiceState(serviceState) || serviceState == InstallServiceUnknown || serviceState == InstallServiceNotInstalled {
		return InstallationOutcome{}, installationStartFailure(id)
	}
	return InstallationOutcome{Schema: InstallationOutcomeSchema, InstallationComplete: true, ServiceState: serviceState, ID: id}, nil
}

func (m *InstallationManager) claimInstallationPlan(plan InstallationPlan) (authorizedInstallationPlan, bool) {
	m.plansMu.Lock()
	defer m.plansMu.Unlock()
	authorized, ok := m.plans[plan.PlanSHA256]
	if !ok || !reflect.DeepEqual(authorized.plan, plan) {
		return authorizedInstallationPlan{}, false
	}
	delete(m.plans, plan.PlanSHA256)
	authorized.request = cloneInstallationPlanRequest(authorized.request)
	authorized.plan = cloneInstallationPlan(authorized.plan)
	authorized.target = cloneInstallationManifest(authorized.target)
	return authorized, true
}

func validInstallationPlanRequest(request InstallationPlanRequest) bool {
	if request.Mode != InstallationModeRepair && request.Mode != InstallationModeFresh {
		return false
	}
	return request.Binary == "" || validAbsPath(request.Binary) && validInstallationPath(request.Binary)
}

func validateInstallationPrepared(input VerifiedInstallationPlanInput, target InstallationManifest) error {
	if err := validateInstallationPlanInput(input); err != nil || !target.Installed || validateInstallationManifest(target, true) != nil || target.Binary == nil {
		return invalidInstallationExecution("installation preparation is invalid")
	}
	instance := input.Instance
	if target.DataRoot != instance.DataRoot || !reflect.DeepEqual(target.DataIdentity, instance.DataIdentity) || !reflect.DeepEqual(target.DataParentIdentity, instance.DataParentIdentity) || target.Binary.SHA256 != input.CandidateSHA256 || target.Enabled != instance.Enabled || target.RunAfterInstall != instance.RunAfterInstall {
		return invalidInstallationExecution("installation preparation does not match plan input")
	}
	if !validInstallationManagerScope(input, target) {
		return invalidInstallationExecution("installation data scope is invalid")
	}
	return nil
}

func validateInstallationRequestBinding(request InstallationPlanRequest, input VerifiedInstallationPlanInput) error {
	if input.Mode != request.Mode {
		return invalidInstallationExecution("installation preparation mode does not match request")
	}
	if request.Enable != nil && input.Instance.Enabled != *request.Enable {
		return invalidInstallationExecution("installation enable policy does not match request")
	}
	if request.Start != nil && input.Instance.RunAfterInstall != *request.Start {
		return invalidInstallationExecution("installation start policy does not match request")
	}
	return nil
}

func validInstallationManagerScope(input VerifiedInstallationPlanInput, target InstallationManifest) bool {
	credentialPreserved := false
	for _, entry := range input.Preserve {
		if installationSamePath(entry.Path, target.Credential) && entry.Category == InstallationCategoryCredential {
			credentialPreserved = true
		}
	}
	if !credentialPreserved {
		return false
	}
	if input.Mode == InstallationModeRepair {
		return len(input.Delete) == 0
	}
	if input.Instance.SourceScope != nil {
		sourceIdentity := input.Instance.SourceScope.DataIdentity
		if target.DataIdentity != nil && sourceIdentity == *target.DataIdentity || target.DataParentIdentity != nil && sourceIdentity == target.DataParentIdentity.Identity {
			return false
		}
	}
	if input.Instance.SourceScope != nil && installationPathsOverlap(target.DataRoot, input.Instance.SourceScope.DataRoot) {
		return false
	}
	if len(input.Delete) != len(installationResetEntries) {
		return false
	}
	for i, expected := range installationResetEntries {
		entry := input.Delete[i]
		if !installationSamePath(entry.Path, filepath.Join(target.DataRoot, expected.name)) || entry.Category != expected.category || installationPathsOverlap(entry.Path, target.Credential) {
			return false
		}
	}
	return true
}

func sameInstallationPlanInput(input VerifiedInstallationPlanInput, plan InstallationPlan) bool {
	bound, err := BindInstallationPlan(input)
	return err == nil && reflect.DeepEqual(bound, plan)
}

func revalidateInstallationExecution(ctx context.Context, session InstallationExecutionSession, authorized authorizedInstallationPlan) (InstallationLockedPreparation, *InstallationState, error) {
	locked, err := session.Revalidate(ctx)
	if err != nil {
		return InstallationLockedPreparation{}, nil, fmt.Errorf("revalidate installation operation: %w", err)
	}
	if err := validateInstallationPrepared(locked.Input, locked.Target); err != nil || !sameInstallationPlanInput(locked.Input, authorized.plan) || !reflect.DeepEqual(locked.Target, authorized.target) || !validInstallationOwner(locked.Owner) {
		return InstallationLockedPreparation{}, nil, invalidInstallationExecution("installation changed while acquiring operation lock")
	}
	if len(locked.RawState) == 0 {
		if locked.StateSHA256 != "" || locked.Input.Instance.RecordID != "" || locked.Input.Instance.RecordSHA256 != "" {
			return InstallationLockedPreparation{}, nil, invalidInstallationExecution("installation record changed while acquiring operation lock")
		}
		if locked.Legacy != nil {
			legacy := locked.Legacy
			if validateInstallationManifest(*legacy, false) != nil || !legacy.Installed || legacy.DataRoot != locked.Target.DataRoot || legacy.InstallRoot != locked.Target.InstallRoot || legacy.Endpoint != locked.Target.Endpoint || legacy.Credential != locked.Target.Credential || !reflect.DeepEqual(legacy.DataIdentity, locked.Target.DataIdentity) {
				return InstallationLockedPreparation{}, nil, invalidInstallationExecution("legacy installation is invalid")
			}
		}
		return locked, nil, nil
	}
	if locked.Legacy != nil || locked.StateSHA256 != installationSHA256(locked.RawState) || locked.Input.Instance.RecordSHA256 != locked.StateSHA256 {
		return InstallationLockedPreparation{}, nil, invalidInstallationExecution("installation record changed while acquiring operation lock")
	}
	state, err := DecodeInstallationState(bytes.NewReader(locked.RawState))
	if err != nil || state.ID != locked.Input.Instance.RecordID || !reflect.DeepEqual(state.SourceScope, locked.Input.Instance.SourceScope) {
		return InstallationLockedPreparation{}, nil, invalidInstallationExecution("installation record changed while acquiring operation lock")
	}
	return locked, &state, nil
}

func newApplyingInstallationState(id string, locked InstallationLockedPreparation, current *InstallationState, resetEntries []string) InstallationState {
	state := InstallationState{Schema: InstallationStateSchema, ID: id, State: InstallationStateApplying, Operation: locked.Input.Mode, Owner: &locked.Owner, Target: cloneInstallationManifest(locked.Target), DataPolicy: InstallationDataRetain, ResetEntries: resetEntries, SourceScope: cloneInstallationSourceScope(locked.Input.Instance.SourceScope)}
	if locked.Input.Mode == InstallationModeFresh {
		state.DataPolicy = InstallationDataReset
	}
	if current != nil {
		state.Supersedes = &InstallationStateRef{ID: current.ID, SHA256: locked.StateSHA256}
		state.SourceScope = cloneInstallationSourceScope(current.SourceScope)
		if current.State == InstallationStateApplying {
			state.Base = cloneInstallationManifestPointer(current.Base)
		} else if !emptyInstallationManifest(current.Target) {
			state.Base = cloneInstallationManifestPointer(&current.Target)
		}
	} else if locked.Legacy != nil {
		state.Base = cloneInstallationManifestPointer(locked.Legacy)
	}
	return state
}

func installationResetEntryNames(plan InstallationPlan) ([]string, error) {
	if plan.Mode == InstallationModeRepair {
		return []string{}, nil
	}
	names := make([]string, len(plan.Delete))
	for i, entry := range plan.Delete {
		relative, err := filepath.Rel(plan.Instance.DataRoot, entry.Path)
		if err != nil || relative == "." || filepath.IsAbs(relative) || filepath.Dir(relative) != "." || !validResetEntry(relative) {
			return nil, invalidInstallationExecution("installation reset scope is invalid")
		}
		names[i] = relative
	}
	return names, nil
}

func publishDurableInstallationState(ctx context.Context, session InstallationExecutionSession, previousSHA string, next []byte) error {
	publication, err := session.PublishState(ctx, previousSHA, next)
	if err != nil {
		return fmt.Errorf("publish installation state: %w", err)
	}
	if !publication.Published || !publication.Durable {
		return invalidInstallationExecution("installation state publication is not durable")
	}
	return nil
}

func validateInstallationRootBinding(initial, bound InstallationManifest) (bool, error) {
	if validateInstallationManifest(bound, false) != nil || !bound.Installed {
		return false, invalidInstallationExecution("installation data root binding is invalid")
	}
	if reflect.DeepEqual(initial, bound) {
		return false, nil
	}
	if initial.DataIdentity != nil || initial.DataParentIdentity == nil || bound.DataIdentity == nil || bound.DataParentIdentity != nil {
		return false, invalidInstallationExecution("installation data root changed")
	}
	comparison := cloneInstallationManifest(bound)
	comparison.DataIdentity = nil
	comparison.DataParentIdentity = cloneInstallationDataParentIdentity(initial.DataParentIdentity)
	if !reflect.DeepEqual(initial, comparison) {
		return false, invalidInstallationExecution("installation target changed while binding data root")
	}
	return true, nil
}

func validateInstallationFinalTarget(bound, final InstallationManifest) error {
	if validateInstallationManifest(final, false) != nil || !final.Installed || !reflect.DeepEqual(bound, final) {
		return invalidInstallationExecution("installed target does not match the verified plan")
	}
	return nil
}

func newInstallationID(reader io.Reader) (string, error) {
	if reader == nil {
		reader = rand.Reader
	}
	value := make([]byte, 16)
	if _, err := io.ReadFull(reader, value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func installationSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func cloneInstallationPlanRequest(request InstallationPlanRequest) InstallationPlanRequest {
	clone := request
	if request.Enable != nil {
		value := *request.Enable
		clone.Enable = &value
	}
	if request.Start != nil {
		value := *request.Start
		clone.Start = &value
	}
	return clone
}

func cloneInstallationPlan(plan InstallationPlan) InstallationPlan {
	clone := plan
	clone.Instance = cloneInstallationPlanInstance(plan.Instance)
	clone.Preserve = cloneInstallationEntries(plan.Preserve)
	clone.Delete = cloneInstallationEntries(plan.Delete)
	return clone
}

func cloneVerifiedInstallationPlanInput(input VerifiedInstallationPlanInput) VerifiedInstallationPlanInput {
	clone := input
	clone.Instance = cloneInstallationPlanInstance(input.Instance)
	clone.Preserve = cloneInstallationEntries(input.Preserve)
	clone.Delete = cloneInstallationEntries(input.Delete)
	return clone
}

func cloneInstallationManifest(manifest InstallationManifest) InstallationManifest {
	clone := manifest
	clone.DataIdentity = cloneInstallationIdentity(manifest.DataIdentity)
	clone.DataParentIdentity = cloneInstallationDataParentIdentity(manifest.DataParentIdentity)
	if manifest.Binary != nil {
		binary := *manifest.Binary
		clone.Binary = &binary
	}
	return clone
}

func cloneInstallationManifestPointer(manifest *InstallationManifest) *InstallationManifest {
	if manifest == nil {
		return nil
	}
	clone := cloneInstallationManifest(*manifest)
	return &clone
}

func cloneInstallationIdentity(identity *InstallationIdentity) *InstallationIdentity {
	if identity == nil {
		return nil
	}
	clone := *identity
	return &clone
}

func cloneInstallationDataParentIdentity(parent *InstallationDataParentIdentity) *InstallationDataParentIdentity {
	if parent == nil {
		return nil
	}
	clone := *parent
	return &clone
}

func cloneInstallationSourceScope(scope *InstallationSourceScope) *InstallationSourceScope {
	if scope == nil {
		return nil
	}
	clone := *scope
	return &clone
}

func wrapInstallationClose(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func invalidInstallationExecution(message string) error {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: message}
}

func installationStartFailure(id string) error {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: "installation completed but service startup failed", Details: map[string]any{"installation_complete": true, "installation_id": id, "reason": InstallationReasonServiceStartFailed}}
}
