package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
)

type previewRuntime struct {
	fakeRuntime
	requests []protocol.ProxyAuthPreviewRequest
}

func (f *previewRuntime) PreviewProxyAuth(_ context.Context, request protocol.ProxyAuthPreviewRequest) (protocol.ProxyAuthPreview, error) {
	f.requests = append(f.requests, request)
	return protocol.ProxyAuthPreview{Revision: 7, Lines: []protocol.ProxyAuthPreviewLine{{Text: "authentication: []", Field: "authentication"}}}, nil
}

func TestProxyAuthPreview_LocalAuthenticatedReadOnlyRoute(t *testing.T) {
	fake := &previewRuntime{}
	s := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: fake})
	for _, authorized := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodPost, "/v1/proxy-auth/preview", bytes.NewBufferString(`{"config":{"mode":"disabled","bypass_mode":"follow","users":[],"bypass_prefixes":[]}}`))
		if authorized {
			request.Header.Set("Authorization", "Bearer token")
		}
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		if authorized && response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if !authorized && response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthorized status=%d", response.Code)
		}
		if authorized {
			var got protocol.ProxyAuthPreview
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Revision != 7 || len(got.Lines) != 1 || got.Lines[0].Text != "authentication: []" || got.Lines[0].Field != "authentication" {
				t.Fatalf("unexpected preview response: %+v", got)
			}
		}
	}
	if len(fake.requests) != 1 || fake.requests[0].Reveal || fake.requests[0].Config.Mode != "disabled" {
		t.Fatalf("requests=%+v", fake.requests)
	}
}
