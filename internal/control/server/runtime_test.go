package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/core"
	"github.com/LeeShunEE/mihari/internal/mihomo"
	runtimeapi "github.com/LeeShunEE/mihari/internal/runtime"
	"github.com/LeeShunEE/mihari/internal/state"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestCoreEndpointReturnsRedactedStableStatus(t *testing.T) {
	store := state.NewStore(state.Snapshot{Revision: 8, Core: state.CoreState{Status: "running", Version: "v1.19.0", PID: 42, Restarts: 1}})
	server := New(Options{Token: "token", Store: store, Runtime: &fakeRuntime{snapshot: store.Load()}})
	request := authorizedRequest(http.MethodGet, "/v1/core", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var status protocol.CoreStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Schema != "mihari/v1" || status.Revision != 8 || status.Status != "running" || status.Version != "v1.19.0" || status.PID != 42 {
		t.Fatalf("status=%#v", status)
	}
}

func TestRuntimeMutationRoutesThroughManager(t *testing.T) {
	fake := &fakeRuntime{}
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: fake})
	request := authorizedRequest(http.MethodPut, "/v1/proxy-groups/Auto%2FSelect", bytes.NewBufferString(`{"operation_id":"op-1","name":"Node A"}`))
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fake.selectedGroup != "Auto/Select" || fake.selectedName != "Node A" || fake.operation.ID != "op-1" {
		t.Fatalf("fake=%#v", fake)
	}
	var result protocol.MutationResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Schema != "mihari/v1" || result.OperationID != "op-1" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestProxyDelayEndpointTestsOneNode(t *testing.T) {
	fake := &fakeRuntime{proxyDelay: 42}
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: fake})
	request := authorizedRequest(http.MethodPost, "/v1/proxies/Node%20A/delay-test", bytes.NewBufferString(
		`{"url":"https://example.com/ping","timeout_ms":3500}`,
	))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.delayedProxy != "Node A" {
		t.Fatalf("proxy=%q", fake.delayedProxy)
	}
	var result protocol.DelayResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Delays["Node A"] != 42 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestInstallAndQueryEndpoints(t *testing.T) {
	fake := &fakeRuntime{
		installResult: core.InstallResult{Version: "v1.19.0", Updated: true},
		proxies: mihomo.Proxies{Proxies: map[string]mihomo.Proxy{
			"GLOBAL": {Name: "GLOBAL", Type: "Selector", Now: "DIRECT", All: []string{"DIRECT"}},
		}},
		connections: mihomo.Connections{DownloadTotal: 2, UploadTotal: 3, Connections: []mihomo.Connection{{ID: "abc"}}},
		rules:       mihomo.Rules{Rules: []mihomo.Rule{{Type: "MATCH", Proxy: "DIRECT"}}},
	}
	store := state.NewStore(state.Snapshot{Revision: 11})
	fake.snapshot = store.Load()
	server := New(Options{Token: "token", Store: store, Runtime: fake})
	tests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/v1/core/install", `{"operation_id":"install-1"}`},
		{http.MethodGet, "/v1/proxies", ""},
		{http.MethodPost, "/v1/proxy-groups/GLOBAL/delay-test", `{"url":"https://example.com/ping","timeout_ms":3000}`},
		{http.MethodGet, "/v1/connections", ""},
		{http.MethodDelete, "/v1/connections/abc", `{"operation_id":"close-1"}`},
		{http.MethodDelete, "/v1/connections", `{"operation_id":"close-all-1"}`},
		{http.MethodGet, "/v1/rules", ""},
	}
	for _, test := range tests {
		request := authorizedRequest(test.method, test.path, bytes.NewBufferString(test.body))
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Errorf("%s %s status=%d body=%s", test.method, test.path, recorder.Code, recorder.Body.String())
		}
		if !bytes.Contains(recorder.Body.Bytes(), []byte(`"schema":"mihari/v1"`)) {
			t.Errorf("%s %s body=%s", test.method, test.path, recorder.Body.String())
		}
	}
}

func TestProxiesMapsNodeProtocolMetadata(t *testing.T) {
	fake := &fakeRuntime{proxies: mihomo.Proxies{Proxies: map[string]mihomo.Proxy{
		"GLOBAL": {Name: "GLOBAL", Type: "Selector", Now: "node-a", All: []string{"node-a"}},
		"node-a": {Name: "node-a", Type: "VLESS", UDP: true, XUDP: true},
	}}}
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: fake})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodGet, "/v1/proxies", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var got protocol.ProxyGroups
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Groups) != 1 || len(got.Groups[0].Nodes) != 1 {
		t.Fatalf("groups=%#v", got.Groups)
	}
	node := got.Groups[0].Nodes[0]
	if node.Name != "node-a" || node.Type != "VLESS" || !node.UDP || !node.XUDP {
		t.Fatalf("node=%#v", node)
	}
}

func TestRuntimeRequestRejectsUnknownOrOversizedJSON(t *testing.T) {
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: &fakeRuntime{}})
	for _, body := range []string{
		`{"operation_id":"op","name":"DIRECT","unknown":true}`,
		`{"operation_id":"op","name":"` + string(bytes.Repeat([]byte{'x'}, maxControlBodySize)) + `"}`,
	} {
		request := authorizedRequest(http.MethodPut, "/v1/proxy-groups/GLOBAL", bytes.NewBufferString(body))
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var envelope protocol.ErrorEnvelope
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != protocol.CodeInvalidArgument {
			t.Fatalf("envelope=%#v err=%v", envelope, err)
		}
	}
}

func TestRuntimeStreamWrapsEveryUpstreamMessage(t *testing.T) {
	fake := &fakeRuntime{streamMessages: []json.RawMessage{json.RawMessage(`{"up":1,"down":2}`)}}
	fixed := time.Date(2026, time.August, 3, 12, 30, 0, 0, time.FixedZone("test", 8*60*60))
	control := New(Options{
		Token:   "token",
		Store:   state.NewStore(state.Snapshot{}),
		Runtime: fake,
		Now:     func() time.Time { return fixed },
	})
	server := httptest.NewServer(control.Handler())
	defer server.Close()
	header := http.Header{}
	header.Set("Authorization", "Bearer token")
	connection, _, err := websocket.Dial(context.Background(), "ws"+server.URL[len("http"):]+"/v1/streams/traffic", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var event protocol.StreamEvent
	if err := wsjson.Read(ctx, connection, &event); err != nil {
		t.Fatal(err)
	}
	if event.Schema != "mihari/v1" || event.Stream != "traffic" || string(event.Data) != `{"up":1,"down":2}` {
		t.Fatalf("event=%#v", event)
	}
	if !event.ObservedAt.Equal(fixed) || event.ObservedAt.Location() != time.UTC {
		t.Fatalf("observed_at=%v want=%v", event.ObservedAt, fixed.UTC())
	}
}

func authorizedRequest(method, path string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("Authorization", "Bearer token")
	return request
}

type fakeRuntime struct {
	capabilities   []string
	snapshot       state.Snapshot
	operation      runtimeapi.Operation
	selectedGroup  string
	selectedName   string
	installResult  core.InstallResult
	proxies        mihomo.Proxies
	connections    mihomo.Connections
	rules          mihomo.Rules
	streamMessages []json.RawMessage
	delayedProxy   string
	proxyDelay     uint16
}

func (f *fakeRuntime) Capabilities() []string { return append([]string(nil), f.capabilities...) }

func (f *fakeRuntime) Snapshot() state.Snapshot { return f.snapshot }

func (f *fakeRuntime) Install(_ context.Context, operation runtimeapi.Operation) (core.InstallResult, error) {
	f.operation = operation
	return f.installResult, nil
}

func (f *fakeRuntime) Restart(_ context.Context, operation runtimeapi.Operation) error {
	f.operation = operation
	return nil
}

func (f *fakeRuntime) Proxies(context.Context) (mihomo.Proxies, error) { return f.proxies, nil }

func (f *fakeRuntime) SelectProxy(_ context.Context, operation runtimeapi.Operation, group, name string) error {
	f.operation = operation
	f.selectedGroup = group
	f.selectedName = name
	return nil
}

func (f *fakeRuntime) DelayGroup(context.Context, string, string, int) (mihomo.Delays, error) {
	return mihomo.Delays{"DIRECT": 1}, nil
}

func (f *fakeRuntime) DelayProxy(_ context.Context, name, _ string, _ int) (uint16, error) {
	f.delayedProxy = name
	return f.proxyDelay, nil
}

func (f *fakeRuntime) Connections(context.Context) (mihomo.Connections, error) {
	return f.connections, nil
}

func (f *fakeRuntime) CloseConnection(_ context.Context, operation runtimeapi.Operation, _ string) error {
	f.operation = operation
	return nil
}

func (f *fakeRuntime) CloseAllConnections(_ context.Context, operation runtimeapi.Operation) error {
	f.operation = operation
	return nil
}

func (f *fakeRuntime) Rules(context.Context) (mihomo.Rules, error) { return f.rules, nil }

func (f *fakeRuntime) Stream(ctx context.Context, _ mihomo.StreamKind, receive func(json.RawMessage) error) error {
	for _, message := range f.streamMessages {
		if err := receive(message); err != nil {
			return err
		}
	}
	return nil
}
