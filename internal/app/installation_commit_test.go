package app

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestInstallationCommit_PublishesApplyingThenCompleteBeforeStart(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeRepair)
	oldRaw := append([]byte(nil), h.raw...)
	plan, err := h.manager.Plan(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcome.Schema != InstallationOutcomeSchema || !outcome.InstallationComplete || outcome.StartFailed || outcome.ServiceState != InstallServiceRunning || outcome.ID != "abababababababababababababababab" {
		t.Fatalf("outcome=%+v", outcome)
	}
	if !bytes.Equal(h.archive, oldRaw) || len(h.publishedStates) != 2 {
		t.Fatalf("archive=%t published=%d", bytes.Equal(h.archive, oldRaw), len(h.publishedStates))
	}
	applying, complete := h.publishedStates[0], h.publishedStates[1]
	if applying.State != InstallationStateApplying || applying.Owner == nil || applying.ID != outcome.ID || applying.Supersedes == nil || applying.Supersedes.ID != "0123456789abcdef0123456789abcdef" || applying.Base == nil || applying.Base.Binary.Version != "old" {
		t.Fatalf("applying=%+v", applying)
	}
	if complete.State != InstallationStateComplete || complete.Owner != nil || complete.ID != applying.ID || complete.Target.Binary.SHA256 != plan.CandidateSHA256 {
		t.Fatalf("complete=%+v", complete)
	}
	wantOrder := []string{"prepare", "describe", "prepared.close", "prepare", "describe", "begin", "revalidate", "archive", "publish", "quiesce", "data.open", "bind_root", "apply", "publish", "data.close", "quiesced.close", "start", "session.close", "prepared.close"}
	if !reflect.DeepEqual(h.events, wantOrder) {
		t.Fatalf("events=%v\nwant=%v", h.events, wantOrder)
	}
}

func TestInstallationCommit_BindsNewRootBeforeWritingBusinessContent(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeRepair)
	h.raw = nil
	h.input.Instance.RecordID = ""
	h.input.Instance.RecordSHA256 = ""
	h.target.DataIdentity = nil
	h.target.DataParentIdentity = &InstallationDataParentIdentity{
		Path:     filepath.Dir(h.target.DataRoot),
		Identity: InstallationIdentity{BootID: "boot-a", Key: "parent-key", Marker: "parent-marker"},
		Relative: filepath.Base(h.target.DataRoot),
	}
	h.input.Instance.DataIdentity = nil
	h.input.Instance.DataParentIdentity = cloneInstallationDataParentIdentityForTest(h.target.DataParentIdentity)
	h.bindRoot = func(_ context.Context, target InstallationManifest) (InstallationManifest, error) {
		target.DataParentIdentity = nil
		target.DataIdentity = &InstallationIdentity{BootID: "boot-a", Key: "new-root-key", Marker: "new-root-marker"}
		return target, nil
	}
	plan, err := h.manager.Plan(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan}); err != nil {
		t.Fatal(err)
	}
	if len(h.publishedStates) != 3 {
		t.Fatalf("publications=%d", len(h.publishedStates))
	}
	initial, bound, complete := h.publishedStates[0], h.publishedStates[1], h.publishedStates[2]
	if initial.Target.DataParentIdentity == nil || initial.Target.DataIdentity != nil {
		t.Fatalf("initial applying did not bind parent: %+v", initial.Target)
	}
	if bound.ID != initial.ID || bound.State != InstallationStateApplying || bound.Target.DataIdentity == nil || bound.Target.DataParentIdentity != nil {
		t.Fatalf("bound applying=%+v", bound)
	}
	if complete.Target.DataIdentity == nil || complete.Target.DataIdentity.Key != bound.Target.DataIdentity.Key {
		t.Fatalf("complete target does not retain bound root: %+v", complete.Target)
	}
	bindIndex, secondPublishIndex, applyIndex := eventIndex(h.events, "bind_root", 1), eventIndex(h.events, "publish", 2), eventIndex(h.events, "apply", 1)
	if bindIndex < 0 || secondPublishIndex <= bindIndex || applyIndex <= secondPublishIndex {
		t.Fatalf("root identity publication did not precede business writes: %v", h.events)
	}
}

func TestInstallationCommit_DurabilityFailureStopsNextEffects(t *testing.T) {
	tests := []struct {
		name         string
		publications []installationPublicationResult
		wantPublish  int
		wantQuiesce  int
		wantApply    int
	}{
		{name: "applying visible but not durable", publications: []installationPublicationResult{{publication: InstallationPublication{Published: true}}}, wantPublish: 1},
		{name: "applying sync error", publications: []installationPublicationResult{{publication: InstallationPublication{Published: true}, err: errors.New("sync applying")}}, wantPublish: 1},
		{name: "complete visible but not durable", publications: []installationPublicationResult{{publication: InstallationPublication{Published: true, Durable: true}}, {publication: InstallationPublication{Published: true}}}, wantPublish: 2, wantQuiesce: 1, wantApply: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newInstallationManagerHarness(t, InstallationModeRepair)
			h.publications = tc.publications
			plan, err := h.manager.Plan(context.Background(), h.request())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan}); err == nil {
				t.Fatal("durability failure reported success")
			}
			if h.publishCalls != tc.wantPublish || h.quiesceCalls != tc.wantQuiesce || h.applyCalls != tc.wantApply || h.startCalls != 0 {
				t.Fatalf("publish=%d quiesce=%d apply=%d start=%d", h.publishCalls, h.quiesceCalls, h.applyCalls, h.startCalls)
			}
		})
	}
}

func TestInstallationCommit_LockedRecordChangeStopsBeforeArchive(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeRepair)
	plan, err := h.manager.Plan(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	h.lockedMutate = func(locked *InstallationLockedPreparation) {
		locked.RawState = append(append([]byte(nil), locked.RawState...), ' ')
	}
	if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan}); err == nil {
		t.Fatal("changed locked record was accepted")
	}
	if h.beginCalls != 1 || h.revalidateCalls != 1 || h.archiveCalls != 0 || h.publishCalls != 0 || h.quiesceCalls != 0 {
		t.Fatalf("changed record crossed commit boundary: begin=%d revalidate=%d archive=%d publish=%d quiesce=%d", h.beginCalls, h.revalidateCalls, h.archiveCalls, h.publishCalls, h.quiesceCalls)
	}
}

func TestInstallationCommit_ArchiveFailureLeavesAuthoritativeStateUntouched(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeRepair)
	before := append([]byte(nil), h.raw...)
	h.archiveErr = errors.New("archive failed")
	plan, err := h.manager.Plan(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan}); err == nil {
		t.Fatal("archive failure reported success")
	}
	if !bytes.Equal(h.raw, before) || h.publishCalls != 0 || h.quiesceCalls != 0 {
		t.Fatalf("archive failure changed authoritative state: publish=%d quiesce=%d", h.publishCalls, h.quiesceCalls)
	}
}

func TestInstallationCommit_ApplyFailureRemainsApplyingWithoutStart(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeRepair)
	h.apply = func(context.Context, InstallationManifest) (InstallationManifest, error) {
		return InstallationManifest{}, errors.New("deploy failed")
	}
	plan, err := h.manager.Plan(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan}); err == nil {
		t.Fatal("apply failure reported success")
	}
	state, err := DecodeInstallationState(bytes.NewReader(h.raw))
	if err != nil || state.State != InstallationStateApplying || state.ID != "abababababababababababababababab" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if h.publishCalls != 1 || h.startCalls != 0 {
		t.Fatalf("apply failure publish=%d start=%d", h.publishCalls, h.startCalls)
	}
}

func TestInstallationCommit_RootBindingMustBeDurableBeforeApply(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeRepair)
	h.raw = nil
	h.input.Instance.RecordID = ""
	h.input.Instance.RecordSHA256 = ""
	h.target.DataIdentity = nil
	h.target.DataParentIdentity = &InstallationDataParentIdentity{
		Path: filepath.Dir(h.target.DataRoot), Identity: InstallationIdentity{BootID: "boot-a", Key: "parent-key", Marker: "parent-marker"}, Relative: filepath.Base(h.target.DataRoot),
	}
	h.input.Instance.DataIdentity = nil
	h.input.Instance.DataParentIdentity = cloneInstallationDataParentIdentityForTest(h.target.DataParentIdentity)
	h.bindRoot = func(_ context.Context, target InstallationManifest) (InstallationManifest, error) {
		target.DataParentIdentity = nil
		target.DataIdentity = &InstallationIdentity{BootID: "boot-a", Key: "new-root-key", Marker: "new-root-marker"}
		return target, nil
	}
	h.publications = []installationPublicationResult{
		{publication: InstallationPublication{Published: true, Durable: true}},
		{publication: InstallationPublication{Published: true}},
	}
	plan, err := h.manager.Plan(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan}); err == nil {
		t.Fatal("non-durable root binding reported success")
	}
	if h.bindRootCalls != 1 || h.applyCalls != 0 || h.startCalls != 0 {
		t.Fatalf("non-durable root binding crossed write boundary: bind=%d apply=%d start=%d", h.bindRootCalls, h.applyCalls, h.startCalls)
	}
}

func TestInstallationCommit_ReadyFailureKeepsCompleteAndReturnsStandardError(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeRepair)
	h.startErr = errors.New("ready timeout")
	plan, err := h.manager.Plan(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan})
	var apiErr protocol.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != protocol.CodeInvalidState {
		t.Fatalf("error=%v", err)
	}
	if apiErr.Details["installation_complete"] != true || apiErr.Details["installation_id"] != "abababababababababababababababab" || apiErr.Details["reason"] != InstallationReasonServiceStartFailed {
		t.Fatalf("details=%v", apiErr.Details)
	}
	state, decodeErr := DecodeInstallationState(bytes.NewReader(h.raw))
	if decodeErr != nil || state.State != InstallationStateComplete || state.Owner != nil {
		t.Fatalf("state after Ready failure=%+v err=%v", state, decodeErr)
	}
	applyCalls := h.applyCalls
	if _, secondErr := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan}); secondErr == nil || h.applyCalls != applyCalls {
		t.Fatal("single-use plan reinstalled after Ready failure")
	}
}

func TestInstallationCommit_InvalidServiceResultReportsCompletedStartFailure(t *testing.T) {
	for _, serviceState := range []string{InstallServiceUnknown, "invalid"} {
		t.Run(serviceState, func(t *testing.T) {
			h := newInstallationManagerHarness(t, InstallationModeRepair)
			h.startState = serviceState
			plan, err := h.manager.Plan(context.Background(), h.request())
			if err != nil {
				t.Fatal(err)
			}
			_, err = h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan})
			var apiErr protocol.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != protocol.CodeInvalidState || apiErr.Details["installation_complete"] != true || apiErr.Details["installation_id"] != "abababababababababababababababab" || apiErr.Details["reason"] != InstallationReasonServiceStartFailed {
				t.Fatalf("error=%v details=%v", err, apiErr.Details)
			}
			state, decodeErr := DecodeInstallationState(bytes.NewReader(h.raw))
			if decodeErr != nil || state.State != InstallationStateComplete {
				t.Fatalf("state=%+v err=%v", state, decodeErr)
			}
		})
	}
}

func TestInstallationCommit_ReleaseFailureCannotReportSuccess(t *testing.T) {
	tests := []struct {
		name   string
		setErr func(*installationManagerHarness)
	}{
		{name: "data", setErr: func(h *installationManagerHarness) { h.dataCloseErr = errors.New("data close") }},
		{name: "startup", setErr: func(h *installationManagerHarness) { h.quiescedCloseErr = errors.New("startup close") }},
		{name: "operation", setErr: func(h *installationManagerHarness) { h.sessionCloseErr = errors.New("operation close") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newInstallationManagerHarness(t, InstallationModeRepair)
			tc.setErr(h)
			plan, err := h.manager.Plan(context.Background(), h.request())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan}); err == nil {
				t.Fatal("release failure reported success")
			}
		})
	}
}

func TestInstallationCommit_FinalManifestCannotChangePlannedTarget(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeRepair)
	h.apply = func(_ context.Context, target InstallationManifest) (InstallationManifest, error) {
		target.Binary.SHA256 = stringsRepeat("e", 64)
		return target, nil
	}
	plan, err := h.manager.Plan(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan}); err == nil {
		t.Fatal("changed final target was accepted")
	}
	if h.publishCalls != 1 || h.startCalls != 0 {
		t.Fatalf("changed final target published complete or started: publish=%d start=%d", h.publishCalls, h.startCalls)
	}
}

func stringsRepeat(value string, count int) string {
	return string(bytes.Repeat([]byte(value), count))
}

func eventIndex(events []string, value string, occurrence int) int {
	seen := 0
	for i, event := range events {
		if event == value {
			seen++
			if seen == occurrence {
				return i
			}
		}
	}
	return -1
}
