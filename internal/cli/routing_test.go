package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"strings"
	"testing"
)

type routingClient struct {
	fakeRuntimeClient
	request protocol.RoutingUpdateRequest
}

func (c *routingClient) Routing(context.Context) (protocol.RoutingStatus, error) {
	return protocol.RoutingStatus{Schema: "mihari/v1", DesiredMode: "global", LiveMode: "global", State: "applied", GlobalSelection: "DIRECT"}, nil
}
func (c *routingClient) UpdateRouting(ctx context.Context, request protocol.RoutingUpdateRequest) (protocol.RoutingStatus, error) {
	c.request = request
	result, _ := c.Routing(ctx)
	result.DesiredMode = request.Mode
	result.LiveMode = request.Mode
	return result, nil
}

func TestProxyMode_CLIQueryAndUpdate(t *testing.T) {
	c := &routingClient{}
	for _, args := range [][]string{{"proxy", "mode"}, {"proxy", "mode", "direct", "--json"}} {
		var out, stderr bytes.Buffer
		exit := Execute(context.Background(), args, &out, &stderr, Dependencies{RuntimeClient: c, NewOperationID: func() string { return "routing-op" }})
		if exit != ExitOK {
			t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
		}
		if len(args) == 2 {
			if !strings.Contains(out.String(), "global") || !strings.Contains(out.String(), "DIRECT") {
				t.Fatalf("output=%s", out.String())
			}
		} else {
			var status protocol.RoutingStatus
			if err := json.Unmarshal(out.Bytes(), &status); err != nil {
				t.Fatal(err)
			}
			if status.DesiredMode != "direct" || c.request.OperationID != "routing-op" {
				t.Fatal("mode request was not forwarded")
			}
		}
	}
	for _, args := range [][]string{{"proxy", "mode", "invalid"}, {"proxy", "mode", "rule", "extra"}} {
		var out, stderr bytes.Buffer
		if exit := Execute(context.Background(), args, &out, &stderr, Dependencies{RuntimeClient: c}); exit != ExitUsage {
			t.Fatalf("invalid arguments exit=%d", exit)
		}
	}
}
