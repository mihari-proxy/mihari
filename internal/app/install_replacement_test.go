package app

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/update"
	"strings"
	"testing"
)

func TestInstallReplacement_RejectsBeforeTransaction(t *testing.T) {
	for _, scenario := range []string{"unconfirmed", "stale", "expected-without-yes"} {
		t.Run(scenario, func(t *testing.T) {
			h := newInstallHarness(t, InstallDataRetain)
			before := h.disk.snapshot()
			c := update.ReplacementCandidate{Version: "v1.0.0", SHA256: strings.Repeat("a", 64), Channel: "main"}
			s := update.ReplacementSnapshot{Targets: []update.ReplacementTarget{{Roles: []string{"managed"}, Path: "/test/mihari", Exists: true, Version: "v2.0.0"}}}
			p, err := update.NewReplacementPreview(c, s)
			if err != nil {
				t.Fatal(err)
			}
			consent := update.ReplacementConsent{}
			if scenario == "stale" {
				consent.Yes = true
				s.ServiceDefinitionSHA256 = "changed"
			}
			if scenario == "expected-without-yes" {
				consent.ExpectedPreview = p.ID
			}
			_, err = runInstallReplacement(context.Background(), p, consent, func(ctx context.Context) error { return update.RecheckReplacement(p, c, s) }, func(ctx context.Context) (InstallResult, error) { return h.tx.Apply(ctx, h.req) })
			if err == nil || before != h.disk.snapshot() || h.lease.acquires != 0 {
				t.Fatalf("rejected replacement reached transaction: err=%v acquires=%d", err, h.lease.acquires)
			}
		})
	}
}
