package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoutingPatch_ValidatesBeforeMutation(t *testing.T) {
	for _, test := range []struct {
		body   string
		method string
		status int
	}{
		{`{"mode":"global"}`, http.MethodPatch, 204},
		{`{"mode":"rule"}`, http.MethodPatch, 204},
		{`{"mode":"direct"}`, http.MethodPatch, 204},
		{`{"mode":"proxy"}`, http.MethodPatch, 400},
		{`{"mode":null}`, http.MethodPatch, 400},
		{`{"mode":3}`, http.MethodPatch, 400},
		{`{"mode":"global","tun":{"enable":true}}`, http.MethodPatch, 403},
		{`{"mode":"global","unknown":true}`, http.MethodPatch, 403},
		{`{"mode":"global","secret":"fixture"}`, http.MethodPatch, 403},
		{`{"mode":"global"} {}`, http.MethodPatch, 400},
		{`{"mode":"global"}`, http.MethodPut, 403},
	} {
		t.Run(test.method+test.body, func(t *testing.T) {
			mutator := &recordingMutator{}
			s := &Server{Mutator: mutator}
			w := httptest.NewRecorder()
			s.handleConfigMutation(w, httptest.NewRequest(test.method, "/configs", strings.NewReader(test.body)))
			if w.Code != test.status {
				t.Fatalf("status=%d want=%d", w.Code, test.status)
			}
			if (mutator.lastPatch() != nil) != (test.status == 204) {
				t.Fatal("unexpected mutation side effect")
			}
		})
	}
}
