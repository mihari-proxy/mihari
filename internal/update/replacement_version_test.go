package update

import (
	"encoding/json"
	"os"
	"testing"
)

func TestReplacementVersion_SharedMatrix(t *testing.T) {
	raw, err := os.ReadFile("../../scripts/install/testdata/replacement_versions.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Current, Target string
		Risk            ReplacementRisk
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("empty version fixture")
	}
	for _, tc := range cases {
		t.Run(tc.Current+"_to_"+tc.Target, func(t *testing.T) {
			if got := ClassifyReplacementVersion(tc.Current, tc.Target); got != tc.Risk {
				t.Fatalf("risk=%s want=%s", got, tc.Risk)
			}
		})
	}
}

func TestReplacementVersion_OverflowIsNotCanonical(t *testing.T) {
	for _, tag := range []string{"v999999999999999999999999.0.0", "v1.999999999999999999999999.0", "v1.0.999999999999999999999999", "v1.0.0-dev.999999999999999999999999"} {
		if _, ok := parseCanonicalTag(tag); ok {
			t.Errorf("overflow accepted: %s", tag)
		}
	}
}
