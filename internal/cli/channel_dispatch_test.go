package cli

import (
	"bytes"
	"context"
	"testing"
)

func TestSelfChannel_UsesReadOnlyAppQuery(t *testing.T) {
	t.Setenv("MIHARI_DATA", t.TempDir())
	var out bytes.Buffer
	calls := 0
	code := Execute(context.Background(), []string{"self", "channel"}, &out, &out, Dependencies{PrepareLocalRoot: func() error { t.Fatal("channel prepared business root"); return nil }, ChannelQuery: func(context.Context) (string, error) { calls++; return "dev", nil }})
	if code != 0 || calls != 1 || out.String() != "dev\n" {
		t.Fatalf("channel app query bypassed: calls=%d code=%d out=%s", calls, code, out.String())
	}
}
