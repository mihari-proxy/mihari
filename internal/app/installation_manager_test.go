package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallationPlan_ManagerIsReadOnlyAndCachesNativeRequest(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeRepair)
	enabled := true
	request := InstallationPlanRequest{Mode: InstallationModeRepair, Binary: h.target.Binary.Path, Enable: &enabled}
	before := append([]byte(nil), h.raw...)
	plan, err := h.manager.Plan(context.Background(), request)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if h.beginCalls != 0 || h.archiveCalls != 0 || h.publishCalls != 0 || h.quiesceCalls != 0 || h.applyCalls != 0 || h.startCalls != 0 || !bytes.Equal(before, h.raw) {
		t.Fatalf("Plan caused effects: begin=%d archive=%d publish=%d quiesce=%d apply=%d start=%d", h.beginCalls, h.archiveCalls, h.publishCalls, h.quiesceCalls, h.applyCalls, h.startCalls)
	}
	enabled = false
	if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(h.requests) != 2 || h.requests[1].Enable == nil || !*h.requests[1].Enable {
		t.Fatalf("cached request was not deep copied: %+v", h.requests)
	}
}

func TestInstallationPlan_RejectsDescribeFactsThatDoNotMatchExplicitRequest(t *testing.T) {
	tests := []struct {
		name    string
		request func(*installationManagerHarness) InstallationPlanRequest
	}{
		{name: "mode", request: func(h *installationManagerHarness) InstallationPlanRequest {
			request := h.request()
			request.Mode = InstallationModeFresh
			return request
		}},
		{name: "explicit disabled", request: func(h *installationManagerHarness) InstallationPlanRequest {
			request := h.request()
			value := false
			request.Enable = &value
			return request
		}},
		{name: "explicit no start", request: func(h *installationManagerHarness) InstallationPlanRequest {
			request := h.request()
			value := false
			request.Start = &value
			return request
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newInstallationManagerHarness(t, InstallationModeRepair)
			if _, err := h.manager.Plan(context.Background(), tc.request(h)); err == nil {
				t.Fatal("Plan accepted Describe facts that did not match the explicit request")
			}
			if h.prepareCalls != 1 || h.beginCalls != 0 || h.publishCalls != 0 {
				t.Fatalf("rejected request caused effects: prepare=%d begin=%d publish=%d", h.prepareCalls, h.beginCalls, h.publishCalls)
			}
		})
	}
}

func TestInstallationPlan_AcceptsMatchingExplicitFalsePolicies(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeRepair)
	h.input.Instance.Enabled = false
	h.input.Instance.RunAfterInstall = false
	h.target.Enabled = false
	h.target.RunAfterInstall = false
	value := false
	request := h.request()
	request.Enable = &value
	request.Start = &value
	plan, err := h.manager.Plan(context.Background(), request)
	if err != nil {
		t.Fatalf("Plan rejected matching explicit false policies: %v", err)
	}
	if plan.Instance.Enabled || plan.Instance.RunAfterInstall {
		t.Fatalf("plan policies=%+v", plan.Instance)
	}
}

func TestInstallationPlan_RejectsForeignOrChangedAuthorizationBeforePrepare(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeFresh)
	plan, err := h.manager.Plan(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	prepareAfterPlan := h.prepareCalls

	foreign := newInstallationManagerHarness(t, InstallationModeFresh)
	if _, err := foreign.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan, ResetConfirmed: true}); err == nil {
		t.Fatal("foreign manager accepted plan")
	}
	if foreign.prepareCalls != 0 {
		t.Fatal("foreign plan reached candidate preparation")
	}

	plan.Instance.Enabled = !plan.Instance.Enabled
	rebound, err := BindInstallationPlan(VerifiedInstallationPlanInput{Mode: plan.Mode, Instance: plan.Instance, CandidateSHA256: plan.CandidateSHA256, Preserve: plan.Preserve, Delete: plan.Delete})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: rebound, ResetConfirmed: true}); err == nil {
		t.Fatal("manager accepted caller-recomputed plan")
	}
	if h.prepareCalls != prepareAfterPlan {
		t.Fatal("changed plan reached candidate preparation")
	}
}

func TestInstallationPlan_FreshRequiresConfirmationBeforeEffects(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeFresh)
	plan, err := h.manager.Plan(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan}); err == nil {
		t.Fatal("fresh plan executed without reset confirmation")
	}
	if h.prepareCalls != 1 || h.beginCalls != 0 || h.archiveCalls != 0 || h.publishCalls != 0 || h.quiesceCalls != 0 {
		t.Fatalf("unconfirmed fresh caused effects: prepare=%d begin=%d archive=%d publish=%d quiesce=%d", h.prepareCalls, h.beginCalls, h.archiveCalls, h.publishCalls, h.quiesceCalls)
	}
}

func TestInstallationPlan_ReprepareChangeFailsBeforeOperationLock(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeRepair)
	plan, err := h.manager.Plan(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	h.describeMutate = func(input *VerifiedInstallationPlanInput, target *InstallationManifest, prepare int) {
		if prepare == 2 {
			input.CandidateSHA256 = strings.Repeat("e", 64)
			target.Binary.SHA256 = input.CandidateSHA256
		}
	}
	if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan}); err == nil {
		t.Fatal("changed candidate was accepted")
	}
	if h.prepareCalls != 2 || h.beginCalls != 0 || h.archiveCalls != 0 || h.publishCalls != 0 {
		t.Fatalf("changed candidate crossed operation boundary: prepare=%d begin=%d archive=%d publish=%d", h.prepareCalls, h.beginCalls, h.archiveCalls, h.publishCalls)
	}
}

func TestInstallationPlan_RejectsUnfixedResetOrUnprotectedCredential(t *testing.T) {
	tests := []struct {
		name   string
		mode   string
		mutate func(*installationManagerHarness)
	}{
		{name: "fresh missing fixed entry", mode: InstallationModeFresh, mutate: func(h *installationManagerHarness) { h.input.Delete = h.input.Delete[:len(h.input.Delete)-1] }},
		{name: "fresh category changed", mode: InstallationModeFresh, mutate: func(h *installationManagerHarness) { h.input.Delete[0].Category = InstallationCategoryUnknown }},
		{name: "repair credential omitted", mode: InstallationModeRepair, mutate: func(h *installationManagerHarness) {
			preserve := h.input.Preserve[:0]
			for _, entry := range h.input.Preserve {
				if entry.Category != InstallationCategoryCredential {
					preserve = append(preserve, entry)
				}
			}
			h.input.Preserve = preserve
		}},
		{name: "fresh source overlaps target", mode: InstallationModeFresh, mutate: func(h *installationManagerHarness) {
			h.input.Instance.SourceScope = &InstallationSourceScope{DataRoot: h.target.DataRoot, DataIdentity: *h.target.DataIdentity, Selection: "explicit_request", EvidenceSHA256: strings.Repeat("e", 64)}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newInstallationManagerHarness(t, tc.mode)
			tc.mutate(h)
			if _, err := h.manager.Plan(context.Background(), h.request()); err == nil {
				t.Fatal("unsafe manager scope was accepted")
			}
			if h.beginCalls != 0 || h.publishCalls != 0 {
				t.Fatalf("unsafe plan caused effects: begin=%d publish=%d", h.beginCalls, h.publishCalls)
			}
		})
	}
}

type installationManagerHarness struct {
	t                *testing.T
	manager          *InstallationManager
	input            VerifiedInstallationPlanInput
	target           InstallationManifest
	raw              []byte
	legacy           *InstallationManifest
	requests         []InstallationPlanRequest
	events           []string
	describeMutate   func(*VerifiedInstallationPlanInput, *InstallationManifest, int)
	lockedMutate     func(*InstallationLockedPreparation)
	bindRoot         func(context.Context, InstallationManifest) (InstallationManifest, error)
	apply            func(context.Context, InstallationManifest) (InstallationManifest, error)
	publications     []installationPublicationResult
	startState       string
	startErr         error
	prepareCloseErr  error
	sessionCloseErr  error
	quiescedCloseErr error
	dataCloseErr     error
	archiveErr       error
	prepareCalls     int
	describeCalls    int
	beginCalls       int
	revalidateCalls  int
	archiveCalls     int
	publishCalls     int
	quiesceCalls     int
	openDataCalls    int
	bindRootCalls    int
	applyCalls       int
	startCalls       int
	archive          []byte
	publishedStates  []InstallationState
}

type installationPublicationResult struct {
	publication InstallationPublication
	err         error
}

func newInstallationManagerHarness(t *testing.T, mode string) *installationManagerHarness {
	t.Helper()
	root := t.TempDir()
	identity := &InstallationIdentity{BootID: "boot-a", Key: "data-key", Marker: "data-marker"}
	base := InstallationManifest{
		Installed: true, DataRoot: root, InstallRoot: filepath.Join(root, "install"),
		Endpoint: filepath.Join(root, "control.sock"), Credential: filepath.Join(root, "control.token"),
		DataIdentity: identity, Binary: &InstallationBinary{Path: filepath.Join(root, "old-mihari"), SHA256: strings.Repeat("a", 64), Version: "old"},
		DefinitionSHA256: strings.Repeat("b", 64), Enabled: true, RunAfterInstall: false,
	}
	state := InstallationState{
		Schema: InstallationStateSchema, ID: "0123456789abcdef0123456789abcdef", State: InstallationStateApplying,
		Operation: InstallationOperationRepair, Owner: &InstallationOwner{BootID: "boot-a", PID: 20, Start: "10"},
		Base: cloneInstallationManifestForTest(base), Target: base, DataPolicy: InstallationDataRetain, ResetEntries: []string{},
	}
	raw, err := EncodeInstallationState(state)
	if err != nil {
		t.Fatal(err)
	}
	target := base
	target.Binary = &InstallationBinary{Path: filepath.Join(root, "new-mihari"), SHA256: strings.Repeat("c", 64), Version: "new"}
	target.DefinitionSHA256 = strings.Repeat("d", 64)
	target.RunAfterInstall = true
	children := []string{"mihari.yaml", "subscriptions", "preferences", "logs", "unknown.data", "control.token"}
	preserve, remove, err := installationDataEntries(mode, target, nil, children)
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	h := &installationManagerHarness{t: t, raw: raw, target: target, startState: InstallServiceRunning}
	h.input = VerifiedInstallationPlanInput{
		Mode: mode,
		Instance: InstallationPlanInstance{
			RecordID: state.ID, RecordSHA256: hash, DataRoot: root, DataIdentity: cloneInstallationIdentityForTest(identity),
			Enabled: target.Enabled, RunAfterInstall: target.RunAfterInstall,
		},
		CandidateSHA256: target.Binary.SHA256, Preserve: preserve, Delete: remove,
	}
	h.manager = NewInstallationManager(InstallationManagerOptions{Backend: h, Random: bytes.NewReader(bytes.Repeat([]byte{0xab}, 64))})
	return h
}

func (h *installationManagerHarness) request() InstallationPlanRequest {
	return InstallationPlanRequest{Mode: h.input.Mode, Binary: h.target.Binary.Path}
}

func (h *installationManagerHarness) Prepare(_ context.Context, request InstallationPlanRequest) (InstallationPrepared, error) {
	h.prepareCalls++
	h.events = append(h.events, "prepare")
	h.requests = append(h.requests, cloneInstallationPlanRequestForTest(request))
	input := cloneVerifiedInstallationPlanInputForTest(h.input)
	target := *cloneInstallationManifestForTest(h.target)
	if h.describeMutate != nil {
		h.describeMutate(&input, &target, h.prepareCalls)
	}
	return &installationPreparedFake{h: h, input: input, target: target}, nil
}

type installationPreparedFake struct {
	h      *installationManagerHarness
	input  VerifiedInstallationPlanInput
	target InstallationManifest
}

func (p *installationPreparedFake) Describe(context.Context) (VerifiedInstallationPlanInput, InstallationManifest, error) {
	p.h.describeCalls++
	p.h.events = append(p.h.events, "describe")
	return cloneVerifiedInstallationPlanInputForTest(p.input), *cloneInstallationManifestForTest(p.target), nil
}
func (p *installationPreparedFake) Begin(context.Context) (InstallationExecutionSession, error) {
	p.h.beginCalls++
	p.h.events = append(p.h.events, "begin")
	return &installationExecutionSessionFake{prepared: p}, nil
}
func (p *installationPreparedFake) Close() error {
	p.h.events = append(p.h.events, "prepared.close")
	return p.h.prepareCloseErr
}

type installationExecutionSessionFake struct{ prepared *installationPreparedFake }

func (s *installationExecutionSessionFake) Revalidate(context.Context) (InstallationLockedPreparation, error) {
	h := s.prepared.h
	h.revalidateCalls++
	h.events = append(h.events, "revalidate")
	locked := InstallationLockedPreparation{
		Input: cloneVerifiedInstallationPlanInputForTest(s.prepared.input), Target: *cloneInstallationManifestForTest(s.prepared.target),
		RawState: append([]byte(nil), h.raw...), StateSHA256: fmt.Sprintf("%x", sha256.Sum256(h.raw)),
		Legacy: cloneInstallationManifestForTestPtr(h.legacy), Owner: InstallationOwner{BootID: "boot-a", PID: 42, Start: "20"},
	}
	if len(h.raw) == 0 {
		locked.RawState = nil
		locked.StateSHA256 = ""
	}
	if h.lockedMutate != nil {
		h.lockedMutate(&locked)
	}
	return locked, nil
}
func (s *installationExecutionSessionFake) ArchiveState(_ context.Context, expected string) error {
	h := s.prepared.h
	h.archiveCalls++
	h.events = append(h.events, "archive")
	if h.archiveErr != nil {
		return h.archiveErr
	}
	if expected != fmt.Sprintf("%x", sha256.Sum256(h.raw)) {
		return errors.New("archive digest changed")
	}
	h.archive = append([]byte(nil), h.raw...)
	return nil
}
func (s *installationExecutionSessionFake) PublishState(_ context.Context, previous string, next []byte) (InstallationPublication, error) {
	h := s.prepared.h
	h.publishCalls++
	h.events = append(h.events, "publish")
	wantPrevious := ""
	if len(h.raw) != 0 {
		wantPrevious = fmt.Sprintf("%x", sha256.Sum256(h.raw))
	}
	if previous != wantPrevious {
		return InstallationPublication{}, fmt.Errorf("previous digest=%q want=%q", previous, wantPrevious)
	}
	result := installationPublicationResult{publication: InstallationPublication{Published: true, Durable: true}}
	if h.publishCalls <= len(h.publications) {
		result = h.publications[h.publishCalls-1]
	}
	if result.publication.Published {
		h.raw = append([]byte(nil), next...)
		if state, err := DecodeInstallationState(bytes.NewReader(next)); err == nil {
			h.publishedStates = append(h.publishedStates, state)
		}
	}
	return result.publication, result.err
}
func (s *installationExecutionSessionFake) Quiesce(context.Context) (InstallationQuiesced, error) {
	h := s.prepared.h
	h.quiesceCalls++
	h.events = append(h.events, "quiesce")
	return &installationQuiescedFake{prepared: s.prepared}, nil
}
func (s *installationExecutionSessionFake) StartAndCheck(_ context.Context, _ InstallationManifest) (string, error) {
	h := s.prepared.h
	h.startCalls++
	h.events = append(h.events, "start")
	return h.startState, h.startErr
}
func (s *installationExecutionSessionFake) Close() error {
	s.prepared.h.events = append(s.prepared.h.events, "session.close")
	return s.prepared.h.sessionCloseErr
}

type installationQuiescedFake struct{ prepared *installationPreparedFake }

func (q *installationQuiescedFake) OpenData(context.Context) (InstallationDataSession, error) {
	q.prepared.h.openDataCalls++
	q.prepared.h.events = append(q.prepared.h.events, "data.open")
	return &installationDataSessionFake{prepared: q.prepared}, nil
}
func (q *installationQuiescedFake) Close() error {
	q.prepared.h.events = append(q.prepared.h.events, "quiesced.close")
	return q.prepared.h.quiescedCloseErr
}

type installationDataSessionFake struct {
	prepared *installationPreparedFake
	target   InstallationManifest
}

func (d *installationDataSessionFake) BindRoot(ctx context.Context) (InstallationManifest, error) {
	h := d.prepared.h
	h.bindRootCalls++
	h.events = append(h.events, "bind_root")
	target := *cloneInstallationManifestForTest(d.prepared.target)
	if h.bindRoot != nil {
		bound, err := h.bindRoot(ctx, target)
		if err != nil {
			return InstallationManifest{}, err
		}
		target = bound
	}
	d.target = *cloneInstallationManifestForTest(target)
	return target, nil
}

func (d *installationDataSessionFake) Apply(ctx context.Context) (InstallationManifest, error) {
	h := d.prepared.h
	h.applyCalls++
	h.events = append(h.events, "apply")
	target := *cloneInstallationManifestForTest(d.target)
	if h.apply != nil {
		return h.apply(ctx, target)
	}
	return target, nil
}
func (d *installationDataSessionFake) Close() error {
	d.prepared.h.events = append(d.prepared.h.events, "data.close")
	return d.prepared.h.dataCloseErr
}

func cloneInstallationPlanRequestForTest(request InstallationPlanRequest) InstallationPlanRequest {
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

func cloneVerifiedInstallationPlanInputForTest(input VerifiedInstallationPlanInput) VerifiedInstallationPlanInput {
	return VerifiedInstallationPlanInput{
		Mode: input.Mode, Instance: cloneInstallationPlanInstance(input.Instance), CandidateSHA256: input.CandidateSHA256,
		Preserve: cloneInstallationEntries(input.Preserve), Delete: cloneInstallationEntries(input.Delete),
	}
}

func cloneInstallationManifestForTest(manifest InstallationManifest) *InstallationManifest {
	clone := manifest
	clone.DataIdentity = cloneInstallationIdentityForTest(manifest.DataIdentity)
	if manifest.DataParentIdentity != nil {
		parent := *manifest.DataParentIdentity
		clone.DataParentIdentity = &parent
	}
	if manifest.Binary != nil {
		binary := *manifest.Binary
		clone.Binary = &binary
	}
	return &clone
}

func cloneInstallationManifestForTestPtr(manifest *InstallationManifest) *InstallationManifest {
	if manifest == nil {
		return nil
	}
	return cloneInstallationManifestForTest(*manifest)
}

func cloneInstallationDataParentIdentityForTest(parent *InstallationDataParentIdentity) *InstallationDataParentIdentity {
	if parent == nil {
		return nil
	}
	clone := *parent
	return &clone
}

func cloneInstallationIdentityForTest(identity *InstallationIdentity) *InstallationIdentity {
	if identity == nil {
		return nil
	}
	clone := *identity
	return &clone
}

func writeInstallationFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
