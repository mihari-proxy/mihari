package cli

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestSubscriptionAdd_LocalFile(t *testing.T) {
	client := &fakeSubscriptionClient{}
	path := filepath.Join(t.TempDir(), "local space.yaml")
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"sub", "add", "local", "--file", path, "--allow-file-references", "--json"}, out, stderr, Dependencies{SubscriptionClient: client, NewOperationID: func() string { return "local" }})
	want, _ := platform.FileURI(path)
	if exit != ExitOK || client.lastAdd.URL != want || !client.lastAdd.AllowFileReferences {
		t.Fatalf("exit=%d request=%+v stderr=%s", exit, client.lastAdd, stderr)
	}
}

type referenceCLIClient struct {
	fakeSubscriptionClient
	requests []protocol.SubscriptionAddRequest
}

func (c *referenceCLIClient) AddSubscription(_ context.Context, r protocol.SubscriptionAddRequest) (protocol.SubscriptionResult, error) {
	c.requests = append(c.requests, r)
	if !r.AllowFileReferences {
		return protocol.SubscriptionResult{}, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "nodes.path: nodes.yaml; provide --allow-file-references", Details: map[string]any{"confirmation_required": "file_references"}}
	}
	return protocol.SubscriptionResult{Subscription: protocol.Subscription{ID: "local", Name: r.Name, Cached: true}}, nil
}

func TestSubscriptionAdd_InteractiveAcknowledgement(t *testing.T) {
	for _, answer := range []string{"\n", "yes\n"} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			c := &referenceCLIClient{}
			n := 0
			cmd := newRoot(Dependencies{SubscriptionClient: c, Interactive: true, NewOperationID: func() string { n++; return fmt.Sprintf("op-%d", n) }}, &runOptions{})
			var out, stderr bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.SetIn(strings.NewReader(answer))
			cmd.SetArgs([]string{"sub", "add", "fixture", "https://fixture.test/sub"})
			err := cmd.ExecuteContext(context.Background())
			if answer == "\n" {
				if err == nil || len(c.requests) != 1 {
					t.Fatal("empty input did not cancel")
				}
			} else {
				if err != nil || len(c.requests) != 2 || c.requests[0].OperationID == c.requests[1].OperationID || !c.requests[1].AllowFileReferences {
					t.Fatalf("err=%v requests=%+v", err, c.requests)
				}
			}
			if !strings.Contains(stderr.String(), "nodes.yaml") {
				t.Fatal("confirmation omitted referenced path")
			}
		})
	}
}

func TestSubscriptionAdd_RejectsAmbiguousSource(t *testing.T) {
	client := &fakeSubscriptionClient{}
	exit := Execute(context.Background(), []string{"sub", "add", "local", "https://example.test", "--file", filepath.Join(t.TempDir(), "local.yaml")}, &bytes.Buffer{}, &bytes.Buffer{}, Dependencies{SubscriptionClient: client})
	if exit != ExitUsage || client.lastAdd.URL != "" {
		t.Fatalf("exit=%d request=%+v", exit, client.lastAdd)
	}
}
