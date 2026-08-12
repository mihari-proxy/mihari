package main

import (
	"strings"
	"testing"
)

func TestParseProfileAggregatesTotalAndCriticalPackages(t *testing.T) {
	input := `mode: atomic
github.com/mihari-proxy/mihari/internal/runtime/a.go:1.1,2.1 4 1
github.com/mihari-proxy/mihari/internal/runtime/b.go:3.1,4.1 6 0
github.com/mihari-proxy/mihari/internal/web/c.go:1.1,2.1 10 1
`
	got, err := parseProfile(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if got.Total.Statements != 20 || got.Total.Covered != 14 {
		t.Fatalf("total=%+v", got.Total)
	}
	rt := got.Packages["github.com/mihari-proxy/mihari/internal/runtime"]
	if rt.Statements != 10 || rt.Covered != 4 {
		t.Fatalf("runtime=%+v", rt)
	}
	web := got.Packages["github.com/mihari-proxy/mihari/internal/web"]
	if web.Statements != 10 || web.Covered != 10 {
		t.Fatalf("web=%+v", web)
	}
}

func TestParseProfileAcceptsWindowsPaths(t *testing.T) {
	input := "mode: set\n" +
		`github.com\mihari-proxy\mihari\internal\state\store.go:1.1,2.1 3 2` + "\n"
	got, err := parseProfile(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	pkg := "github.com/mihari-proxy/mihari/internal/state"
	if _, ok := got.Packages[pkg]; !ok {
		t.Fatalf("packages=%v", got.Packages)
	}
}

func TestParseProfileRejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"mode:\n",
		"github.com/mihari-proxy/mihari/internal/runtime/a.go:1.1,2.1 1 1\n",
		"mode: atomic\nmode: set\n",
		"mode: atomic\nbadline\n",
		"mode: atomic\ngithub.com/mihari-proxy/mihari/internal/runtime/a.go:1.1,2.1 -1 1\n",
		"mode: atomic\ngithub.com/mihari-proxy/mihari/internal/runtime/a.go:1.1,2.1 1 x\n",
	}
	for _, input := range cases {
		if _, err := parseProfile(strings.NewReader(input)); err == nil {
			t.Fatalf("expected error for %q", input)
		}
	}
}

func TestParseProfileZeroStatements(t *testing.T) {
	input := `mode: atomic
github.com/mihari-proxy/mihari/internal/runtime/a.go:1.1,2.1 0 0
`
	got, err := parseProfile(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Total.percent(); ok {
		t.Fatal("expected n/a percent for zero statements")
	}
}

func TestCompareDetectsDropsAndAllowsImprovements(t *testing.T) {
	base := report{
		Total: coverage{Covered: 80, Statements: 100},
		Packages: map[string]coverage{
			"github.com/mihari-proxy/mihari/internal/runtime": {Covered: 90, Statements: 100},
			"github.com/mihari-proxy/mihari/internal/web":     {Covered: 50, Statements: 100},
		},
	}
	// total drop 0.6pp (>0.5), runtime drop 1.5pp (>1.0), web improves, new critical package appears.
	head := report{
		Total: coverage{Covered: 794, Statements: 1000}, // 79.4%
		Packages: map[string]coverage{
			"github.com/mihari-proxy/mihari/internal/runtime": {Covered: 885, Statements: 1000}, // 88.5%
			"github.com/mihari-proxy/mihari/internal/web":     {Covered: 700, Statements: 1000}, // 70%
			"github.com/mihari-proxy/mihari/internal/state":   {Covered: 10, Statements: 10},
		},
	}
	res := compare(base, head, policy{TotalDrop: 0.5, CriticalDrop: 1.0, Critical: criticalPackages})
	if len(res.Violations) < 2 {
		t.Fatalf("violations=%v", res.Violations)
	}
	// Improvement for web should not violate.
	for _, v := range res.Violations {
		if strings.Contains(v, "internal/web") {
			t.Fatalf("unexpected web violation: %s", v)
		}
	}
}

func TestCompareMissingCriticalPackage(t *testing.T) {
	base := report{
		Total: coverage{Covered: 1, Statements: 1},
		Packages: map[string]coverage{
			"github.com/mihari-proxy/mihari/internal/runtime": {Covered: 1, Statements: 1},
		},
	}
	head := report{
		Total:    coverage{Covered: 1, Statements: 1},
		Packages: map[string]coverage{},
	}
	res := compare(base, head, policy{TotalDrop: 0.5, CriticalDrop: 1.0, Critical: []string{"github.com/mihari-proxy/mihari/internal/runtime"}})
	if len(res.Violations) != 1 || !strings.Contains(res.Violations[0], "missing in head") {
		t.Fatalf("violations=%v", res.Violations)
	}
}

func TestCompareWithinTolerancePasses(t *testing.T) {
	base := report{Total: coverage{Covered: 800, Statements: 1000}, Packages: map[string]coverage{
		"github.com/mihari-proxy/mihari/internal/runtime": {Covered: 900, Statements: 1000},
	}}
	head := report{Total: coverage{Covered: 796, Statements: 1000}, Packages: map[string]coverage{
		"github.com/mihari-proxy/mihari/internal/runtime": {Covered: 895, Statements: 1000},
	}}
	res := compare(base, head, policy{TotalDrop: 0.5, CriticalDrop: 1.0, Critical: []string{"github.com/mihari-proxy/mihari/internal/runtime"}})
	if len(res.Violations) != 0 {
		t.Fatalf("violations=%v", res.Violations)
	}
}
