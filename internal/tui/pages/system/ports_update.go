package system

import (
	"context"
	"errors"
	"fmt"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// applyPortUpdate rebases only unchanged endpoint inputs onto a fresh global
// revision. A revision rejection guarantees that the daemon did not commit;
// transport failures and other errors must never trigger another write.
func applyPortUpdate(ctx context.Context, client Client, baseline protocol.OnboardingStatus, request protocol.OnboardingUpdateRequest) (protocol.OnboardingStatus, error) {
	operationID := request.OperationID
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return protocol.OnboardingStatus{}, err
		}
		current, err := client.Onboarding(ctx)
		if err != nil {
			return protocol.OnboardingStatus{}, err
		}
		mixed, controller, web := current.MixedAddr, current.ControllerAddr, current.WebAddr
		for _, endpoint := range []struct {
			name    string
			before  string
			current *string
			target  *string
		}{
			{"mixed-addr", baseline.MixedAddr, &mixed, request.MixedAddr},
			{"controller-addr", baseline.ControllerAddr, &controller, request.ControllerAddr},
			{"web-addr", baseline.WebAddr, &web, request.WebAddr},
		} {
			if endpoint.target == nil {
				continue
			}
			if *endpoint.current != endpoint.before {
				return current, protocol.APIError{
					Code:    protocol.CodeRevisionConflict,
					Message: "port configuration changed while editing; reopen the port editor and try again",
					Details: map[string]any{"setting": endpoint.name},
				}
			}
			*endpoint.current = *endpoint.target
		}
		if err := ui.ValidateManagedEndpoints(mixed, controller, web); err != nil {
			return current, err
		}
		if err := ctx.Err(); err != nil {
			return current, err
		}
		revision := current.Revision
		request.IfRevision = &revision
		if attempt > 0 {
			// Failed operations are cached by ID; only a confirmed revision
			// rejection may be retried with a distinct execution identity.
			request.OperationID = fmt.Sprintf("%s-retry-%d", operationID, attempt)
		}
		updated, err := client.UpdateOnboarding(ctx, request)
		if err == nil {
			return updated, nil
		}
		var api protocol.APIError
		if attempt >= 2 || !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict {
			latest, refreshErr := client.Onboarding(ctx)
			if refreshErr != nil {
				return protocol.OnboardingStatus{}, errors.Join(err, fmt.Errorf("refresh ports after failed update: %w", refreshErr))
			}
			return latest, err
		}
	}
}
