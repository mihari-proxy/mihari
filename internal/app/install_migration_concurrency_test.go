package app

import (
	"bytes"
	"os"
	"testing"

	"github.com/mihari-proxy/mihari/internal/preferences"
)

func TestDecodeTUIBytes_LatencyConcurrency(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		valid       bool
	}{
		{"legacy", "", true},
		{"minimum", `,"latency_test_concurrency":1`, true},
		{"default", `,"latency_test_concurrency":5`, true},
		{"custom", `,"latency_test_concurrency":8`, true},
		{"maximum", `,"latency_test_concurrency":50`, true},
		{"zero", `,"latency_test_concurrency":0`, false},
		{"negative", `,"latency_test_concurrency":-1`, false},
		{"over maximum", `,"latency_test_concurrency":51`, false},
		{"unknown", `,"unexpected":true`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"schema":"mihari.tui-preferences/v1","connections_columns":["host"],"proxies":{"extra_latency":false,"auto_latency_test":true` + tc.field + `}}`)
			if err := decodeTUIBytes(raw); (err == nil) != tc.valid {
				t.Fatalf("valid=%t err=%v", tc.valid, err)
			}
		})
	}
}

func TestUnixMigration_PreservesSavedLatencyConcurrency(t *testing.T) {
	fx := newMigrationFixture(t)
	rel := "preferences/tui.json"
	svc, err := preferences.Open(fx.source.osPath(rel))
	if err != nil {
		t.Fatal(err)
	}
	prefs := svc.Snapshot().Proxies
	prefs.LatencyTestConcurrency = 8
	if _, err := svc.Update(t.Context(), preferences.Update{Proxies: &prefs}); err != nil {
		t.Fatal(err)
	}
	before := fx.sourceHashes(t)
	raw, err := os.ReadFile(fx.source.osPath(rel))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareMigration(t.Context(), fx.options())
	if err != nil {
		t.Fatalf("migration rejected daemon-written preferences: %v", err)
	}
	defer prepared.cleanup()
	staged, err := os.ReadFile(fx.staging.osPath(rel))
	if err != nil || !bytes.Equal(raw, staged) || !prepared.hasRel(rel) {
		t.Fatalf("preferences changed or were lost during staging: %v", err)
	}
	if !mapsEqual(before, fx.sourceHashes(t)) {
		t.Fatal("migration changed the source tree")
	}
}
