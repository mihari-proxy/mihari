package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestInstallationInspect_ClassifiesStableSnapshots(t *testing.T) {
	applying := mustInstallationStateJSON(t, testInstallationState(InstallationStateApplying))
	completeState := testInstallationState(InstallationStateComplete)
	complete := mustInstallationStateJSON(t, completeState)
	legacy := testInstallationManifest()
	uninstalledState := completeState
	uninstalledState.Operation = InstallationOperationUninstall
	uninstalledState.Target.Installed = false
	uninstalledState.Target.Binary = nil
	uninstalledState.Target.DefinitionSHA256 = ""
	uninstalled := mustInstallationStateJSON(t, uninstalledState)

	tests := []struct {
		name       string
		snapshot   InstallationSnapshot
		resource   InstallationResourceObservation
		service    InstallationServiceObservation
		wantKind   string
		wantState  string
		wantReason string
	}{
		{name: "applying locked", snapshot: InstallationSnapshot{Present: true, OperationLocked: true, State: applying}, service: InstallationServiceObservation{State: InstallServiceStopped}, wantKind: InstallationKindInProgress, wantState: InstallServiceStopped, wantReason: "installing"},
		{name: "locked while record is unavailable", snapshot: InstallationSnapshot{Present: true, OperationLocked: true}, service: InstallationServiceObservation{State: InstallServiceStopped}, wantKind: InstallationKindInProgress, wantState: InstallServiceStopped, wantReason: "installing"},
		{name: "locked before first record", snapshot: InstallationSnapshot{OperationLocked: true}, service: InstallationServiceObservation{State: InstallServiceStopped}, wantKind: InstallationKindInProgress, wantState: InstallServiceStopped, wantReason: "installing"},
		{name: "applying unlocked", snapshot: InstallationSnapshot{Present: true, State: applying}, service: InstallationServiceObservation{State: InstallServiceStopped}, wantKind: InstallationKindInterrupted, wantState: InstallServiceStopped, wantReason: "operation_interrupted"},
		{name: "complete stopped despite prior run policy", snapshot: InstallationSnapshot{Present: true, State: complete}, resource: InstallationResourceObservation{Matches: true}, service: InstallationServiceObservation{State: InstallServiceStopped, Enabled: true}, wantKind: InstallationKindInstalled, wantState: InstallServiceStopped},
		{name: "complete running ready", snapshot: InstallationSnapshot{Present: true, State: complete}, resource: InstallationResourceObservation{Matches: true}, service: InstallationServiceObservation{State: InstallServiceRunning, Enabled: true, Ready: true}, wantKind: InstallationKindInstalled, wantState: InstallServiceRunning},
		{name: "complete running not ready", snapshot: InstallationSnapshot{Present: true, State: complete}, resource: InstallationResourceObservation{Matches: true}, service: InstallationServiceObservation{State: InstallServiceRunning, Enabled: true}, wantKind: InstallationKindInstalled, wantState: InstallServiceRunning, wantReason: "service_not_ready"},
		{name: "complete policy mismatch", snapshot: InstallationSnapshot{Present: true, State: complete}, resource: InstallationResourceObservation{Matches: true}, service: InstallationServiceObservation{State: InstallServiceStopped, Enabled: false}, wantKind: InstallationKindInstalled, wantState: InstallServiceStopped, wantReason: "service_policy_mismatch"},
		{name: "complete uninstalled", snapshot: InstallationSnapshot{Present: true, State: uninstalled}, service: InstallationServiceObservation{State: InstallServiceNotInstalled}, wantKind: InstallationKindNotInstalled, wantState: InstallServiceNotInstalled},
		{name: "complete uninstalled but service stopped", snapshot: InstallationSnapshot{Present: true, State: uninstalled}, service: InstallationServiceObservation{State: InstallServiceStopped}, wantKind: InstallationKindUnknown, wantState: InstallServiceStopped, wantReason: "record_invalid"},
		{name: "complete uninstalled but service running", snapshot: InstallationSnapshot{Present: true, State: uninstalled}, service: InstallationServiceObservation{State: InstallServiceRunning, Ready: true}, wantKind: InstallationKindUnknown, wantState: InstallServiceRunning, wantReason: "record_invalid"},
		{name: "verified legacy", snapshot: InstallationSnapshot{Legacy: legacy}, resource: InstallationResourceObservation{Matches: true}, service: InstallationServiceObservation{State: InstallServiceStopped, Enabled: true}, wantKind: InstallationKindInstalled, wantState: InstallServiceStopped, wantReason: "legacy_record_absent"},
		{name: "absent", snapshot: InstallationSnapshot{}, service: InstallationServiceObservation{State: InstallServiceNotInstalled}, wantKind: InstallationKindNotInstalled, wantState: InstallServiceNotInstalled},
		{name: "absent but service exists", snapshot: InstallationSnapshot{}, service: InstallationServiceObservation{State: InstallServiceStopped}, wantKind: InstallationKindUnknown, wantState: InstallServiceStopped, wantReason: "record_invalid"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			observer := &fakeInstallationObserver{snapshots: []InstallationSnapshot{tc.snapshot, tc.snapshot}, resource: tc.resource, service: tc.service}
			status, err := NewInstallationManager(InstallationManagerOptions{Observer: observer}).Inspect(context.Background())
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if status.Schema != InstallationStatusSchema || status.Kind != tc.wantKind || status.ServiceState != tc.wantState || status.Reason != tc.wantReason || status.StartFailed {
				t.Fatalf("status=%+v", status)
			}
			if tc.snapshot.Present && len(tc.snapshot.State) != 0 && status.ID != "0123456789abcdef0123456789abcdef" {
				t.Fatalf("id=%q", status.ID)
			}
			observer.assertReadOnly(t)
		})
	}
}

func TestInstallationInspect_ClassifiesPermissionCorruptionAndMismatch(t *testing.T) {
	complete := mustInstallationStateJSON(t, testInstallationState(InstallationStateComplete))
	changed := append([]byte(nil), complete...)
	changed[len(changed)-2] = ' '
	tests := []struct {
		name       string
		observer   *fakeInstallationObserver
		wantKind   string
		wantReason string
	}{
		{name: "permission", observer: &fakeInstallationObserver{snapshotErr: ErrInstallationPermissionRequired}, wantKind: InstallationKindPermissionRequired, wantReason: "permission_required"},
		{name: "invalid record", observer: &fakeInstallationObserver{snapshots: []InstallationSnapshot{{Present: true, State: []byte(`{}`)}, {Present: true, State: []byte(`{}`)}}, service: InstallationServiceObservation{State: InstallServiceUnknown}}, wantKind: InstallationKindUnknown, wantReason: "record_invalid"},
		{name: "unstable record", observer: &fakeInstallationObserver{snapshots: []InstallationSnapshot{{Present: true, State: complete}, {Present: true, State: changed}}, resource: InstallationResourceObservation{Matches: true}, service: InstallationServiceObservation{State: InstallServiceStopped, Enabled: true}}, wantKind: InstallationKindUnknown, wantReason: "record_invalid"},
		{name: "resource mismatch", observer: &fakeInstallationObserver{snapshots: []InstallationSnapshot{{Present: true, State: complete}, {Present: true, State: complete}}, resource: InstallationResourceObservation{}, service: InstallationServiceObservation{State: InstallServiceStopped, Enabled: true}}, wantKind: InstallationKindUnknown, wantReason: "resource_mismatch"},
		{name: "resource permission", observer: &fakeInstallationObserver{snapshots: []InstallationSnapshot{{Present: true, State: complete}, {Present: true, State: complete}}, resourceErr: ErrInstallationPermissionRequired, service: InstallationServiceObservation{State: InstallServiceStopped, Enabled: true}}, wantKind: InstallationKindPermissionRequired, wantReason: "permission_required"},
		{name: "service permission preserves verified installation", observer: &fakeInstallationObserver{snapshots: []InstallationSnapshot{{Present: true, State: complete}, {Present: true, State: complete}}, resource: InstallationResourceObservation{Matches: true}, serviceErr: ErrInstallationPermissionRequired}, wantKind: InstallationKindInstalled},
		{name: "service permission preserves invalid record", observer: &fakeInstallationObserver{snapshots: []InstallationSnapshot{{Present: true, State: []byte(`{}`)}, {Present: true, State: []byte(`{}`)}}, serviceErr: ErrInstallationPermissionRequired}, wantKind: InstallationKindUnknown, wantReason: "record_invalid"},
		{name: "observation unknown", observer: &fakeInstallationObserver{snapshotErr: ErrInstallationObservationUnknown}, wantKind: InstallationKindUnknown, wantReason: "record_invalid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, err := NewInstallationManager(InstallationManagerOptions{Observer: tc.observer}).Inspect(context.Background())
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if status.Kind != tc.wantKind || status.Reason != tc.wantReason || status.StartFailed {
				t.Fatalf("status=%+v", status)
			}
			tc.observer.assertReadOnly(t)
		})
	}
}

func TestInstallationInspect_ServiceEvidenceOnlyDeterminesAnOtherwiseAbsentInstallation(t *testing.T) {
	complete := mustInstallationStateJSON(t, testInstallationState(InstallationStateComplete))
	tests := []struct {
		name       string
		observer   *fakeInstallationObserver
		wantKind   string
		wantReason string
	}{
		{name: "absent permission", observer: &fakeInstallationObserver{snapshots: []InstallationSnapshot{{}, {}}, serviceErr: ErrInstallationPermissionRequired}, wantKind: InstallationKindPermissionRequired, wantReason: InstallationReasonPermissionRequired},
		{name: "absent unknown", observer: &fakeInstallationObserver{snapshots: []InstallationSnapshot{{}, {}}, serviceErr: ErrInstallationObservationUnknown}, wantKind: InstallationKindUnknown, wantReason: InstallationReasonRecordInvalid},
		{name: "verified complete invalid service enum", observer: &fakeInstallationObserver{snapshots: []InstallationSnapshot{{Present: true, State: complete}, {Present: true, State: complete}}, resource: InstallationResourceObservation{Matches: true}, service: InstallationServiceObservation{State: "invalid"}}, wantKind: InstallationKindInstalled},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, err := NewInstallationManager(InstallationManagerOptions{Observer: tc.observer}).Inspect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if status.Kind != tc.wantKind || status.Reason != tc.wantReason || status.ServiceState != InstallServiceUnknown {
				t.Fatalf("status=%+v", status)
			}
		})
	}
}

func TestInstallationInspect_PropagatesContextAndUnexpectedErrors(t *testing.T) {
	for _, want := range []error{context.Canceled, errors.New("read failed")} {
		t.Run(want.Error(), func(t *testing.T) {
			_, err := NewInstallationManager(InstallationManagerOptions{Observer: &fakeInstallationObserver{snapshotErr: want}}).Inspect(context.Background())
			if !errors.Is(err, want) {
				t.Fatalf("error=%v want=%v", err, want)
			}
		})
	}
}

func TestInstallationInspect_DoesNotMutateObservedState(t *testing.T) {
	raw := mustInstallationStateJSON(t, testInstallationState(InstallationStateApplying))
	before := sha256.Sum256(raw)
	observer := &fakeInstallationObserver{
		snapshots: []InstallationSnapshot{{Present: true, State: raw}, {Present: true, State: raw}},
		service:   InstallationServiceObservation{State: InstallServiceStopped},
		fileCount: 4,
	}
	if _, err := NewInstallationManager(InstallationManagerOptions{Observer: observer}).Inspect(context.Background()); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	after := sha256.Sum256(raw)
	if before != after || observer.fileCount != 4 {
		t.Fatalf("read-only inspection mutated record or file count: before=%x after=%x files=%d", before, after, observer.fileCount)
	}
	observer.assertReadOnly(t)
}

type fakeInstallationObserver struct {
	snapshots     []InstallationSnapshot
	snapshotErr   error
	resource      InstallationResourceObservation
	resourceErr   error
	service       InstallationServiceObservation
	serviceErr    error
	snapshotCalls int
	recoveryCalls int
	writeCalls    int
	startCalls    int
	fileCount     int
}

func (f *fakeInstallationObserver) Snapshot(context.Context) (InstallationSnapshot, error) {
	if f.snapshotErr != nil {
		return InstallationSnapshot{}, f.snapshotErr
	}
	if f.snapshotCalls >= len(f.snapshots) {
		return InstallationSnapshot{}, fmt.Errorf("unexpected snapshot call %d", f.snapshotCalls+1)
	}
	snapshot := f.snapshots[f.snapshotCalls]
	f.snapshotCalls++
	snapshot.State = append([]byte(nil), snapshot.State...)
	return snapshot, nil
}

func (f *fakeInstallationObserver) VerifyManifest(context.Context, InstallationManifest) (InstallationResourceObservation, error) {
	return f.resource, f.resourceErr
}

func (f *fakeInstallationObserver) ObserveService(context.Context) (InstallationServiceObservation, error) {
	return f.service, f.serviceErr
}

func (f *fakeInstallationObserver) assertReadOnly(t *testing.T) {
	t.Helper()
	if f.recoveryCalls != 0 || f.writeCalls != 0 || f.startCalls != 0 {
		t.Fatalf("inspection caused side effects: recovery=%d write=%d start=%d", f.recoveryCalls, f.writeCalls, f.startCalls)
	}
}

func mustInstallationStateJSON(t *testing.T, state InstallationState) []byte {
	t.Helper()
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
