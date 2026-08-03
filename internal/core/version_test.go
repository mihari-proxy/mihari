package core

import (
	"context"
	"testing"
)

func TestParseVersionFromMihomoOutput(t *testing.T) {
	for _, output := range []string{
		"Mihomo Meta v1.19.0 windows amd64 with go1.26.0",
		"mihomo v1.20.1\n",
	} {
		version, err := ParseVersion(output)
		if err != nil {
			t.Fatal(err)
		}
		if version != "v1.19.0" && version != "v1.20.1" {
			t.Fatalf("version=%q", version)
		}
	}
	if _, err := ParseVersion("not a version"); err == nil {
		t.Fatal("expected invalid version output to fail")
	}
}

func TestDetectVersionRunsVersionFlag(t *testing.T) {
	runner := &recordingRunner{output: []byte("Mihomo Meta v1.19.0")}
	version, err := DetectVersion(context.Background(), runner, "mihomo")
	if err != nil {
		t.Fatal(err)
	}
	if version != "v1.19.0" || len(runner.args) != 1 || runner.args[0] != "-v" {
		t.Fatalf("version=%q args=%q", version, runner.args)
	}
}
