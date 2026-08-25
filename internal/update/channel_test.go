package update

import "testing"

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
		{name: "newer latest", current: "v0.8.2", latest: "v0.9.0-dev.3", available: true, ahead: false},
		{name: "dev ahead of main", current: "v0.9.0-dev.3", latest: "v0.8.2", available: false, ahead: true},
		{name: "stable ahead of same-series dev", current: "v0.9.0", latest: "v0.9.0-dev.3", available: false, ahead: true},
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
