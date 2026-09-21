package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type egressCLIClient struct {
	fakeRuntimeClient
	request protocol.EgressUpdateRequest
	calls   int
	failure error
	status  *protocol.EgressStatus
}

func (c *egressCLIClient) Egress(context.Context) (protocol.EgressStatus, error) {
	if c.status != nil {
		return *c.status, nil
	}
	return protocol.EgressStatus{Schema: "mihari/v1", State: "saved", Selection: protocol.EgressSelection{Mode: "automatic"}, Interfaces: []protocol.EgressInterface{}}, nil
}
func (c *egressCLIClient) UpdateEgress(ctx context.Context, r protocol.EgressUpdateRequest) (protocol.EgressStatus, error) {
	c.request = r
	c.calls++
	if c.failure != nil {
		return protocol.EgressStatus{}, c.failure
	}
	return c.Egress(ctx)
}

func TestEgressCLI_CommandsPreserveNamesAndRevision(t *testing.T) {
	for _, action := range []string{"set", "auto", "list", "status"} {
		t.Run(action, func(t *testing.T) {
			c := &egressCLIClient{}
			out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
			args := []string{"egress", action}
			if action == "set" {
				args = append(args, "VPN 日本")
			}
			if action == "set" || action == "auto" {
				args = append(args, "--if-revision", "9")
			}
			args = append(args, "--json")
			code := Execute(t.Context(), args, out, errOut, Dependencies{RuntimeClient: c, NewOperationID: func() string { return "cli-op" }})
			if code != ExitOK || errOut.Len() != 0 {
				t.Fatalf("code=%d stderr=%s", code, errOut)
			}
			var got protocol.EgressStatus
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if action == "set" || action == "auto" {
				if c.calls != 1 || c.request.IfRevision == nil || *c.request.IfRevision != 9 {
					t.Fatalf("request=%+v", c.request)
				}
			} else if c.calls != 0 {
				t.Fatal("read mutated state")
			}
			if action == "set" && c.request.InterfaceName != "VPN 日本" {
				t.Fatal("name changed")
			}
			if action == "auto" && (c.request.Mode != "automatic" || c.request.InterfaceName != "") {
				t.Fatal("invalid automatic request")
			}
		})
	}
}

func TestEgressCLI_ApplyFailureIsNotSuccess(t *testing.T) {
	c := &egressCLIClient{failure: errors.New("apply failed")}
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	code := Execute(t.Context(), []string{"egress", "set", "Ethernet"}, out, errOut, Dependencies{RuntimeClient: c, NewOperationID: func() string { return "op" }})
	if code == ExitOK || out.Len() != 0 {
		t.Fatalf("code=%d output=%s error=%s", code, out, errOut)
	}
}

func TestEgressCLI_TextShowsUnavailableSavedChoiceAndState(t *testing.T) {
	for _, action := range []string{"list", "status"} {
		c := &egressCLIClient{status: &protocol.EgressStatus{State: "saved", Selection: protocol.EgressSelection{Mode: "manual", InterfaceName: "Missing VPN"}, Interfaces: []protocol.EgressInterface{{Name: "Missing VPN", Availability: "not_found", Selectable: true}}}}
		out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
		code := Execute(t.Context(), []string{"egress", action}, out, errOut, Dependencies{RuntimeClient: c})
		if code != ExitOK || !strings.Contains(out.String(), "Missing VPN") || !strings.Contains(out.String(), "not_found") || strings.Contains(out.String(), "Pending") {
			t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
		}
		if action == "status" && !strings.Contains(out.String(), "State: saved") {
			t.Fatal("saved state missing")
		}
	}
}
