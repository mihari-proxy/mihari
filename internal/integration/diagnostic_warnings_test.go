package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/mihari-proxy/mihari/internal/cli"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestDiagnosticWarnings_CommittedResultAndReplayContainOriginalCause(t *testing.T) {
	fixture := newOperationDiagnosticsIPCFixture(t, config.CommitResult{Committed: true, Warning: errors.New("sync /private/settings.yaml password=fixture-original")}, nil)
	for attempt := 0; attempt < 2; attempt++ {
		status, err := fixture.client.UpdateLogging(context.Background(), protocol.LoggingUpdateRequest{OperationID: "full-warning", Level: stringPointer("debug")})
		if err != nil || status.Revision != 1 || status.Level != "debug" {
			t.Fatalf("warning changed result: %+v %v", status, err)
		}
		if len(status.Warnings) != 1 || status.Warnings[0].Diagnostic == nil || !strings.Contains(status.Warnings[0].Diagnostic.Detail, "password=fixture-original") {
			t.Fatalf("committed warning missing original content: %+v", status.Warnings)
		}
	}
	if fixture.saver.CallCount() != 1 {
		t.Fatal("warning replay repeated the mutation")
	}
}

type failedDiagnosticSubscriptionFetch struct{ calls atomic.Int32 }

func (f *failedDiagnosticSubscriptionFetch) Fetch(context.Context, subscription.FetchRequest) (subscription.FetchResult, error) {
	f.calls.Add(1)
	return subscription.FetchResult{}, errors.New("first fetch token=fixture-cli-original")
}

func TestDiagnosticWarnings_SubscriptionFirstFetchReachesCLIThroughIPC(t *testing.T) {
	fetcher := &failedDiagnosticSubscriptionFetch{}
	fixture := newSubscriptionControlFixtureWithDownloader(t, fetcher, &editController{})
	for _, asJSON := range []bool{false, true} {
		args := []string{"sub", "add", "Fixture", "https://fixture.invalid/sub"}
		if asJSON {
			args = append(args, "--json")
		}
		var stdout, stderr bytes.Buffer
		code := cli.Execute(context.Background(), args, &stdout, &stderr, cli.Dependencies{SubscriptionClient: fixture.client, NewOperationID: func() string { return "first-fetch-cli" }})
		if code != cli.ExitOK {
			t.Fatalf("registration became failure: %d %s", code, stderr.String())
		}
		if asJSON {
			var result protocol.SubscriptionResult
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if stderr.Len() != 0 || result.Subscription.ID == "" || len(result.Warnings) != 1 || result.Warnings[0].Diagnostic == nil || !strings.Contains(result.Warnings[0].Diagnostic.Detail, "token=fixture-cli-original") {
				t.Fatal("JSON lost committed result or original fetch cause")
			}
		} else if !strings.Contains(stdout.String(), "Fixture") || !strings.Contains(stderr.String(), "token=fixture-cli-original") {
			t.Fatal("text omitted first-fetch warning")
		}
	}
	if fetcher.calls.Load() != 1 {
		t.Fatal("same operation was replayed during diagnostic delivery")
	}
}
