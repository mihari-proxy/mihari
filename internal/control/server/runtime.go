package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/core"
	"github.com/LeeShunEE/mihari/internal/mihomo"
	runtimeapi "github.com/LeeShunEE/mihari/internal/runtime"
	"github.com/LeeShunEE/mihari/internal/state"
	"github.com/coder/websocket"
)

const maxControlBodySize = 1 << 20

type RuntimeAPI interface {
	Snapshot() state.Snapshot
	Install(context.Context, runtimeapi.Operation) (core.InstallResult, error)
	Restart(context.Context, runtimeapi.Operation) error
	Proxies(context.Context) (mihomo.Proxies, error)
	SelectProxy(context.Context, runtimeapi.Operation, string, string) error
	DelayGroup(context.Context, string, string, int) (mihomo.Delays, error)
	Connections(context.Context) (mihomo.Connections, error)
	CloseConnection(context.Context, runtimeapi.Operation, string) error
	CloseAllConnections(context.Context, runtimeapi.Operation) error
	Rules(context.Context) (mihomo.Rules, error)
	Stream(context.Context, mihomo.StreamKind, func(json.RawMessage) error) error
}

func (s *Server) runtimeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/core", s.coreStatus)
	mux.HandleFunc("POST /v1/core/install", s.installCore)
	mux.HandleFunc("POST /v1/core/restart", s.restartCore)
	mux.HandleFunc("GET /v1/proxies", s.proxies)
	mux.HandleFunc("PUT /v1/proxy-groups/{name}", s.selectProxy)
	mux.HandleFunc("POST /v1/proxy-groups/{name}/delay-test", s.delayTest)
	mux.HandleFunc("GET /v1/connections", s.connections)
	mux.HandleFunc("DELETE /v1/connections/{id}", s.closeConnection)
	mux.HandleFunc("DELETE /v1/connections", s.closeAllConnections)
	mux.HandleFunc("GET /v1/rules", s.rules)
	mux.HandleFunc("GET /v1/streams/{kind}", s.stream)
	s.subscriptionRoutes(mux)
}

func (s *Server) coreStatus(writer http.ResponseWriter, _ *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	snapshot := s.runtime.Snapshot()
	writeJSON(writer, http.StatusOK, coreStatusDTO(snapshot))
}

func (s *Server) installCore(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	var body protocol.MutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	result, err := s.runtime.Install(request.Context(), runtimeapi.Operation{ID: body.OperationID, Source: "control", IfRevision: body.IfRevision})
	if err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, protocol.CoreInstallResult{
		Schema: "mihari/v1", Version: result.Version, Updated: result.Updated, Revision: s.runtime.Snapshot().Revision,
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
	groups := make([]protocol.ProxyGroup, 0, len(upstream.Proxies))
	for _, proxy := range upstream.Proxies {
		if len(proxy.All) == 0 {
			continue
		}
		groups = append(groups, protocol.ProxyGroup{Name: proxy.Name, Type: proxy.Type, Now: proxy.Now, All: append([]string(nil), proxy.All...)})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	writeJSON(writer, http.StatusOK, protocol.ProxyGroups{Schema: "mihari/v1", Groups: groups})
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

func (s *Server) connections(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	upstream, err := s.runtime.Connections(request.Context())
	if err != nil {
		writeControlError(writer, err)
		return
	}
	connections := make([]protocol.Connection, 0, len(upstream.Connections))
	for _, connection := range upstream.Connections {
		connections = append(connections, protocol.Connection{
			ID: connection.ID, Upload: connection.Upload, Download: connection.Download,
			Chains: append([]string(nil), connection.Chains...), Rule: connection.Rule, RulePay: connection.RulePay,
			Metadata: protocol.ConnectionMetadata{
				Network: connection.Metadata.Network, Type: connection.Metadata.Type, SourceIP: connection.Metadata.SourceIP,
				DestinationIP: connection.Metadata.DestinationIP, SourcePort: connection.Metadata.SourcePort,
				DestinationPort: connection.Metadata.DestinationPort, Host: connection.Metadata.Host,
			},
		})
	}
	writeJSON(writer, http.StatusOK, protocol.ConnectionList{
		Schema: "mihari/v1", DownloadTotal: upstream.DownloadTotal, UploadTotal: upstream.UploadTotal, Connections: connections,
	})
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
		event, err := json.Marshal(protocol.StreamEvent{Schema: "mihari/v1", Stream: string(kind), Data: message})
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
		Schema: "mihari/v1", Revision: snapshot.Revision, Status: snapshot.Core.Status, Version: snapshot.Core.Version,
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
	case protocol.CodeRevisionConflict, protocol.CodeInvalidState:
		status = http.StatusConflict
	case protocol.CodeDataFailure:
		status = http.StatusUnprocessableEntity
	case protocol.CodeDaemonUnavailable, protocol.CodeUpstreamFailure, protocol.CodeNetworkFailure:
		status = http.StatusBadGateway
	}
	writeJSON(writer, status, protocol.NewError(apiError.Code, apiError.Message, apiError.Details))
}
