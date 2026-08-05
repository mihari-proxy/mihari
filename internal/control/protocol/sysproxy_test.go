package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSystemProxyStatus_StableJSONContract(t *testing.T) {
	status := SystemProxyStatus{
		Schema:   "mihari/v1",
		Revision: 12,
		Desired:  true,
		Target:   "127.0.0.1:9190",
		Observed: SystemProxyObserved{
			Enabled: true,
			Server:  "127.0.0.1:9190",
			Owned:   true,
			Foreign: false,
		},
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema":"mihari/v1","revision":12,"desired":true,"target":"127.0.0.1:9190","observed":{"enabled":true,"server":"127.0.0.1:9190","owned":true,"foreign":false}}`
	if string(raw) != want {
		t.Fatalf("json=%s want=%s", raw, want)
	}

	var got SystemProxyStatus
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, status) {
		t.Fatalf("got=%#v want=%#v", got, status)
	}
}

func TestSystemProxyStatus_OmitEmptyFields(t *testing.T) {
	status := SystemProxyStatus{
		Schema:   "mihari/v1",
		Revision: 1,
		Desired:  false,
		Observed: SystemProxyObserved{Enabled: false, Owned: false, Foreign: false},
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema":"mihari/v1","revision":1,"desired":false,"observed":{"enabled":false,"owned":false,"foreign":false}}`
	if string(raw) != want {
		t.Fatalf("json=%s want=%s", raw, want)
	}
}

func TestSystemProxyMutationRequest_StableJSONContract(t *testing.T) {
	revision := uint64(12)
	req := SystemProxyMutationRequest{
		OperationID: "op-sysproxy-1",
		IfRevision:  &revision,
		Force:       true,
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"operation_id":"op-sysproxy-1","if_revision":12,"force":true}`
	if string(raw) != want {
		t.Fatalf("json=%s want=%s", raw, want)
	}

	var got SystemProxyMutationRequest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.OperationID != req.OperationID || got.Force != true || got.IfRevision == nil || *got.IfRevision != 12 {
		t.Fatalf("got=%#v", got)
	}
}

func TestSystemProxyMutationRequest_OmitEmptyOptionalFields(t *testing.T) {
	req := SystemProxyMutationRequest{OperationID: "op-sysproxy-2"}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"operation_id":"op-sysproxy-2"}`
	if string(raw) != want {
		t.Fatalf("json=%s want=%s", raw, want)
	}
}
