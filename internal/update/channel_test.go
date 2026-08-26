package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCanonicalTag(t *testing.T) {
	tests := []struct {
		in    string
		ok    bool
		major int
		minor int
		patch int
		dev   int
		isDev bool
	}{
		{in: "v0.8.2", ok: true, major: 0, minor: 8, patch: 2},
		{in: "v0.9.0-dev.3", ok: true, major: 0, minor: 9, patch: 0, dev: 3, isDev: true},
		{in: "v0.9.0-dev.10", ok: true, major: 0, minor: 9, patch: 0, dev: 10, isDev: true},
		{in: "0.8.2", ok: false},
		{in: "v01.0.0", ok: false},
		{in: "v0.9.0-dev", ok: false},
		{in: "v0.9.0-dev.3-rc.1", ok: false},
		{in: "dev", ok: false},
		{in: "v0.9.0-alpha.1", ok: false},
	}
	for _, test := range tests {
		t.Run(test.in, func(t *testing.T) {
			got, ok := parseCanonicalTag(test.in)
			if ok != test.ok {
				t.Fatalf("ok=%v want=%v got=%#v", ok, test.ok, got)
			}
			if !test.ok {
				return
			}
			if got.major != test.major || got.minor != test.minor || got.patch != test.patch || got.dev != test.dev || got.isDev != test.isDev {
				t.Fatalf("got=%#v", got)
			}
		})
	}
}

func TestCompareCanonicalTags(t *testing.T) {
	tests := []struct {
		name  string
		left  string
		right string
		want  int
	}{
		{name: "dev newer series", left: "v0.9.0-dev.3", right: "v0.8.2", want: 1},
		{name: "stable beats same-series dev", left: "v0.9.0", right: "v0.9.0-dev.3", want: 1},
		{name: "dev.10 > dev.9", left: "v0.9.0-dev.10", right: "v0.9.0-dev.9", want: 1},
		{name: "equal stable", left: "v0.8.2", right: "v0.8.2", want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := compareCanonicalTags(test.left, test.right)
			if !ok || got != test.want {
				t.Fatalf("got=%d ok=%v want=%d", got, ok, test.want)
			}
			rev, ok := compareCanonicalTags(test.right, test.left)
			if !ok || rev != -test.want {
				t.Fatalf("reverse got=%d ok=%v", rev, ok)
			}
		})
	}
}

func TestClassifyUpdate(t *testing.T) {
	tests := []struct {
		name      string
		current   string
		latest    string
		available bool
		ahead     bool
	}{
		{name: "same tag strips v", current: "0.8.2", latest: "v0.8.2", available: false, ahead: false},
		{name: "dev to main offers official latest", current: "v0.9.0-dev.8", latest: "v0.8.2", available: true, ahead: false},
		{name: "main to dev offers prerelease latest", current: "v0.8.2", latest: "v0.9.0-dev.8", available: true, ahead: false},
		{name: "unprefixed prerelease current offers official latest", current: "0.9.0-dev.3", latest: "v0.8.2", available: true, ahead: false},
		{name: "newer official ahead of older official", current: "v0.9.0", latest: "v0.8.2", available: false, ahead: true},
		{name: "stable ahead of same-series dev", current: "v0.9.0", latest: "v0.9.0-dev.3", available: false, ahead: true},
		{name: "newer prerelease ahead of older prerelease", current: "v0.9.0-dev.8", latest: "v0.9.0-dev.3", available: false, ahead: true},
		{name: "dirty current is available not ahead", current: "dev", latest: "v0.8.2", available: true, ahead: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			available, ahead := classifyUpdate(test.current, test.latest)
			if available != test.available || ahead != test.ahead {
				t.Fatalf("available=%v ahead=%v", available, ahead)
			}
			if available && ahead {
				t.Fatal("both true")
			}
		})
	}
}

func TestLoadChannelMissingFileDefaultsMain(t *testing.T) {
	got, err := LoadChannel(filepath.Join(t.TempDir(), "mihari-channel"))
	if err != nil || got != ChannelMain {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestLoadChannelRejectsInvalidLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mihari-channel")
	if err := os.WriteFile(path, []byte("stable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadChannel(path); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadChannelIgnoresTrailingLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mihari-channel")
	if err := os.WriteFile(path, []byte("dev\nignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadChannel(path)
	if err != nil || got != ChannelDev {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestSaveChannelRoundTripAndOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "mihari-channel")
	if err := SaveChannel(path, ChannelDev); err != nil {
		t.Fatal(err)
	}
	got, err := LoadChannel(path)
	if err != nil || got != ChannelDev {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if err := SaveChannel(path, ChannelMain); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "main\n" {
		t.Fatalf("raw=%q err=%v", raw, err)
	}
}

func TestSaveChannelRejectsInvalid(t *testing.T) {
	if err := SaveChannel(filepath.Join(t.TempDir(), "mihari-channel"), "stable"); err == nil {
		t.Fatal("expected error")
	}
}
