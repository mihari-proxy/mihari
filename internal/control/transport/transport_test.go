package transport

import (
	"context"
	"io"
	"testing"
	"time"

	transporttest "github.com/LeeShunEE/mihari/internal/control/transport/testutil"
)

func TestListenAndDialRoundTrip(t *testing.T) {
	endpoint := transporttest.Endpoint(t)
	listener, err := Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	done := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer connection.Close()
		_, err = connection.Write([]byte("pong"))
		done <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connection, err := DialContext(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(connection)
	connection.Close()
	if err != nil || string(body) != "pong" {
		t.Fatalf("body=%q err=%v", body, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEndpointIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 32 {
		endpoint := transporttest.Endpoint(t)
		if seen[endpoint] {
			t.Fatalf("duplicate endpoint %q", endpoint)
		}
		seen[endpoint] = true
	}
}
