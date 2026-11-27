package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestConfigOverrides_ReadsEscapedStatusAfterGetAndCommittedPut(t *testing.T) {
	// settings and catalog each fit their storage bounds, but their JSON strings
	// expand independently when embedded in a single status response.
	status := protocol.ConfigOverridesStatus{ActiveName: strings.Repeat("<", 750000), Config: protocol.ConfigOverrides{Fragments: []protocol.OverrideFragment{{ID: "excerpt", Name: "Company", YAML: "# " + strings.Repeat(">", 750000)}}}}
	data, err := json.Marshal(status)
	if err != nil || len(data) <= maxControlResponseSize {
		t.Fatalf("invalid fixture: bytes=%d err=%v", len(data), err)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(data); err != nil {
			t.Logf("response interrupted: %v", err)
		}
	}))
	defer s.Close()
	c := NewHTTP(s.URL, "fixture-token", s.Client())
	for _, method := range []string{"GET", "PUT"} {
		t.Run(method, func(t *testing.T) {
			var got protocol.ConfigOverridesStatus
			var err error
			if method == "GET" {
				got, err = c.ConfigOverrides(t.Context())
			} else {
				got, err = c.UpdateConfigOverrides(t.Context(), "dns", protocol.ConfigOverridesUpdateRequest{OperationID: "fixture"})
			}
			if err != nil || got.ActiveName != status.ActiveName || len(got.Config.Fragments) != 1 || got.Config.Fragments[0].YAML != status.Config.Fragments[0].YAML {
				t.Fatalf("incomplete %s status: %v", method, err)
			}
		})
	}
}
