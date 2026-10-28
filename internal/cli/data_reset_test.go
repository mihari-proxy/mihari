package cli

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"testing"
)

type resetCLIClient struct {
	RuntimeClient
	calls int
}

func (c *resetCLIClient) DataResetPreview(context.Context) (protocol.ResetPreview, error) {
	return protocol.ResetPreview{Digest: "fixture-digest"}, nil
}
func (c *resetCLIClient) ResetBusinessData(context.Context, protocol.DataResetRequest) (protocol.DataResetResult, error) {
	c.calls++
	return protocol.DataResetResult{}, nil
}
func TestDataResetCLI_RequiresDigestAndBothConfirmations(t *testing.T) {
	for _, args := range [][]string{{"data-reset", "run"}, {"data-reset", "run", "--digest", "fixture-digest", "--yes"}, {"data-reset", "run", "--digest", "stale", "--yes", "--confirm"}} {
		c := &resetCLIClient{}
		root := newRoot(Dependencies{RuntimeClient: c}, &runOptions{})
		root.SetArgs(args)
		if err := root.ExecuteContext(t.Context()); err == nil || c.calls != 0 {
			t.Fatal("unreviewed reset reached daemon")
		}
	}
}
