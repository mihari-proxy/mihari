package cli

import (
	"bytes"
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"testing"
)

type authCLIClient struct {
	RuntimeClient
	config protocol.ProxyAuthConfig
	reads  int
}

func (c *authCLIClient) ProxyAuth(context.Context, bool) (protocol.ProxyAuthStatus, error) {
	c.reads++
	return protocol.ProxyAuthStatus{Config: c.config, Revision: 3}, nil
}
func (c *authCLIClient) UpdateProxyAuth(_ context.Context, r protocol.ProxyAuthUpdateRequest) (protocol.ProxyAuthStatus, error) {
	c.config = r.Config
	return protocol.ProxyAuthStatus{Config: c.config}, nil
}
func TestProxyAuthCLI_UserPasswordFromStdin(t *testing.T) {
	c := &authCLIClient{config: protocol.ProxyAuthConfig{Mode: "follow", BypassMode: "follow"}}
	root := newRoot(Dependencies{RuntimeClient: c, NewOperationID: func() string { return "fixture" }}, &runOptions{})
	root.SetArgs([]string{"proxy-auth", "user", "set", "alice", "--password-stdin"})
	root.SetIn(bytes.NewBufferString("fixture-secret\n"))
	var out bytes.Buffer
	root.SetOut(&out)
	if err := root.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(c.config.Users) != 1 || c.config.Users[0].Password != "fixture-secret" || bytes.Contains(out.Bytes(), []byte("fixture-secret")) {
		t.Fatalf("unexpected credential handling: users=%d output=%s", len(c.config.Users), out.String())
	}
}
