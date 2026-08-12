package protocol

import (
	"encoding/json"
	"testing"
)

func TestServiceStatusMarshals(t *testing.T) {
	raw, err := json.Marshal(ServiceStatus{Schema: "mihari/v1", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"schema":"mihari/v1","status":"running"}` {
		t.Fatalf("json=%s", raw)
	}
	var got ServiceStatus
	if err := json.Unmarshal([]byte(`{"schema":"mihari/v1","status":"not_installed"}`), &got); err != nil || got.Status != "not_installed" {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}
