package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDataResetResultUsesStableSafeJSON(t *testing.T) {
	raw, err := json.Marshal(DataResetResult{
		Schema: "mihari/v1", OperationID: "reset-1", Revision: 8, SetupRequired: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.Contains(got, `"setup_required":true`) || strings.Contains(got, "secret") || strings.Contains(got, "token") {
		t.Fatalf("json=%s", got)
	}
}
