package cli

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"io"
	"testing"
)

type resetCLIClient struct {
	RuntimeClient
	calls int
	last  protocol.DataResetRequest
}

func (c *resetCLIClient) DataResetPreview(context.Context) (protocol.ResetPreview, error) {
	return protocol.ResetPreview{Digest: "fixture-digest"}, nil
}
func (c *resetCLIClient) ResetBusinessData(_ context.Context, request protocol.DataResetRequest) (protocol.DataResetResult, error) {
	c.calls++
	c.last = request
	return protocol.DataResetResult{}, nil
}
func TestDataResetCLI_RequiresDigestAndBothConfirmations(t *testing.T) {
	for _, args := range [][]string{{"data-reset", "run"}, {"data-reset", "run", "--digest", "fixture-digest", "--yes"}, {"data-reset", "run", "--digest", "stale", "--yes", "--confirm"}} {
		c := &resetCLIClient{}
		root := newRoot(Dependencies{RuntimeClient: c}, &runOptions{})
		root.SetArgs(args)
		var api protocol.APIError
		if err := root.ExecuteContext(t.Context()); !errors.As(err, &api) || api.Code != protocol.CodeInvalidArgument || c.calls != 0 {
			t.Fatal("unreviewed reset reached daemon")
		}
	}
}

func TestDataResetCLI_ConfirmedReviewedPlanReachesDaemonOnce(t *testing.T) {
	c := &resetCLIClient{}
	root := newRoot(Dependencies{RuntimeClient: c, NewOperationID: func() string { return "fixture" }}, &runOptions{})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"data-reset", "run", "--digest", "fixture-digest", "--yes", "--confirm"})
	if err := root.ExecuteContext(t.Context()); err != nil || c.calls != 1 || c.last.Digest != "fixture-digest" || c.last.OperationID != "fixture" || !c.last.Confirm || !c.last.ConfirmAgain {
		t.Fatalf("confirmed reset not sent exactly once: calls=%d err=%v", c.calls, err)
	}
}
