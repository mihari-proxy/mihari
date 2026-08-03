package session

import (
	"context"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

type Client interface {
	Status(context.Context) (protocol.Status, error)
	Core(context.Context) (protocol.CoreStatus, error)
	Subscriptions(context.Context) (protocol.SubscriptionList, error)
	ProxyGroups(context.Context) (protocol.ProxyGroups, error)
	TUIPreferences(context.Context) (protocol.TUIPreferences, error)
	Stream(context.Context, string, func(protocol.StreamEvent) error) error
}
