package server

import (
	"net/http"
	"strconv"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// diagnosticList never reports its own query failures back into the queried history.
func (s *Server) diagnosticList(writer http.ResponseWriter, request *http.Request) {
	if s.diagnosticHistory == nil {
		writeJSON(writer, http.StatusOK, protocol.DiagnosticList{Schema: "mihari.diagnostics/v1", State: protocol.DiagnosticUnsupported, Records: []protocol.Diagnostic{}})
		return
	}
	query := request.URL.Query()
	var after uint64
	var err error
	if value := query.Get("after"); value != "" {
		after, err = strconv.ParseUint(value, 10, 64)
		if err != nil {
			writeInvalidArgument(writer, "invalid diagnostic sequence")
			return
		}
	}
	limit := 50
	if value := query.Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			writeInvalidArgument(writer, "diagnostic limit must be between 1 and 100")
			return
		}
	}
	instance := query.Get("instance_id")
	if len(instance) > 128 {
		writeInvalidArgument(writer, "invalid diagnostic instance")
		return
	}
	writeJSON(writer, http.StatusOK, s.diagnosticHistory.List(instance, after, limit))
}

func (s *Server) diagnosticDetail(writer http.ResponseWriter, request *http.Request) {
	if s.diagnosticHistory == nil {
		writeJSON(writer, http.StatusOK, protocol.DiagnosticResult{Schema: "mihari.diagnostic/v1", State: protocol.DiagnosticUnsupported})
		return
	}
	id := request.PathValue("id")
	if len(id) > 256 {
		writeInvalidArgument(writer, "invalid diagnostic ID")
		return
	}
	writeJSON(writer, http.StatusOK, s.diagnosticHistory.Get(id))
}
