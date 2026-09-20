package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

type reinstallRuntime struct {
	*fakeRuntime
	calls int
}

func (r *reinstallRuntime) Reinstall(_ context.Context, operation runtimeapi.Operation) (core.InstallResult, error) {
	r.calls++
	r.operation = operation
	return core.InstallResult{Updated: true, Version: "v1.99.0"}, nil
}

func TestCoreReinstall_AuthenticatedDegradedRuntimeReceivesExplicitMutation(t *testing.T) {
	runtime := &reinstallRuntime{fakeRuntime: &fakeRuntime{snapshot: state.Snapshot{Health: "degraded", Revision: 9, Core: state.CoreState{Channel: "stable"}}}}
	server := New(Options{Token: "token", Store: state.NewStore(runtime.snapshot), Runtime: runtime})
	for _, authorized := range []bool{false, true} {
		request := authorizedRequest(http.MethodPost, "/v1/core/reinstall", bytes.NewBufferString(`{"operation_id":"repair-1","if_revision":9}`))
		if !authorized {
			request.Header.Del("Authorization")
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if !authorized {
			if response.Code != http.StatusUnauthorized || runtime.calls != 0 {
				t.Fatal("unauthenticated reinstall reached owner")
			}
			continue
		}
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var result protocol.CoreInstallResult
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if runtime.calls != 1 || runtime.operation.ID != "repair-1" || runtime.operation.IfRevision == nil || *runtime.operation.IfRevision != 9 || result.Channel != "stable" || result.Version != "v1.99.0" {
			t.Fatalf("lost reinstall metadata/result: %+v %+v", runtime.operation, result)
		}
	}
}
