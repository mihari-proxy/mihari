package cli

import (
	"bytes"
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"testing"
)

type authCLIClient struct {
	RuntimeClient
	config     protocol.ProxyAuthConfig
	reads      int
	lastReveal bool
	last       protocol.ProxyAuthUpdateRequest
}

func (c *authCLIClient) ProxyAuth(_ context.Context, reveal bool) (protocol.ProxyAuthStatus, error) {
	c.reads++
	c.lastReveal = reveal
	return protocol.ProxyAuthStatus{Config: c.config, Revision: 3}, nil
}
func (c *authCLIClient) UpdateProxyAuth(_ context.Context, r protocol.ProxyAuthUpdateRequest) (protocol.ProxyAuthStatus, error) {
	c.last = r
	if r.IfRevision != nil {
		revision := *r.IfRevision
		c.last.IfRevision = &revision
	}
	c.config = r.Config
	return protocol.ProxyAuthStatus{Config: c.config}, nil
}
func TestProxyAuthCLI_UserPasswordFromStdin(t *testing.T) {
	c := &authCLIClient{config: protocol.ProxyAuthConfig{Mode: "follow", BypassMode: "follow"}}
	root := newRoot(Dependencies{RuntimeClient: c, NewOperationID: func() string { return "fixture" }}, &runOptions{})
	root.SetArgs([]string{"proxy-auth", "user", "set", "alice", "--password-stdin"})
	root.SetIn(bytes.NewBufferString("fixture-secret\n"))
	var out, errOut bytes.Buffer
	root.SetErr(&errOut)
	root.SetOut(&out)
	if err := root.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if c.reads != 1 || c.last.IfRevision == nil || *c.last.IfRevision != 3 {
		t.Fatal("update did not bind the revision read from the daemon")
	}
	if bytes.Contains(errOut.Bytes(), []byte("fixture-secret")) {
		t.Fatal("password leaked to stderr")
	}
	if len(c.config.Users) != 1 || c.config.Users[0].Password != "fixture-secret" || bytes.Contains(out.Bytes(), []byte("fixture-secret")) {
		t.Fatalf("unexpected credential handling: users=%d output=%s", len(c.config.Users), out.String())
	}
}

func TestProxyAuthCLI_RevealsInactiveSavedCredential(t *testing.T) {
	c := &authCLIClient{config: protocol.ProxyAuthConfig{Mode: "disabled", BypassMode: "follow", Users: []protocol.ProxyAuthUser{{Username: "alice", Password: "fixture-secret"}}}}
	root := newRoot(Dependencies{RuntimeClient: c}, &runOptions{})
	root.SetArgs([]string{"proxy-auth", "user", "reveal", "alice"})
	var out bytes.Buffer
	root.SetOut(&out)
	if err := root.ExecuteContext(t.Context()); err != nil || !c.lastReveal || !bytes.Contains(out.Bytes(), []byte("fixture-secret")) {
		t.Fatalf("inactive saved credential cannot be revealed: %v", err)
	}
}
