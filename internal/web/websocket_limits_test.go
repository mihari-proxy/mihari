package web

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestGatewayWebSocketLargeUpstreamMessages(t *testing.T) {
	for _, kind := range []websocket.MessageType{websocket.MessageText, websocket.MessageBinary} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			payloads := [][]byte{bytes.Repeat([]byte("x"), (32<<10)+1), bytes.Repeat([]byte("y"), 1<<20)}
			controller, state := newTask5WebSocketController(t, func(ctx context.Context, conn *websocket.Conn) error {
				for _, payload := range payloads {
					if err := conn.Write(ctx, kind, payload); err != nil {
						return err
					}
				}
				_, _, err := conn.Read(ctx)
				return err
			})
			gateway := newTask5Gateway(t, controller.URL, nil)
			reporter, out := newWebDiagnostics()
			gateway.Reporter = reporter
			observer := newWebSocketRelayJoinObserver()
			gateway.wsObserver = observer
			stream := dialTask5GatewayStream(t, serveWebSocketGateway(t, gateway))
			stream.SetReadLimit(2 << 20)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			for _, want := range payloads {
				gotKind, got, err := stream.Read(ctx)
				if err != nil {
					t.Fatalf("read %d-byte upstream message: %v", len(want), err)
				}
				if gotKind != kind || !bytes.Equal(got, want) {
					t.Fatal("gateway changed message type or payload")
				}
			}
			if err := stream.Close(websocket.StatusNormalClosure, ""); err != nil {
				t.Fatal(err)
			}
			assertLimitRelayJoined(t, ctx, observer)
			waitDone(t, state.done, "large-message upstream cleanup")
			assertWebDiagnostics(t, out, "", "", 0)
		})
	}
}

func TestGatewayWebSocketBrowserReadLimit(t *testing.T) {
	controller, state := newTask5WebSocketController(t, func(ctx context.Context, conn *websocket.Conn) error {
		conn.SetReadLimit(1 << 20)
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		if len(data) != 32<<10 {
			t.Errorf("browser boundary message has %d bytes", len(data))
		}
		if err := conn.Write(ctx, websocket.MessageText, []byte("accepted")); err != nil {
			return err
		}
		_, _, err = conn.Read(ctx)
		if err == nil {
			t.Error("oversized browser message reached controller")
		}
		return err
	})
	gateway := newTask5Gateway(t, controller.URL, nil)
	reporter, out := newWebDiagnostics()
	gateway.Reporter = reporter
	observer := newWebSocketRelayJoinObserver()
	gateway.wsObserver = observer
	stream := dialTask5GatewayStream(t, serveWebSocketGateway(t, gateway))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := stream.Write(ctx, websocket.MessageText, bytes.Repeat([]byte("x"), 32<<10)); err != nil {
		t.Fatal(err)
	}
	if _, data, err := stream.Read(ctx); err != nil || string(data) != "accepted" {
		t.Fatalf("browser boundary message rejected: %v", err)
	}
	// The peer may close before Write returns; Read must observe that closure.
	_ = stream.Write(ctx, websocket.MessageText, bytes.Repeat([]byte("x"), (32<<10)+1))
	if _, _, err := stream.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("oversized browser message did not close promptly: %v", err)
	}
	assertLimitRelayJoined(t, ctx, observer)
	waitDone(t, state.done, "browser-limit upstream cleanup")
	assertWebDiagnostics(t, out, "websocket.relay.failed", "ERROR", 1)
}

func assertLimitRelayJoined(t *testing.T, ctx context.Context, observer *webSocketRelayJoinObserver) {
	t.Helper()
	select {
	case active := <-observer.handlerResult:
		if active != 0 {
			t.Fatal("handler left relay active")
		}
	case <-ctx.Done():
		t.Fatal("handler did not join relays")
	}
}
