package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/preferences"
	runtimeapi "github.com/LeeShunEE/mihari/internal/runtime"
	"github.com/LeeShunEE/mihari/internal/state"
)

func TestTUIPreferencesEndpointsUseRuntimeMutation(t *testing.T) {
	fake := &fakePreferencesRuntime{
		fakeRuntime: fakeRuntime{snapshot: state.Snapshot{Revision: 4}},
		preferences: preferences.Preferences{ConnectionsColumns: []string{"host", "chain"}},
	}
	server := New(Options{Token: "token", Store: state.NewStore(fake.snapshot), Runtime: fake})

	get := httptest.NewRecorder()
	server.Handler().ServeHTTP(get, authorizedRequest(http.MethodGet, "/v1/preferences/tui", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", get.Code, get.Body.String())
	}
	var initial protocol.TUIPreferences
	if err := json.Unmarshal(get.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if initial.Revision != 4 || !slices.Equal(initial.ConnectionsColumns, []string{"host", "chain"}) {
		t.Fatalf("initial=%#v", initial)
	}

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPatch, "/v1/preferences/tui", bytes.NewBufferString(
		`{"operation_id":"columns-1","if_revision":4,"connections_columns":["source","traffic"]}`,
	)))
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.operation.ID != "columns-1" || fake.operation.IfRevision == nil || *fake.operation.IfRevision != 4 ||
		!slices.Equal(fake.update.ConnectionsColumns, []string{"source", "traffic"}) {
		t.Fatalf("operation=%#v update=%#v", fake.operation, fake.update)
	}
	var updated protocol.TUIPreferences
	if err := json.Unmarshal(response.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 5 || !slices.Equal(updated.ConnectionsColumns, []string{"source", "traffic"}) {
		t.Fatalf("updated=%#v", updated)
	}
}

type fakePreferencesRuntime struct {
	fakeRuntime
	preferences preferences.Preferences
	operation   runtimeapi.Operation
	update      preferences.Update
}

func (f *fakePreferencesRuntime) TUIPreferences() preferences.Preferences { return f.preferences }

func (f *fakePreferencesRuntime) UpdateTUIPreferences(_ context.Context, operation runtimeapi.Operation, update preferences.Update) (preferences.Preferences, error) {
	f.operation = operation
	f.update = update
	f.preferences = preferences.Preferences{ConnectionsColumns: append([]string(nil), update.ConnectionsColumns...)}
	f.snapshot.Revision++
	return f.preferences, nil
}
