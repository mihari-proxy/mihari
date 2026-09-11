package daemon

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	transporttest "github.com/mihari-proxy/mihari/internal/control/transport/testutil"
	"net"
	"testing"
)

type closeObservedListener struct {
	net.Listener
	closes int
}

func (l *closeObservedListener) Close() error { l.closes++; return l.Listener.Close() }
func TestInstallValidation_ReadyPublicationFailureClosesOwnedListener(t *testing.T) {
	endpoint := transporttest.Endpoint(t)
	l, err := transport.Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	owned := &closeObservedListener{Listener: l}
	failure := errors.New("publish ready failed")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = Run(ctx, Options{ValidationMode: true, Endpoint: endpoint, Listen: func(context.Context) (net.Listener, error) { return owned, nil }, OnReady: func() error { return failure }})
	if !errors.Is(err, failure) || owned.closes != 1 {
		t.Fatalf("ready failure lost listener ownership: err=%v closes=%d", err, owned.closes)
	}
}

func TestInstallValidation_RunRequiresOwnedListenerAndPrivateReady(t *testing.T) {
	called := false
	err := Run(context.Background(), Options{ValidationMode: true, Listen: func(context.Context) (net.Listener, error) { called = true; return nil, errors.New("ordinary listen") }})
	if err == nil || called {
		t.Fatalf("validation without private ready reached listener: err=%v called=%v", err, called)
	}
}
