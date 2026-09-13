package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

func TestPortRecoverySurface_AdvertisesOnlyRecovery(t *testing.T) {
	f := &fakeOnboardingRuntime{fakeRuntime: &fakeRuntime{}, status: onboarding.Status{}}
	s := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{Health: "degraded"}), Onboarding: f})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, authorizedRequest(http.MethodGet, "/v1/status", nil))
	var status protocol.Status
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Health != "degraded" || !status.SetupRequired || !slices.Contains(status.Capabilities, protocol.CapabilityOnboarding) || slices.Contains(status.Capabilities, protocol.CapabilityCore) {
		t.Fatal("recovery exposed normal-runtime capabilities")
	}
}

func TestPortRecoverySurface_AcceptsEndpointUpdate(t *testing.T) {
	f := &fakeOnboardingRuntime{fakeRuntime: &fakeRuntime{}, status: onboarding.Status{}}
	s := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{Health: "degraded"}), Onboarding: f})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, authorizedRequest(http.MethodPatch, "/v1/onboarding", bytes.NewBufferString(`{"operation_id":"save","web_addr":"127.0.0.1:19291"}`)))
	if w.Code != http.StatusOK || f.update.WebAddr == nil {
		t.Fatal("recovery could not update endpoints")
	}
}

func TestPortRecoverySurface_RejectsCoreMutation(t *testing.T) {
	f := &fakeOnboardingRuntime{fakeRuntime: &fakeRuntime{}, status: onboarding.Status{}}
	s := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{Health: "degraded"}), Onboarding: f})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/core/install", bytes.NewBufferString(`{"operation_id":"forbidden"}`)))
	if w.Code != http.StatusConflict {
		t.Fatalf("restricted recovery status = %d, want %d", w.Code, http.StatusConflict)
	}
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode restricted recovery error: %v", err)
	}
	if envelope.Error.Code != protocol.CodeInvalidState {
		t.Fatalf("restricted recovery error code = %s, want %s", envelope.Error.Code, protocol.CodeInvalidState)
	}
}

type fakeOnboardingRuntime struct {
	*fakeRuntime
	status   onboarding.Status
	update   onboarding.Update
	revision uint64
	reads    int
}

type readyOnboardingRuntime struct{ *fakeOnboardingRuntime }

func (f *readyOnboardingRuntime) SetupRequired(context.Context) (bool, error) { return false, nil }

func TestStatus_ReadyRequiredResourcesDoNotRepeatOptionalSetup(t *testing.T) {
	f := &readyOnboardingRuntime{&fakeOnboardingRuntime{fakeRuntime: &fakeRuntime{}, status: onboarding.Status{Complete: false}}}
	s := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: f})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, authorizedRequest(http.MethodGet, "/v1/status", nil))
	var got protocol.Status
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.SetupRequired {
		t.Fatal("optional setup marker overrode actual readiness")
	}
	if f.reads != 0 {
		t.Fatal("status read both readiness and the historical onboarding marker")
	}
}

type unavailableReadinessRuntime struct{ *fakeOnboardingRuntime }

func (f *unavailableReadinessRuntime) SetupRequired(context.Context) (bool, error) {
	return false, context.DeadlineExceeded
}

func TestStatus_ReadinessFailureDoesNotRequireSetup(t *testing.T) {
	f := &unavailableReadinessRuntime{&fakeOnboardingRuntime{fakeRuntime: &fakeRuntime{}, status: onboarding.Status{Complete: false}}}
	s := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: f})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, authorizedRequest(http.MethodGet, "/v1/status", nil))
	var status protocol.Status
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || status.SetupRequired || f.reads != 0 {
		t.Fatal("readiness failure fell back to a stale onboarding marker")
	}
}

func (f *fakeOnboardingRuntime) OnboardingStatus(context.Context) (onboarding.Snapshot, error) {
	f.reads++
	return onboarding.Snapshot{Status: f.status, Revision: f.revision}, nil
}

func (f *fakeOnboardingRuntime) UpdateOnboarding(_ context.Context, operation runtimeapi.Operation, update onboarding.Update) (onboarding.Snapshot, error) {
	f.operation, f.update = operation, update
	f.status.RestartRequired = true
	f.revision++
	return onboarding.Snapshot{Status: f.status, Revision: f.revision}, nil
}

func TestOnboardingRoutesExposeSafeStatusAndRevisionCheckedUpdate(t *testing.T) {
	base := &fakeRuntime{snapshot: state.Snapshot{Revision: 99}}
	fake := &fakeOnboardingRuntime{fakeRuntime: base, revision: 7, status: onboarding.Status{
		MixedAddr: "127.0.0.1:9190", ControllerAddr: "127.0.0.1:9090", WebAddr: "127.0.0.1:9191",
	}}
	server := New(Options{Token: "token", Store: state.NewStore(base.snapshot), Runtime: fake})

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodGet, "/v1/onboarding", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var got protocol.OnboardingStatus
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Revision != 7 || got.Complete || got.WebAddr != "127.0.0.1:9191" {
		t.Fatalf("onboarding=%#v", got)
	}

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPatch, "/v1/onboarding", bytes.NewBufferString(
		`{"operation_id":"setup-1","if_revision":7,"complete":true,"web_addr":"127.0.0.1:9292"}`,
	)))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.operation.ID != "setup-1" || fake.operation.IfRevision == nil || *fake.operation.IfRevision != 7 || fake.update.Complete == nil || !*fake.update.Complete || fake.update.WebAddr == nil || *fake.update.WebAddr != "127.0.0.1:9292" {
		t.Fatalf("operation=%#v update=%#v", fake.operation, fake.update)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || got.Revision != 8 || !got.RestartRequired {
		t.Fatalf("onboarding=%#v err=%v", got, err)
	}
}

func TestStatusReportsSetupRequiredFromDaemonOnboardingState(t *testing.T) {
	base := &fakeRuntime{snapshot: state.Snapshot{Revision: 2}}
	fake := &fakeOnboardingRuntime{fakeRuntime: base, revision: 2, status: onboarding.Status{Complete: false}}
	server := New(Options{Token: "token", Store: state.NewStore(base.snapshot), Runtime: fake})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodGet, "/v1/status", nil))
	var got protocol.Status
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.SetupRequired {
		t.Fatalf("status=%#v", got)
	}
}

func TestOnboardingUpdateRejectsEmptyMutation(t *testing.T) {
	base := &fakeRuntime{snapshot: state.Snapshot{Revision: 2}}
	fake := &fakeOnboardingRuntime{fakeRuntime: base, revision: 2}
	server := New(Options{Token: "token", Store: state.NewStore(base.snapshot), Runtime: fake})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPatch, "/v1/onboarding", bytes.NewBufferString(`{"operation_id":"empty"}`)))
	if response.Code != http.StatusBadRequest || fake.revision != 2 {
		t.Fatalf("status=%d revision=%d body=%s", response.Code, fake.revision, response.Body.String())
	}
}
