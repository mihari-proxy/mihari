package update

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSelfPrepare_VerifiedCandidateLeavesRunningBinary(t *testing.T) {
	payload := []byte("prepared release")
	env := startSelfUpdateEnv(t, selfUpdateServerConfig{checksumBody: fixtureSHA256Hex(payload) + "  mihari-linux-amd64\n", binaryBody: payload})
	before, err := os.ReadFile(env.binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	env.updater.AfterReplace = func(context.Context, string) error { calls++; return nil }
	prepared, err := env.updater.Prepare(context.Background(), env.binaryPath, "v1.0.0", ChannelMain)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := prepared.Close(); err != nil {
			t.Error(err)
		}
	}()
	if !prepared.Available || prepared.Version != "v9.9.9" || prepared.SHA256 != fixtureSHA256Hex(payload) {
		t.Fatalf("missing verified prepared release: %+v", prepared)
	}
	got, err := os.ReadFile(prepared.CandidatePath)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("candidate=%q err=%v", got, err)
	}
	after, err := os.ReadFile(env.binaryPath)
	if err != nil || string(after) != string(before) || calls != 0 {
		t.Fatalf("Prepare mutated running installation: calls=%d err=%v", calls, err)
	}
	if filepath.Dir(prepared.CandidatePath) == filepath.Dir(env.binaryPath) {
		t.Fatal("candidate lacks private workspace")
	}
	candidate := prepared.CandidatePath
	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(candidate); !os.IsNotExist(err) {
		t.Fatalf("candidate leaked: %v", err)
	}
}
