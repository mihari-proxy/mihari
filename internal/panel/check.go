package panel

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"time"
)

// LatestBuild resolves upstream metadata without modifying panel install state.
func (s *Service) LatestBuild(ctx context.Context, id string) (string, error) {
	if _, ok := Lookup(id); !ok {
		return "", protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "unknown panel"}
	}
	s.mu.Lock()
	adapter := s.adapters[id]
	s.mu.Unlock()
	if adapter == nil {
		return "", protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel version check is unavailable"}
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	build, _, err := adapter.ResolveLatest(ctx)
	if err != nil {
		return "", err
	}
	if build == "" {
		return "", protocol.APIError{Code: protocol.CodeDataFailure, Message: "panel release is missing a build identity"}
	}
	return build, nil
}
