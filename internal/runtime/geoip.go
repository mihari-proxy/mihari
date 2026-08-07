package runtime

import (
	"context"
	"errors"
	"net/netip"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/geoip"
	"github.com/mihari-proxy/mihari/internal/state"
)

// GeoIPStatus returns the current redacted database health.
func (m *Manager) GeoIPStatus(context.Context) (geoip.Status, error) {
	if m.geoip == nil {
		return geoip.Status{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "geoip service is unavailable"}
	}
	return m.geoip.Status(), nil
}

// LookupGeoIP resolves a bounded batch from daemon-owned local databases.
func (m *Manager) LookupGeoIP(_ context.Context, addresses []netip.Addr) ([]geoip.Record, error) {
	if m.geoip == nil {
		return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "geoip service is unavailable"}
	}
	if len(addresses) == 0 || len(addresses) > 16 {
		return nil, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "geoip lookup requires 1 to 16 addresses"}
	}
	records := make([]geoip.Record, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		if _, exists := seen[address]; exists {
			continue
		}
		seen[address] = struct{}{}
		record, err := m.geoip.Lookup(address)
		if err != nil {
			switch {
			case errors.Is(err, geoip.ErrInvalidAddress):
				return nil, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "geoip lookup accepts public addresses only"}
			case errors.Is(err, geoip.ErrUnavailable):
				return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "geoip database is unavailable"}
			default:
				return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "geoip lookup failed"}
			}
		}
		records = append(records, record)
	}
	return records, nil
}

// UpdateGeoIP prepares outside the mutation gate and commits under revision control.
func (m *Manager) UpdateGeoIP(ctx context.Context, operation Operation) (geoip.Status, error) {
	result, err := m.doOperation(ctx, "geoip:"+operation.ID, func() (any, error) {
		// setup 预检（design §4.3）：aio 脚本已预置 GeoIP 且本地 MMDB 有效时直接返回，不联网下载。
		if operation.Source == "setup" && m.geoip != nil {
			if status := m.geoip.Status(); status.Country.Available && status.ASN.Available {
				return status, nil
			}
			// 无效 → 落联网下载。
		}
		if m.geoip == nil || m.prepareGeoIP == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "geoip updater is unavailable"}
		}
		candidate, err := m.prepareGeoIP(ctx)
		if err != nil {
			return nil, err
		}
		defer candidate.Cleanup()
		if err := m.lock(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		if err := m.checkOpen(); err != nil {
			return nil, err
		}
		if candidate.Identity() == "" || !candidate.Valid() {
			return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "geoip update candidate changed before commit"}
		}
		_, err = m.coordinator.Do(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			if !candidate.Valid() {
				return snapshot, protocol.APIError{Code: protocol.CodeDataFailure, Message: "geoip update candidate changed before commit"}
			}
			if err := candidate.Commit(); err != nil {
				if errors.Is(err, geoip.ErrStaleCandidate) {
					return snapshot, protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "geoip database pair changed during update"}
				}
				return snapshot, err
			}
			return snapshot, nil
		})
		if err != nil {
			return nil, err
		}
		return m.geoip.Status(), nil
	})
	if recorder, ok := m.geoip.(interface{ RecordUpdateError(error) }); ok {
		var apiError protocol.APIError
		if err == nil {
			recorder.RecordUpdateError(nil)
		} else if !(errors.As(err, &apiError) && apiError.Code == protocol.CodeRevisionConflict) &&
			!errors.Is(err, context.Canceled) && !errors.Is(err, geoip.ErrStaleCandidate) {
			recorder.RecordUpdateError(err)
		}
	}
	if err != nil {
		return geoip.Status{}, err
	}
	return result.(geoip.Status), nil
}
