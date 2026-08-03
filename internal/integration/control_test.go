package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LeeShunEE/mihari/internal/cli"
	controlclient "github.com/LeeShunEE/mihari/internal/control/client"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	transporttest "github.com/LeeShunEE/mihari/internal/control/transport/testutil"
	"github.com/LeeShunEE/mihari/internal/daemon"
)

func TestControlPlaneLifecycleAndConcurrentStatus(t *testing.T) {
	endpoint := transporttest.Endpoint(t)
	const token = "integration-token"
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, daemon.Options{
			Endpoint: endpoint,
			Token:    token,
			Version:  "integration",
			Ready:    ready,
		})
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("daemon did not become ready")
	}

	client := controlclient.New(endpoint, token)
	errors := make(chan error, 64)
	var wait sync.WaitGroup
	for range 64 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			requestCtx, requestCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer requestCancel()
			status, err := client.Status(requestCtx)
			if err != nil {
				errors <- err
				return
			}
			if status.Schema != "mihari/v1" || status.ProtocolVersion != "v1" || status.Health != "ok" || status.Revision != 0 {
				errors <- fmt.Errorf("unexpected status: %#v", status)
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not stop")
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exit := cli.Execute(context.Background(), []string{"status", "--json"}, stdout, stderr, cli.Dependencies{StatusClient: client})
	if exit != cli.ExitDaemonUnavailable {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
		t.Fatalf("decode post-shutdown error: %v: %q", err, stderr.String())
	}
	if envelope.Error.Code != protocol.CodeDaemonUnavailable {
		t.Fatalf("post-shutdown error=%#v", envelope.Error)
	}
}
