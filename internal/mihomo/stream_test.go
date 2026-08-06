package mihomo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestStreamReadsAllSupportedKindsWithAuthentication(t *testing.T) {
	for _, kind := range []StreamKind{StreamTraffic, StreamMemory, StreamLogs, StreamConnections} {
		t.Run(string(kind), func(t *testing.T) {
			serverErrors := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/"+string(kind) {
					serverErrors <- errors.New("unexpected stream path: " + request.URL.Path)
					return
				}
				if request.Header.Get("Authorization") != "Bearer stream-secret" {
					serverErrors <- errors.New("missing stream authorization")
					return
				}
				connection, err := websocket.Accept(response, request, nil)
				if err != nil {
					serverErrors <- err
					return
				}
				if err := connection.Write(request.Context(), websocket.MessageText, []byte(`{"kind":"`+kind+`"}`)); err != nil {
					serverErrors <- err
					return
				}
				serverErrors <- connection.Close(websocket.StatusNormalClosure, "done")
			}))
			defer server.Close()

			var messages []json.RawMessage
			err := NewClient(server.URL, "stream-secret", server.Client()).Stream(context.Background(), kind, func(message json.RawMessage) error {
				messages = append(messages, append(json.RawMessage(nil), message...))
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := <-serverErrors; err != nil {
				t.Fatal(err)
			}
			if len(messages) != 1 || string(messages[0]) != `{"kind":"`+string(kind)+`"}` {
				t.Fatalf("messages=%q", messages)
			}
		})
	}
}

func TestStreamStopsCleanlyWhenContextIsCancelled(t *testing.T) {
	accepted := make(chan struct{})
	release := make(chan struct{})
	var closeRelease sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		close(accepted)
		<-release
	}))
	defer func() {
		closeRelease.Do(func() { close(release) })
		server.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- NewClient(server.URL, "secret", server.Client()).Stream(ctx, StreamTraffic, func(json.RawMessage) error {
			return nil
		})
	}()
	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("stream was not accepted")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled stream returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled stream did not stop")
	}
	closeRelease.Do(func() { close(release) })
}

func TestStreamRejectsInvalidAndOversizedMessages(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{"invalid JSON", `not-json`},
		{"oversized", `"` + strings.Repeat("x", maxStreamMessageSize) + `"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				connection, err := websocket.Accept(response, request, nil)
				if err != nil {
					return
				}
				defer connection.CloseNow()
				_ = connection.Write(request.Context(), websocket.MessageText, []byte(test.message))
			}))
			defer server.Close()

			err := NewClient(server.URL, "secret", server.Client()).Stream(context.Background(), StreamTraffic, func(json.RawMessage) error {
				return nil
			})
			var apiError protocol.APIError
			if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestStreamRejectsUnknownKindBeforeDial(t *testing.T) {
	client := NewClient("http://127.0.0.1:1", "secret", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("should not dial")
	})})
	err := client.Stream(context.Background(), StreamKind("unknown"), func(json.RawMessage) error { return nil })
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeInvalidArgument {
		t.Fatalf("err=%v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
