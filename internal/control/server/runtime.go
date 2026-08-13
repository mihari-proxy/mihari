package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"sort"

	"github.com/coder/websocket"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/geoip"
	"github.com/mihari-proxy/mihari/internal/mihomo"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

const maxControlBodySize = 1 << 20

type RuntimeAPI interface {
	Capabilities() []string
	Snapshot() state.Snapshot
	Install(context.Context, runtimeapi.Operation) (core.InstallResult, error)
	Restart(context.Context, runtimeapi.Operation) error
	Proxies(context.Context) (mihomo.Proxies, error)
	SelectProxy(context.Context, runtimeapi.Operation, string, string) error
	DelayGroup(context.Context, string, string, int) (mihomo.Delays, error)
	DelayProxy(context.Context, string, string, int) (uint16, error)
	Connections(context.Context) (mihomo.Connections, error)
	CloseConnection(context.Context, runtimeapi.Operation, string) error
	CloseAllConnections(context.Context, runtimeapi.Operation) error
	Rules(context.Context) (mihomo.Rules, error)
	RuleProviders(context.Context) (mihomo.RuleProviders, error)
	UpdateRuleProvider(context.Context, runtimeapi.Operation, string) error
	Stream(context.Context, mihomo.StreamKind, func(json.RawMessage) error) error
	GeoIPStatus(context.Context) (geoip.Status, error)
	LookupGeoIP(context.Context, []netip.Addr) ([]geoip.Record, error)
	UpdateGeoIP(context.Context, runtimeapi.Operation) (geoip.Status, error)
	SystemProxyStatus(context.Context) (protocol.SystemProxyStatus, error)
	EnableSystemProxy(context.Context, runtimeapi.Operation, bool) (protocol.SystemProxyStatus, error)
	DisableSystemProxy(context.Context, runtimeapi.Operation) (protocol.SystemProxyStatus, error)
	TunStatus(context.Context) (protocol.TunStatus, error)
	EnableTun(context.Context, runtimeapi.Operation, bool) (protocol.TunStatus, error)
	DisableTun(context.Context, runtimeapi.Operation) (protocol.TunStatus, error)
}

// localCoreAPI is the optional read-only local-core readiness contract. Runtimes
// that implement it let GET /v1/core surface onboarding "use existing" hints
// without a network install; runtimes without it simply omit the fields. Mirrors
// the onboardingAPI / preferencesAPI optional-capability pattern (design §6.2).
type localCoreAPI interface {
	LocalCore(context.Context) (core.LocalCoreInfo, error)
}

func (s *Server) runtimeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/core", s.coreStatus)
	mux.HandleFunc("POST /v1/core/install", s.installCore)
	mux.HandleFunc("POST /v1/core/restart", s.restartCore)
	mux.HandleFunc("GET /v1/proxies", s.proxies)
	mux.HandleFunc("PUT /v1/proxy-groups/{name}", s.selectProxy)
	mux.HandleFunc("POST /v1/proxy-groups/{name}/delay-test", s.delayTest)
	mux.HandleFunc("POST /v1/proxies/{name}/delay-test", s.delayProxy)
	mux.HandleFunc("GET /v1/connections", s.connections)
	mux.HandleFunc("DELETE /v1/connections/{id}", s.closeConnection)
	mux.HandleFunc("DELETE /v1/connections", s.closeAllConnections)
	mux.HandleFunc("GET /v1/rules", s.rules)
	mux.HandleFunc("GET /v1/rule-providers", s.ruleProviders)
	mux.HandleFunc("POST /v1/rule-providers/{name}/update", s.updateRuleProvider)
	mux.HandleFunc("GET /v1/streams/{kind}", s.stream)
	s.subscriptionRoutes(mux)
	s.preferencesRoutes(mux)
	s.geoIPRoutes(mux)
	s.systemProxyRoutes(mux)
	s.tunRoutes(mux)
	s.onboardingRoutes(mux)
	s.webGUIRoutes(mux)
	s.serviceRoutes(mux)
}

func (s *Server) coreStatus(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	snapshot := s.runtime.Snapshot()
	status := coreStatusDTO(snapshot)
	if runtime, ok := s.runtime.(localCoreAPI); ok {
		if info, err := runtime.LocalCore(request.Context()); err == nil {
			status.LocalReady = info.Ready
			status.LocalVersion = info.Version
		}
	}
	writeJSON(writer, http.StatusOK, status)
}

func (s *Server) installCore(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	var body protocol.MutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	result, err := s.runtime.Install(request.Context(), runtimeapi.Operation{ID: body.OperationID, Source: mutationSource(body.Source), IfRevision: body.IfRevision, Channel: body.Channel})
	if err != nil {
		writeControlError(writer, err)
		return
	}
	snapshot := s.runtime.Snapshot()
	writeJSON(writer, http.StatusOK, protocol.CoreInstallResult{
		Schema: "mihari/v1", Version: result.Version, Updated: result.Updated, Revision: snapshot.Revision, Channel: snapshot.Core.Channel,
	})
}

func (s *Server) restartCore(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	var body protocol.MutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	if err := s.runtime.Restart(request.Context(), runtimeapi.Operation{ID: body.OperationID, Source: "control", IfRevision: body.IfRevision}); err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, protocol.MutationResult{Schema: "mihari/v1", OperationID: body.OperationID, Revision: s.runtime.Snapshot().Revision})
}

func (s *Server) proxies(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	upstream, err := s.runtime.Proxies(request.Context())
	if err != nil {
		writeControlError(writer, err)
		return
	}
	// Preserve mihomo/config order: follow GLOBAL.All when present. Do not sort
	// alphabetically — panel UIs expect subscription default group order.
	groups := orderedProxyGroups(upstream.Proxies)
	writeJSON(writer, http.StatusOK, protocol.ProxyGroups{Schema: "mihari/v1", Groups: groups})
}

func orderedProxyGroups(proxies map[string]mihomo.Proxy) []protocol.ProxyGroup {
	isGroup := func(p mihomo.Proxy) bool { return len(p.All) > 0 }
	toGroup := func(proxy mihomo.Proxy) protocol.ProxyGroup {
		nodes := make([]protocol.ProxyNode, 0, len(proxy.All))
		for _, name := range proxy.All {
			node := proxies[name]
			nodes = append(nodes, protocol.ProxyNode{Name: name, Type: node.Type, UDP: node.UDP, XUDP: node.XUDP})
		}
		return protocol.ProxyGroup{
			Name: proxy.Name, Type: proxy.Type, Now: proxy.Now,
			All: append([]string(nil), proxy.All...), Nodes: nodes,
		}
	}

	seen := make(map[string]bool, len(proxies))
	groups := make([]protocol.ProxyGroup, 0, len(proxies))
	if global, ok := proxies["GLOBAL"]; ok {
		for _, name := range global.All {
			proxy, ok := proxies[name]
			if !ok || !isGroup(proxy) || seen[name] {
				continue
			}
			seen[name] = true
			groups = append(groups, toGroup(proxy))
		}
	}
	// Append any remaining groups (e.g. when GLOBAL is missing). Order among
	// leftovers is unspecified map order, but keeps completeness.
	for name, proxy := range proxies {
		if !isGroup(proxy) || seen[name] {
			continue
		}
		groups = append(groups, toGroup(proxy))
	}
	return groups
}

func (s *Server) selectProxy(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	var body protocol.ProxySelectionRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	if body.Name == "" {
		writeInvalidArgument(writer, "proxy name is required")
		return
	}
	if err := s.runtime.SelectProxy(request.Context(), runtimeapi.Operation{ID: body.OperationID, Source: "control"}, request.PathValue("name"), body.Name); err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, protocol.MutationResult{Schema: "mihari/v1", OperationID: body.OperationID})
}

func (s *Server) delayTest(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	var body protocol.DelayTestRequest
	if !decodeControlJSON(writer, request, &body) {
		return
	}
	if body.URL == "" || body.TimeoutMilliseconds <= 0 || body.TimeoutMilliseconds > 60_000 {
		writeInvalidArgument(writer, "delay test URL and timeout are invalid")
		return
	}
	delays, err := s.runtime.DelayGroup(request.Context(), request.PathValue("name"), body.URL, body.TimeoutMilliseconds)
	if err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, protocol.DelayResult{Schema: "mihari/v1", Delays: map[string]uint16(delays)})
}

func (s *Server) delayProxy(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	var body protocol.DelayTestRequest
	if !decodeControlJSON(writer, request, &body) {
		return
	}
	if body.URL == "" || body.TimeoutMilliseconds <= 0 || body.TimeoutMilliseconds > 60_000 {
		writeInvalidArgument(writer, "delay test URL and timeout are invalid")
		return
	}
	name := request.PathValue("name")
	delay, err := s.runtime.DelayProxy(request.Context(), name, body.URL, body.TimeoutMilliseconds)
	if err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, protocol.DelayResult{Schema: "mihari/v1", Delays: map[string]uint16{name: delay}})
}

func (s *Server) connections(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	upstream, err := s.runtime.Connections(request.Context())
	if err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, connectionListDTO(upstream))
}

func connectionListDTO(upstream mihomo.Connections) protocol.ConnectionList {
	connections := make([]protocol.Connection, 0, len(upstream.Connections))
	for _, connection := range upstream.Connections {
		connections = append(connections, protocol.Connection{
			ID: connection.ID, Start: connection.Start, Upload: connection.Upload, Download: connection.Download,
			Chains: append([]string(nil), connection.Chains...), Rule: connection.Rule, RulePay: connection.RulePay,
			Metadata: protocol.ConnectionMetadata{
				Network: connection.Metadata.Network, Type: connection.Metadata.Type, SourceIP: connection.Metadata.SourceIP,
				DestinationIP: connection.Metadata.DestinationIP, SourcePort: connection.Metadata.SourcePort,
				DestinationPort: connection.Metadata.DestinationPort, Host: connection.Metadata.Host,
				Process: connection.Metadata.Process, ProcessPath: connection.Metadata.ProcessPath,
				InboundName: connection.Metadata.InboundName, InboundUser: connection.Metadata.InboundUser,
				SniffHost: connection.Metadata.SniffHost, RemoteDestination: connection.Metadata.RemoteDestination,
			},
		})
	}
	return protocol.ConnectionList{
		Schema: "mihari/v1", DownloadTotal: upstream.DownloadTotal, UploadTotal: upstream.UploadTotal, Connections: connections,
	}
}

func (s *Server) closeConnection(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	var body protocol.MutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	if err := s.runtime.CloseConnection(request.Context(), runtimeapi.Operation{ID: body.OperationID, Source: "control"}, request.PathValue("id")); err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, protocol.MutationResult{Schema: "mihari/v1", OperationID: body.OperationID})
}

func (s *Server) closeAllConnections(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	var body protocol.MutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	if err := s.runtime.CloseAllConnections(request.Context(), runtimeapi.Operation{ID: body.OperationID, Source: "control"}); err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, protocol.MutationResult{Schema: "mihari/v1", OperationID: body.OperationID})
}

func (s *Server) rules(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	upstream, err := s.runtime.Rules(request.Context())
	if err != nil {
		writeControlError(writer, err)
		return
	}
	rules := make([]protocol.Rule, 0, len(upstream.Rules))
	for _, rule := range upstream.Rules {
		rules = append(rules, protocol.Rule{Type: rule.Type, Payload: rule.Payload, Proxy: rule.Proxy})
	}
	writeJSON(writer, http.StatusOK, protocol.RuleList{Schema: "mihari/v1", Rules: rules})
}

func (s *Server) ruleProviders(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	upstream, err := s.runtime.RuleProviders(request.Context())
	if err != nil {
		writeControlError(writer, err)
		return
	}
	names := make([]string, 0, len(upstream.Providers))
	for name := range upstream.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	providers := make([]protocol.RuleProvider, 0, len(names))
	for _, name := range names {
		provider := upstream.Providers[name]
		typeName := provider.VehicleType
		if typeName == "" {
			typeName = provider.Type
		}
		providers = append(providers, protocol.RuleProvider{
			Name: provider.Name, Type: typeName, Behavior: provider.Behavior, Format: provider.Format,
			RuleCount: provider.RuleCount, UpdatedAt: provider.UpdatedAt, Status: "Ready",
		})
	}
	writeJSON(writer, http.StatusOK, protocol.RuleProviderList{Schema: "mihari/v1", Revision: s.runtime.Snapshot().Revision, Providers: providers})
}

func (s *Server) updateRuleProvider(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	name := request.PathValue("name")
	if name == "" {
		writeInvalidArgument(writer, "rule provider name is required")
		return
	}
	var body protocol.MutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	if err := s.runtime.UpdateRuleProvider(request.Context(), runtimeapi.Operation{ID: body.OperationID, Source: "control", IfRevision: body.IfRevision}, name); err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, protocol.MutationResult{
		Schema: "mihari/v1", OperationID: body.OperationID, Revision: s.runtime.Snapshot().Revision,
	})
}

func (s *Server) stream(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	kind := mihomo.StreamKind(request.PathValue("kind"))
	if kind != mihomo.StreamTraffic && kind != mihomo.StreamMemory && kind != mihomo.StreamLogs && kind != mihomo.StreamConnections {
		writeInvalidArgument(writer, "unsupported stream")
		return
	}
	connection, err := websocket.Accept(writer, request, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	err = s.runtime.Stream(request.Context(), kind, func(message json.RawMessage) error {
		if kind == mihomo.StreamConnections {
			var upstream mihomo.Connections
			if err := json.Unmarshal(message, &upstream); err != nil {
				return err
			}
			mapped, err := json.Marshal(connectionListDTO(upstream))
			if err != nil {
				return err
			}
			message = mapped
		}
		event, err := json.Marshal(protocol.StreamEvent{
			Schema:     "mihari/v1",
			Stream:     string(kind),
			ObservedAt: s.now().UTC(),
			Data:       message,
		})
		if err != nil {
			return err
		}
		return connection.Write(request.Context(), websocket.MessageText, event)
	})
	if err == nil {
		_ = connection.Close(websocket.StatusNormalClosure, "stream complete")
		return
	}
	_ = connection.Close(websocket.StatusInternalError, "stream failed")
}

func (s *Server) requireRuntime(writer http.ResponseWriter) bool {
	if s.runtime != nil {
		return true
	}
	writeControlError(writer, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo runtime is unavailable"})
	return false
}

func coreStatusDTO(snapshot state.Snapshot) protocol.CoreStatus {
	return protocol.CoreStatus{
		Schema: "mihari/v1", Revision: snapshot.Revision, Status: snapshot.Core.Status, Version: snapshot.Core.Version, Channel: snapshot.Core.Channel,
		PID: snapshot.Core.PID, Restarts: snapshot.Core.Restarts, LastError: snapshot.Core.LastError, NextRetryAt: snapshot.Core.NextRetryAt,
	}
}

func decodeControlJSON(writer http.ResponseWriter, request *http.Request, target any) bool {
	body := http.MaxBytesReader(writer, request.Body, maxControlBodySize)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeInvalidArgument(writer, "invalid request body")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeInvalidArgument(writer, "request body must contain one JSON object")
		return false
	}
	return true
}

func requireOperationID(writer http.ResponseWriter, operationID string) bool {
	if operationID != "" {
		return true
	}
	writeInvalidArgument(writer, "operation_id is required")
	return false
}

// mutationSource 归一化请求来源：空值默认 "control"。只有 setup 流程显式传 "setup"
// 以触发 daemon 侧本地预检短路；其余 mutation 保持 "control" 语义（design §4.3）。
func mutationSource(raw string) string {
	if raw == "" {
		return "control"
	}
	return raw
}

func writeInvalidArgument(writer http.ResponseWriter, message string) {
	writeJSON(writer, http.StatusBadRequest, protocol.NewError(protocol.CodeInvalidArgument, message, nil))
}

func writeControlError(writer http.ResponseWriter, err error) {
	var apiError protocol.APIError
	if !errors.As(err, &apiError) {
		apiError = protocol.APIError{Code: protocol.CodeInternal, Message: "internal error"}
	}
	status := http.StatusInternalServerError
	switch apiError.Code {
	case protocol.CodeInvalidArgument:
		status = http.StatusBadRequest
	case protocol.CodePermissionDenied:
		status = http.StatusForbidden
	case protocol.CodeRevisionConflict, protocol.CodeInvalidState,
		protocol.CodeSystemProxyConflict, protocol.CodeSystemProxyNotOwned,
		protocol.CodeTunConflict:
		status = http.StatusConflict
	case protocol.CodeDataFailure:
		status = http.StatusUnprocessableEntity
	case protocol.CodeDaemonUnavailable, protocol.CodeUpstreamFailure, protocol.CodeNetworkFailure:
		status = http.StatusBadGateway
	}
	writeJSON(writer, status, protocol.NewError(apiError.Code, apiError.Message, apiError.Details))
}
