package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestInstallationPlan_BindsDeterministicDigest(t *testing.T) {
	input := testVerifiedInstallationPlanInput()
	plan, err := BindInstallationPlan(input)
	if err != nil {
		t.Fatalf("BindInstallationPlan: %v", err)
	}
	const wantDigest = "13d6af7eda6f31101f69b359e9e9fea861c2ed526ad192814e92f1d9015fc662"
	if plan.Schema != InstallationPlanSchema || plan.PlanSHA256 != wantDigest {
		t.Fatalf("schema=%q digest=%q want=%q", plan.Schema, plan.PlanSHA256, wantDigest)
	}
	if err := VerifyInstallationPlan(plan); err != nil {
		t.Fatalf("VerifyInstallationPlan: %v", err)
	}

	input.Preserve[0].Path = `/changed`
	if plan.Preserve[0].Path != `/var/lib/mihari/control.token` {
		t.Fatal("plan retained caller-owned slice")
	}
}

func TestInstallationPlan_DigestBindsEveryAuthorizedFact(t *testing.T) {
	base, err := BindInstallationPlan(testVerifiedInstallationPlanInput())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*VerifiedInstallationPlanInput)
	}{
		{name: "record id", mutate: func(i *VerifiedInstallationPlanInput) { i.Instance.RecordID = "fedcba9876543210fedcba9876543210" }},
		{name: "record hash", mutate: func(i *VerifiedInstallationPlanInput) { i.Instance.RecordSHA256 = strings.Repeat("f", 64) }},
		{name: "root identity", mutate: func(i *VerifiedInstallationPlanInput) { i.Instance.DataIdentity.Key = "other-key" }},
		{name: "candidate hash", mutate: func(i *VerifiedInstallationPlanInput) { i.CandidateSHA256 = strings.Repeat("e", 64) }},
		{name: "enabled policy", mutate: func(i *VerifiedInstallationPlanInput) { i.Instance.Enabled = false }},
		{name: "run policy", mutate: func(i *VerifiedInstallationPlanInput) { i.Instance.RunAfterInstall = false }},
		{name: "preserve list", mutate: func(i *VerifiedInstallationPlanInput) { i.Preserve[0].Category = "unknown" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := testVerifiedInstallationPlanInput()
			tc.mutate(&input)
			changed, err := BindInstallationPlan(input)
			if err != nil {
				t.Fatalf("BindInstallationPlan: %v", err)
			}
			if changed.PlanSHA256 == base.PlanSHA256 {
				t.Fatalf("digest did not bind %s", tc.name)
			}
			changed.PlanSHA256 = base.PlanSHA256
			if err := VerifyInstallationPlan(changed); err == nil {
				t.Fatalf("old confirmation remained valid after changing %s", tc.name)
			}
		})
	}

	t.Run("delete list", func(t *testing.T) {
		input := testVerifiedInstallationPlanInput()
		input.Mode = InstallationModeFresh
		input.Delete = []InstallationEntry{{Path: `/var/lib/mihari/logs`, Category: "logs"}}
		original, err := BindInstallationPlan(input)
		if err != nil {
			t.Fatal(err)
		}
		input.Delete[0] = InstallationEntry{Path: `/var/lib/mihari/web`, Category: "web"}
		changed, err := BindInstallationPlan(input)
		if err != nil {
			t.Fatal(err)
		}
		if changed.PlanSHA256 == original.PlanSHA256 {
			t.Fatal("digest did not bind delete list")
		}
		changed.PlanSHA256 = original.PlanSHA256
		if err := VerifyInstallationPlan(changed); err == nil {
			t.Fatal("old confirmation remained valid after changing delete list")
		}
	})
}

func TestInstallationPlan_StableDigestForSameVerifiedFacts(t *testing.T) {
	first, err := BindInstallationPlan(testVerifiedInstallationPlanInput())
	if err != nil {
		t.Fatal(err)
	}
	second, err := BindInstallationPlan(testVerifiedInstallationPlanInput())
	if err != nil {
		t.Fatal(err)
	}
	if first.PlanSHA256 != second.PlanSHA256 {
		t.Fatalf("stable facts produced different digests: %s != %s", first.PlanSHA256, second.PlanSHA256)
	}
}

func TestInstallationPlan_RejectsInvalidModeAndUnprovenRoot(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*VerifiedInstallationPlanInput)
	}{
		{name: "mode", mutate: func(i *VerifiedInstallationPlanInput) { i.Mode = "reinstall" }},
		{name: "no root identity", mutate: func(i *VerifiedInstallationPlanInput) { i.Instance.DataIdentity = nil }},
		{name: "conflicting root evidence", mutate: func(i *VerifiedInstallationPlanInput) { i.Instance.DataParentIdentity = testDataParentIdentity() }},
		{name: "candidate hash", mutate: func(i *VerifiedInstallationPlanInput) { i.CandidateSHA256 = "ABC" }},
		{name: "record pair", mutate: func(i *VerifiedInstallationPlanInput) { i.Instance.RecordSHA256 = "" }},
		{name: "repair delete", mutate: func(i *VerifiedInstallationPlanInput) {
			i.Delete = []InstallationEntry{{Path: `/var/lib/mihari/logs`, Category: "logs"}}
		}},
		{name: "unknown category", mutate: func(i *VerifiedInstallationPlanInput) { i.Preserve[0].Category = "other" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := testVerifiedInstallationPlanInput()
			tc.mutate(&input)
			if _, err := BindInstallationPlan(input); err == nil {
				t.Fatal("BindInstallationPlan accepted invalid verified input")
			}
		})
	}
}

func TestInstallationPlan_RejectsPlanLargerThanWireLimit(t *testing.T) {
	input := testVerifiedInstallationPlanInput()
	input.Preserve = make([]InstallationEntry, 20)
	for i := range input.Preserve {
		input.Preserve[i] = InstallationEntry{
			Path:     `/` + strings.Repeat("a", 4088) + string(rune('A'+i)),
			Category: "unknown",
		}
	}
	if _, err := BindInstallationPlan(input); err == nil {
		t.Fatal("BindInstallationPlan accepted plan above 64 KiB")
	}
}

func TestInstallationPlan_AcceptsAllShortPreservedEntriesWithinWireLimit(t *testing.T) {
	input := testVerifiedInstallationPlanInput()
	input.Preserve = make([]InstallationEntry, 65)
	for i := range input.Preserve {
		input.Preserve[i] = InstallationEntry{Path: fmt.Sprintf("/unknown/%02d", i), Category: InstallationCategoryUnknown}
	}
	plan, err := BindInstallationPlan(input)
	if err != nil {
		t.Fatalf("BindInstallationPlan rejected 65 short preserved entries: %v", err)
	}
	if len(plan.Preserve) != 65 {
		t.Fatalf("preserve count=%d", len(plan.Preserve))
	}
}

func TestInstallationPlan_RejectsFullWireAtDigestBoundary(t *testing.T) {
	input := testVerifiedInstallationPlanInput()
	input.Preserve = make([]InstallationEntry, 16)
	for i := 0; i < 15; i++ {
		input.Preserve[i] = InstallationEntry{
			Path:     `/` + strings.Repeat(string(rune('a'+i)), 4075) + fmt.Sprintf("/%02d", i),
			Category: "unknown",
		}
	}
	found := false
	for length := 1; length <= 4090; length++ {
		input.Preserve[15] = InstallationEntry{Path: `/z/` + strings.Repeat("q", length), Category: "unknown"}
		probe := InstallationPlan{
			Schema:          InstallationPlanSchema,
			Mode:            input.Mode,
			Instance:        input.Instance,
			CandidateSHA256: input.CandidateSHA256,
			Preserve:        input.Preserve,
			Delete:          input.Delete,
			PlanSHA256:      strings.Repeat("0", 64),
		}
		canonical, err := json.Marshal(installationPlanDigestDocument{
			Schema: probe.Schema, Mode: probe.Mode, Instance: probe.Instance,
			CandidateSHA256: probe.CandidateSHA256, Preserve: probe.Preserve, Delete: probe.Delete,
		})
		if err != nil {
			t.Fatal(err)
		}
		wire, err := json.Marshal(probe)
		if err != nil {
			t.Fatal(err)
		}
		if len(canonical) <= MaxInstallationPlanBytes && len(wire) > MaxInstallationPlanBytes {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("test fixture did not reach the full-wire-only boundary")
	}
	if _, err := BindInstallationPlan(input); err == nil {
		t.Fatal("BindInstallationPlan accepted a full plan above 64 KiB")
	}
}

func TestInstallationPlan_BindsMissingRootParentAndSourceScope(t *testing.T) {
	input := testVerifiedInstallationPlanInput()
	input.Instance.RecordID = ""
	input.Instance.RecordSHA256 = ""
	input.Instance.DataIdentity = nil
	input.Instance.DataParentIdentity = testDataParentIdentity()
	input.Instance.SourceScope = &InstallationSourceScope{
		DataRoot:       `/legacy`,
		DataIdentity:   InstallationIdentity{BootID: "boot-a", Key: "source-key", Marker: "source-marker"},
		Selection:      "legacy_v1",
		EvidenceSHA256: strings.Repeat("d", 64),
	}
	plan, err := BindInstallationPlan(input)
	if err != nil {
		t.Fatalf("BindInstallationPlan: %v", err)
	}
	if plan.Instance.DataParentIdentity == nil || plan.Instance.SourceScope == nil || plan.Instance.SourceScope.Selection != "legacy_v1" {
		t.Fatalf("plan lost instance scope: %+v", plan.Instance)
	}
}

func testVerifiedInstallationPlanInput() VerifiedInstallationPlanInput {
	return VerifiedInstallationPlanInput{
		Mode: InstallationModeRepair,
		Instance: InstallationPlanInstance{
			RecordID:        "0123456789abcdef0123456789abcdef",
			RecordSHA256:    strings.Repeat("c", 64),
			DataRoot:        `/var/lib/mihari/data`,
			DataIdentity:    &InstallationIdentity{BootID: "boot-a", Key: "data-key", Marker: "data-marker"},
			Enabled:         true,
			RunAfterInstall: true,
		},
		CandidateSHA256: strings.Repeat("a", 64),
		Preserve:        []InstallationEntry{{Path: `/var/lib/mihari/control.token`, Category: "credential"}},
		Delete:          []InstallationEntry{},
	}
}
