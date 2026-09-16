package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestSubscriptionTimeout_CanceledBatchConfirmationReleasesScope(t *testing.T) {
	m := New(timeoutPageClient{call: func(context.Context) error { t.Error("canceled confirmation executed"); return nil }}, nil, nil)
	m.subscriptions = []protocol.Subscription{{ID: "a"}}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if len(m.requestCancels) != 1 {
		t.Fatal("batch was not registered before command creation")
	}
	intent := cmd().(ui.ActionIntentMsg)
	message := intent.Cancel().(ui.PageResultMsg)
	m.Update(message.Result)
	if len(m.requestCancels) != 0 || m.lastError != "" {
		t.Fatal("canceled confirmation leaked request or showed failure")
	}
	if result := intent.Execute().(refreshAllResultMsg); !errors.Is(result.err, context.Canceled) {
		t.Fatal("queued canceled batch escaped cancellation")
	}
}

func TestSubscriptionTimeout_StopCancelsRunningRows(t *testing.T) {
	entered := make(chan context.Context, 2)
	nextID := 0
	m := New(timeoutPageClient{call: func(ctx context.Context) error { entered <- ctx; <-ctx.Done(); return ctx.Err() }}, func() string { nextID++; return fmt.Sprint(nextID) }, nil)
	first, second := m.refresh("a"), m.refresh("b")
	done := make(chan mutationResultMsg, 2)
	go func() { done <- mutationResultFromCmd(t, first) }()
	go func() { done <- mutationResultFromCmd(t, second) }()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			m.Stop()
			t.Fatal("row did not start")
		}
	}
	m.Stop()
	for range 2 {
		result := <-done
		if !errors.Is(result.err, context.Canceled) {
			t.Error("running request not canceled")
		}
		m.Update(result)
	}
	if len(m.requestCancels) != 0 {
		t.Fatal("cancel registrations leaked")
	}
}

func TestSubscriptionTimeout_BatchUsesFreshItemBudget(t *testing.T) {
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	var previous context.Context
	calls := 0
	m := New(timeoutPageClient{call: func(ctx context.Context) error {
		calls++
		if previous != nil {
			if previous.Err() != context.Canceled {
				t.Error("previous item scope not released")
			}
			old, _ := previous.Deadline()
			next, _ := ctx.Deadline()
			if next.Before(old) || ctx.Err() != nil || time.Until(next) < 179*time.Second {
				t.Error("next item reused batch deadline")
			}
		}
		previous = ctx
		return nil
	}}, nil, nil)
	m.SetContextFactory(func() (context.Context, context.CancelFunc) {
		if _, ok := owner.Deadline(); ok {
			t.Error("owner is time limited")
		}
		return context.WithCancel(owner)
	})
	m.subscriptions = []protocol.Subscription{{ID: "a"}, {ID: "b"}}
	result := m.refreshAll()().(refreshAllResultMsg)
	m.Update(result)
	if calls != 2 || result.err != nil || len(m.requestCancels) != 0 {
		t.Fatalf("batch did not release scopes: %v", result.err)
	}
	if previous.Err() != context.Canceled {
		t.Error("last item not released")
	}
}

func TestSubscriptionTimeout_RunOwnerCancellationStopsBatch(t *testing.T) {
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	m := New(timeoutPageClient{call: func(context.Context) error { calls++; cancel(); return nil }}, nil, nil)
	m.SetContextFactory(func() (context.Context, context.CancelFunc) { return context.WithCancel(owner) })
	m.subscriptions = []protocol.Subscription{{ID: "a"}, {ID: "b"}}
	result := m.refreshAll()().(refreshAllResultMsg)
	if calls != 1 || !errors.Is(result.err, context.Canceled) {
		t.Fatalf("remaining item started: calls=%d err=%v", calls, result.err)
	}
}

type timeoutPageClient struct {
	Client
	call func(context.Context) error
}

func (c timeoutPageClient) AddSubscription(ctx context.Context, _ protocol.SubscriptionAddRequest) (protocol.SubscriptionResult, error) {
	return protocol.SubscriptionResult{}, c.call(ctx)
}
func (c timeoutPageClient) RefreshSubscription(ctx context.Context, _ string, _ protocol.MutationRequest) (protocol.SubscriptionResult, error) {
	return protocol.SubscriptionResult{}, c.call(ctx)
}

func TestSubscriptionTimeout_SubscriptionsRequestDeadlines(t *testing.T) {
	for _, kind := range []string{"add", "row", "batch"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			m := New(timeoutPageClient{call: func(ctx context.Context) error {
				calls++
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) < 179*time.Second || time.Until(deadline) > 180*time.Second {
					t.Error("request does not have full subscription budget")
				}
				return nil
			}}, nil, nil)
			if kind == "batch" {
				m.subscriptions = []protocol.Subscription{{ID: "a"}, {ID: "b"}}
				m.refreshAll()()
				if calls != 2 {
					t.Fatalf("batch calls=%d", calls)
				}
			} else {
				cmd := m.refresh("a")
				if kind == "add" {
					m.form = newAddForm()
					cmd = m.submitForm(m.form, "", 0)
				}
				mutationResultFromCmd(t, cmd)
			}
		})
	}
}

func TestSubscriptionTimeout_StopCancelsQueuedCommands(t *testing.T) {
	for _, kind := range []string{"add", "row", "batch"} {
		t.Run(kind, func(t *testing.T) {
			m := New(timeoutPageClient{call: func(ctx context.Context) error {
				if ctx.Err() == nil {
					t.Error("queued request escaped Stop")
				}
				return ctx.Err()
			}}, nil, nil)
			m.subscriptions = []protocol.Subscription{{ID: "a"}, {ID: "b"}}
			if kind == "batch" {
				cmd := m.refreshAll()
				m.Stop()
				m.Stop()
				if msg := cmd().(refreshAllResultMsg); !errors.Is(msg.err, context.Canceled) {
					t.Errorf("batch cancellation lost: %v", msg.err)
				}
			} else {
				cmd := m.refresh("a")
				if kind == "add" {
					m.form = newAddForm()
					cmd = m.submitForm(m.form, "", 0)
				}
				m.Stop()
				m.Stop()
				if msg := mutationResultFromCmd(t, cmd); !errors.Is(msg.err, context.Canceled) {
					t.Errorf("cancellation lost: %v", msg.err)
				}
			}
		})
	}
}
