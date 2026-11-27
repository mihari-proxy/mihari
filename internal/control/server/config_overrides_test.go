package server

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

type configOverrideRuntime struct {
	fakeRuntime
	category string
	preview  protocol.ConfigOverridesPreviewRequest
}

func (f *configOverrideRuntime) ConfigOverrides(context.Context) (protocol.ConfigOverridesStatus, error) {
	return protocol.ConfigOverridesStatus{Revision: 8}, nil
}
func (f *configOverrideRuntime) PreviewConfigOverrides(_ context.Context, req protocol.ConfigOverridesPreviewRequest) (protocol.ConfigOverridesPreview, error) {
	f.preview = req
	return protocol.ConfigOverridesPreview{Revision: 8}, nil
}
func (f *configOverrideRuntime) UpdateConfigOverrides(_ context.Context, _ runtimeapi.Operation, category string, _ protocol.ConfigOverridesUpdateRequest) (protocol.ConfigOverridesStatus, error) {
	f.category = category
	return protocol.ConfigOverridesStatus{Revision: 9}, nil
}

func TestConfigOverridesLocalRoutes(t *testing.T) {
	f := &configOverrideRuntime{}
	s := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: f})
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/v1/config-overrides", "", 200},
		{"PUT", "/v1/config-overrides/dns", `{"operation_id":"edit","if_revision":8,"dns":{}}`, 200},
		{"PUT", "/v1/config-overrides/fragments", `{"operation_id":"edit","if_revision":8,"fragments":[]}`, 200},
		{"PUT", "/v1/config-overrides/dns", `{"operation_id":"edit","fragments":[]}`, 400},
		{"PUT", "/v1/config-overrides/dns", `{"operation_id":"missing-draft"}`, 400},
		{"PUT", "/v1/config-overrides/fragments", `{"operation_id":"missing-draft","fragments":null}`, 400},
		{"POST", "/v1/config-overrides/preview", `{"category":"dns"}`, 200},
	} {
		req := authorizedRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("%s %s = %d: %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	if f.category != "fragments" {
		t.Fatalf("category=%q", f.category)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/config-overrides", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated=%d", w.Code)
	}
}

func TestConfigOverridesPreview_ContinuationFitsOriginalDraftRequestBudget(t *testing.T) {
	f := &configOverrideRuntime{}
	srv := httptest.NewServer(New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: f}).Handler())
	defer srv.Close()
	c := controlclient.NewHTTP(srv.URL, "token", srv.Client())
	req := protocol.ConfigOverridesPreviewRequest{Category: "fragments", Fragments: []protocol.OverrideFragment{{ID: "large", Name: "Office", YAML: "x"}}}
	head, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	req.Fragments[0].YAML += strings.Repeat("x", maxControlBodySize-len(head))
	if _, err = c.PreviewConfigOverrides(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	req.Offset, req.Snapshot = 52, "0/"+strings.Repeat("a", 64)
	if _, err = c.PreviewConfigOverrides(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if f.preview.Snapshot != req.Snapshot || f.preview.Offset != 52 || f.preview.Fragments[0].YAML != req.Fragments[0].YAML {
		t.Fatal("draft or cursor lost")
	}
}

func TestConfigOverridesPreview_ValidatesCursorAndAcceptsBodyOnlyClients(t *testing.T) {
	f := &configOverrideRuntime{}
	srv := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: f})
	for _, tc := range []struct {
		name, query, body string
		status            int
	}{
		{"body only", "", `{"category":"dns","offset":8,"snapshot":"s"}`, 200},
		{"matching query and body", "?offset=8&snapshot=s", `{"category":"dns","offset":8,"snapshot":"s"}`, 200},
		{"conflicting offset", "?offset=7&snapshot=s", `{"category":"dns","offset":8,"snapshot":"s"}`, 400},
		{"missing offset", "?snapshot=s", `{"category":"dns"}`, 400},
		{"duplicate snapshot", "?snapshot=s&snapshot=s&offset=8", `{"category":"dns"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.preview = protocol.ConfigOverridesPreviewRequest{}
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, authorizedRequest("POST", "/v1/config-overrides/preview"+tc.query, strings.NewReader(tc.body)))
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d", w.Code, tc.status)
			}
			if tc.status == 200 && (f.preview.Offset != 8 || f.preview.Snapshot != "s") {
				t.Fatalf("cursor=%+v", f.preview)
			}
			if tc.status == 400 && f.preview.Category != "" {
				t.Fatal("invalid cursor reached runtime")
			}
		})
	}
}
