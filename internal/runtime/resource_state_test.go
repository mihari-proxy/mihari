package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

func TestSubscriptionResourceChange_MismatchedPlanDoesNotRequireRecovery(t *testing.T) {
	for name, change := range map[string]durableResourceStateChange{
		"use":     &subscriptionUseChange{id: "expected-subscription"},
		"refresh": &subscriptionRefreshChange{wantID: "expected-subscription", wantGen: 3},
	} {
		t.Run(name, func(t *testing.T) {
			err := change.PrepareLocked(context.Background(), &subscription.PreparedResources{})
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict || api.Details["degraded"] == true {
				t.Fatalf("unactivated stale plan must remain retryable: %v", err)
			}
		})
	}
}
