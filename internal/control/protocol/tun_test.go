package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestTunStatus_StableJSONContract(t *testing.T) {
	live := true
	status := TunStatus{
		Schema:        "mihari/v1",
		Revision:      12,
		DesiredEnable: true,
		LiveEnable:    &live,
		Stack:         "gVisor",
		Managed:       true,
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema":"mihari/v1","revision":12,"desired_enable":true,"live_enable":true,"stack":"gVisor","managed":true}`
	if string(raw) != want {
		t.Fatalf("json=%s want=%s", raw, want)
	}

	var got TunStatus
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != status.Schema || got.Revision != status.Revision ||
		got.DesiredEnable != status.DesiredEnable || got.Stack != status.Stack ||
		got.Managed != status.Managed || got.LiveEnable == nil || !*got.LiveEnable {
		t.Fatalf("got=%#v want=%#v", got, status)
	}
}

func TestTunStatus_OmitEmptyOptionalFields(t *testing.T) {
	status := TunStatus{
		Schema:        "mihari/v1",
		Revision:      1,
		DesiredEnable: false,
		Managed:       false,
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema":"mihari/v1","revision":1,"desired_enable":false,"managed":false}`
	if string(raw) != want {
		t.Fatalf("json=%s want=%s", raw, want)
	}
}

func TestTunMutationRequest_StableJSONContract(t *testing.T) {
	revision := uint64(12)
	req := TunMutationRequest{
		OperationID: "op-tun-1",
		IfRevision:  &revision,
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"operation_id":"op-tun-1","if_revision":12}`
	if string(raw) != want {
		t.Fatalf("json=%s want=%s", raw, want)
	}

	var got TunMutationRequest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, req) {
		t.Fatalf("got=%#v want=%#v", got, req)
	}
}

func TestTunMutationRequest_OmitEmptyOptionalFields(t *testing.T) {
	req := TunMutationRequest{OperationID: "op-tun-2"}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"operation_id":"op-tun-2"}`
	if string(raw) != want {
		t.Fatalf("json=%s want=%s", raw, want)
	}
}
