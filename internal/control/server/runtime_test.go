package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/core"
	"github.com/LeeShunEE/mihari/internal/geoip"
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
		ruleProviders: mihomo.RuleProviders{Providers: map[string]mihomo.RuleProvider{
			"OpenAI": {Name: "OpenAI", Type: "Rule", VehicleType: "HTTP", Behavior: "Classical", Format: "YamlRule", RuleCount: 12, UpdatedAt: time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC)},
		}},
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
		{http.MethodGet, "/v1/rule-providers", ""},
		{http.MethodPost, "/v1/rule-providers/OpenAI/update", `{"operation_id":"provider-1","if_revision":11}`},
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
	if fake.updatedRuleProvider != "OpenAI" || fake.operation.ID != "provider-1" || fake.operation.IfRevision == nil || *fake.operation.IfRevision != 11 {
		t.Fatalf("provider=%q operation=%#v", fake.updatedRuleProvider, fake.operation)
	}
}

func TestRuleProvidersExposeSafeTypedFields(t *testing.T) {
	fake := &fakeRuntime{ruleProviders: mihomo.RuleProviders{Providers: map[string]mihomo.RuleProvider{
		"OpenAI": {Name: "OpenAI", Type: "Rule", VehicleType: "HTTP", Behavior: "Classical", Format: "YamlRule", RuleCount: 12, UpdatedAt: time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC)},
	}}}
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: fake})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodGet, "/v1/rule-providers", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var got protocol.RuleProviderList
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Providers) != 1 || got.Providers[0].Type != "HTTP" || got.Providers[0].Status != "Ready" {
		t.Fatalf("providers=%#v", got.Providers)
	}
	for _, forbidden := range []string{"url", "secret", "controller"} {
		if strings.Contains(strings.ToLower(response.Body.String()), forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, response.Body.String())
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

func TestConnectionsMapsCompleteSafeMetadataAndChain(t *testing.T) {
	started := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	fake := &fakeRuntime{connections: mihomo.Connections{Connections: []mihomo.Connection{{
		ID: "connection-1", Start: started, Upload: 10, Download: 20,
		Chains: []string{"GLOBAL", "Streaming", "Auto Select", "Japan 01"}, Rule: "RuleSet", RulePay: "OpenAI",
		Metadata: mihomo.ConnectionMetadata{
			Network: "tcp", Type: "HTTPS", SourceIP: "127.0.0.1", SourcePort: "46154",
			DestinationIP: "172.64.155.209", DestinationPort: "443", Host: "chatgpt.com",
			Process: "codex.exe", ProcessPath: `C:\tools\codex.exe`, InboundName: "DEFAULT-MIXED",
			InboundUser: "local", SniffHost: "chatgpt.com", RemoteDestination: "103.73.220.63:443",
		},
	}}}}
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: fake})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodGet, "/v1/connections", nil))
	var got protocol.ConnectionList
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Connections) != 1 {
		t.Fatalf("connections=%#v", got.Connections)
	}
	connection := got.Connections[0]
	if !connection.Start.Equal(started) || !slices.Equal(connection.Chains, []string{"GLOBAL", "Streaming", "Auto Select", "Japan 01"}) ||
		connection.Metadata.Process != "codex.exe" || connection.Metadata.ProcessPath == "" ||
		connection.Metadata.InboundName != "DEFAULT-MIXED" || connection.Metadata.InboundUser != "local" ||
		connection.Metadata.SniffHost != "chatgpt.com" || connection.Metadata.RemoteDestination != "103.73.220.63:443" {
		t.Fatalf("connection=%#v", connection)
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

func TestConnectionStreamMapsSafeDTOAndCompleteChain(t *testing.T) {
	fake := &fakeRuntime{streamMessages: []json.RawMessage{json.RawMessage(`{
		"downloadTotal":20,"uploadTotal":10,"connections":[{
			"id":"connection-1","chains":["GLOBAL","Auto Select","Japan 01"],
			"metadata":{"sourceIP":"127.0.0.1","host":"example.com","processPath":"C:\\\\tools\\\\client.exe"}
		}]
	}`)}}
	control := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: fake})
	server := httptest.NewServer(control.Handler())
	defer server.Close()
	header := http.Header{}
	header.Set("Authorization", "Bearer token")
	connection, _, err := websocket.Dial(context.Background(), "ws"+server.URL[len("http"):]+"/v1/streams/connections", &websocket.DialOptions{HTTPHeader: header})
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
	var got protocol.ConnectionList
	if err := json.Unmarshal(event.Data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != "mihari/v1" || got.DownloadTotal != 20 || len(got.Connections) != 1 {
		t.Fatalf("connections=%#v", got)
	}
	item := got.Connections[0]
	if !slices.Equal(item.Chains, []string{"GLOBAL", "Auto Select", "Japan 01"}) || item.Metadata.SourceIP != "127.0.0.1" || item.Metadata.ProcessPath == "" {
		t.Fatalf("connection=%#v", item)
	}
}

func authorizedRequest(method, path string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("Authorization", "Bearer token")
	return request
}

type fakeRuntime struct {
	capabilities          []string
	snapshot              state.Snapshot
	operation             runtimeapi.Operation
	selectedGroup         string
	selectedName          string
	installResult         core.InstallResult
	proxies               mihomo.Proxies
	connections           mihomo.Connections
	rules                 mihomo.Rules
	ruleProviders         mihomo.RuleProviders
	updatedRuleProvider   string
	streamMessages        []json.RawMessage
	delayedProxy          string
	proxyDelay            uint16
	geoIPStatus           geoip.Status
	geoIPRecords          []geoip.Record
	systemProxyStatus     protocol.SystemProxyStatus
	systemProxyForce      bool
	disableSystemProxyErr error
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

func (f *fakeRuntime) RuleProviders(context.Context) (mihomo.RuleProviders, error) {
	return f.ruleProviders, nil
}

func (f *fakeRuntime) UpdateRuleProvider(_ context.Context, operation runtimeapi.Operation, name string) error {
	f.operation = operation
	f.updatedRuleProvider = name
	return nil
}

func (f *fakeRuntime) Stream(ctx context.Context, _ mihomo.StreamKind, receive func(json.RawMessage) error) error {
	for _, message := range f.streamMessages {
		if err := receive(message); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeRuntime) GeoIPStatus(context.Context) (geoip.Status, error) { return f.geoIPStatus, nil }

func (f *fakeRuntime) LookupGeoIP(context.Context, []netip.Addr) ([]geoip.Record, error) {
	return append([]geoip.Record(nil), f.geoIPRecords...), nil
}

func (f *fakeRuntime) UpdateGeoIP(_ context.Context, operation runtimeapi.Operation) (geoip.Status, error) {
	f.operation = operation
	return f.geoIPStatus, nil
}

func (f *fakeRuntime) SystemProxyStatus(context.Context) (protocol.SystemProxyStatus, error) {
	return f.systemProxyStatus, nil
}

func (f *fakeRuntime) EnableSystemProxy(_ context.Context, operation runtimeapi.Operation, force bool) (protocol.SystemProxyStatus, error) {
	f.operation = operation
	f.systemProxyForce = force
	return f.systemProxyStatus, nil
}

func (f *fakeRuntime) DisableSystemProxy(_ context.Context, operation runtimeapi.Operation) (protocol.SystemProxyStatus, error) {
	f.operation = operation
	if f.disableSystemProxyErr != nil {
		return protocol.SystemProxyStatus{}, f.disableSystemProxyErr
	}
	return f.systemProxyStatus, nil
}
