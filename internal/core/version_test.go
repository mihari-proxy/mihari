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

func TestParseAlphaSHA(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"mihomo-linux-amd64-alpha-e183c58.gz", "e183c58"},
		{"mihomo-windows-arm64-alpha-abc1234.zip", "abc1234"},
		{"mihomo-linux-amd64-compatible-v1.19.0.gz", ""},
		{"mihomo-linux-amd64-v3-alpha-e183c58.gz", ""},
		{"mihomo-linux-amd64-alpha-e183c58-go124.gz", ""},
		{"not-an-asset", ""},
	}
	for _, test := range tests {
		if got := ParseAlphaSHA(test.name); got != test.want {
			t.Fatalf("ParseAlphaSHA(%q)=%q want %q", test.name, got, test.want)
		}
	}
}

func TestParseVersionAcceptsAlphaToken(t *testing.T) {
	version, err := ParseVersion("Mihomo Meta alpha-dd7bc4c windows amd64 with go1.26.5")
	if err != nil || version != "alpha-dd7bc4c" {
		t.Fatalf("version=%q err=%v", version, err)
	}
	version, err = ParseVersion("Mihomo Meta v1.19.0 windows amd64 with go1.26.0")
	if err != nil || version != "v1.19.0" {
		t.Fatalf("stable version=%q err=%v", version, err)
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
