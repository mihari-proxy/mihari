package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSubscriptionResponsesCannotEncodeURL(t *testing.T) {
	list := SubscriptionList{Schema: "mihari/v1", Subscriptions: []Subscription{{ID: "one", Name: "main"}}}
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "url") {
		t.Fatalf("response schema contains URL field: %s", raw)
	}
}

func TestSubscriptionUpdatePointersPreserveExplicitFalseAndEmpty(t *testing.T) {
	var request SubscriptionUpdateRequest
	if err := json.Unmarshal([]byte(`{"operation_id":"op","auto_refresh":false,"interval":""}`), &request); err != nil {
		t.Fatal(err)
	}
	if request.AutoRefresh == nil || *request.AutoRefresh || request.Interval == nil || *request.Interval != "" {
		t.Fatalf("lost explicit values: %#v", request)
	}
}
