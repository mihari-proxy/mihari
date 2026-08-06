package server

import (
	"net/http"
	"net/netip"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/geoip"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
)

func (s *Server) geoIPRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/geoip/status", s.geoIPStatus)
	mux.HandleFunc("POST /v1/geoip/lookup", s.geoIPLookup)
	mux.HandleFunc("POST /v1/geoip/update", s.geoIPUpdate)
}

func (s *Server) geoIPStatus(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	status, err := s.runtime.GeoIPStatus(request.Context())
	if err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, geoIPStatusDTO(status, s.runtime.Snapshot().Revision))
}

func (s *Server) geoIPLookup(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	var body protocol.GeoIPLookupRequest
	if !decodeControlJSON(writer, request, &body) {
		return
	}
	if len(body.Addresses) == 0 || len(body.Addresses) > 16 {
		writeInvalidArgument(writer, "geoip lookup requires 1 to 16 addresses")
		return
	}
	addresses := make([]netip.Addr, 0, len(body.Addresses))
	seen := make(map[netip.Addr]struct{}, len(body.Addresses))
	for _, raw := range body.Addresses {
		address, err := netip.ParseAddr(raw)
		if err != nil {
			writeInvalidArgument(writer, "invalid geoip address")
			return
		}
		address = address.Unmap()
		if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsMulticast() || address.IsUnspecified() {
			writeInvalidArgument(writer, "geoip lookup accepts public addresses only")
			return
		}
		if _, exists := seen[address]; exists {
			writeInvalidArgument(writer, "duplicate geoip address")
			return
		}
		seen[address] = struct{}{}
		addresses = append(addresses, address)
	}
	records, err := s.runtime.LookupGeoIP(request.Context(), addresses)
	if err != nil {
		writeControlError(writer, err)
		return
	}
	if len(records) != len(addresses) {
		writeControlError(writer, protocol.APIError{Code: protocol.CodeDataFailure, Message: "geoip lookup returned an invalid record count"})
		return
	}
	result := make([]protocol.GeoIPRecord, len(records))
	for index, record := range records {
		result[index] = protocol.GeoIPRecord{
			Address: addresses[index].String(), CountryCode: record.CountryCode, ASN: record.ASN, Organization: record.Organization,
		}
	}
	writeJSON(writer, http.StatusOK, protocol.GeoIPLookupResult{Schema: "mihari/v1", Records: result})
}

func (s *Server) geoIPUpdate(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	var body protocol.MutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	status, err := s.runtime.UpdateGeoIP(request.Context(), runtimeapi.Operation{ID: body.OperationID, Source: "control", IfRevision: body.IfRevision})
	if err != nil {
		writeControlError(writer, err)
		return
	}
	revision := s.runtime.Snapshot().Revision
	writeJSON(writer, http.StatusOK, protocol.GeoIPUpdateResult{
		Schema: "mihari/v1", OperationID: body.OperationID, Revision: revision, Status: geoIPStatusDTO(status, revision),
	})
}

func geoIPStatusDTO(status geoip.Status, revision uint64) protocol.GeoIPStatus {
	return protocol.GeoIPStatus{
		Schema: "mihari/v1", Revision: revision,
		Country: geoIPDatabaseStatusDTO(status.Country), ASN: geoIPDatabaseStatusDTO(status.ASN),
	}
}

func geoIPDatabaseStatusDTO(status geoip.DatabaseStatus) protocol.GeoIPDatabaseStatus {
	errorText := ""
	switch status.LastError {
	case "database unavailable":
		errorText = "Unavailable"
	case "update failed":
		errorText = "Update failed"
	}
	return protocol.GeoIPDatabaseStatus{Available: status.Available, UpdatedAt: status.UpdatedAt, Error: errorText}
}
