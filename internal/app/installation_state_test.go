package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestInstallationState_StrictRoundTrip(t *testing.T) {
	state := testInstallationState(InstallationStateApplying)
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeInstallationState(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("DecodeInstallationState: %v", err)
	}
	if got.ID != state.ID || got.Target.DataIdentity == nil || got.Target.DataIdentity.Key != "data-key" {
		t.Fatalf("decoded state lost identity: %#v", got)
	}
	encoded, err := EncodeInstallationState(got)
	if err != nil {
		t.Fatalf("EncodeInstallationState: %v", err)
	}
	if !bytes.Equal(encoded, raw) {
		t.Fatalf("round trip changed document\ngot  %s\nwant %s", encoded, raw)
	}
}

func TestInstallationState_RejectsMalformedDocuments(t *testing.T) {
	valid := testInstallationState(InstallationStateApplying)
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"unknown field":     bytes.Replace(raw, []byte(`"schema":`), []byte(`"extra":true,"schema":`), 1),
		"wrong key case":    bytes.Replace(raw, []byte(`"schema":`), []byte(`"Schema":`), 1),
		"duplicate top key": bytes.Replace(raw, []byte(`"schema":`), []byte(`"schema":"mihari.install-state/v1","schema":`), 1),
		"duplicate nested":  bytes.Replace(raw, []byte(`"boot_id":"boot-a"`), []byte(`"boot_id":"boot-a","boot_id":"boot-b"`), 1),
		"trailing object":   append(append([]byte(nil), raw...), []byte(` {}`)...),
		"invalid utf8":      append(append([]byte(nil), raw...), 0xff),
		"oversized":         []byte(`{"schema":"` + strings.Repeat("a", MaxInstallationStateBytes) + `"}`),
	}
	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeInstallationState(bytes.NewReader(document)); err == nil {
				t.Fatal("DecodeInstallationState accepted malformed document")
			}
		})
	}
}

func TestInstallationState_RejectsNullOrMissingRequiredNestedFields(t *testing.T) {
	state := testInstallationState(InstallationStateApplying)
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"null schema":        bytes.Replace(raw, []byte(`"schema":"mihari.install-state/v1"`), []byte(`"schema":null`), 1),
		"null boolean":       bytes.Replace(raw, []byte(`"installed":true`), []byte(`"installed":null`), 1),
		"missing boolean":    bytes.Replace(raw, []byte(`"installed":true,`), nil, 1),
		"missing nested key": bytes.Replace(raw, []byte(`"key":"data-key",`), nil, 1),
	}
	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeInstallationState(bytes.NewReader(document)); err == nil {
				t.Fatal("DecodeInstallationState accepted null or missing required field")
			}
		})
	}
}

func TestInstallationState_RequiresEveryFieldIncludingNullableFields(t *testing.T) {
	state := testInstallationState(InstallationStateComplete)
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"owner", "base", "supersedes", "source_scope"} {
		t.Run(field, func(t *testing.T) {
			var object map[string]json.RawMessage
			if err := json.Unmarshal(raw, &object); err != nil {
				t.Fatal(err)
			}
			delete(object, field)
			missing, err := json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeInstallationState(bytes.NewReader(missing)); err == nil {
				t.Fatalf("DecodeInstallationState accepted missing %q", field)
			}
		})
	}
}

func TestInstallationState_ValidatesInstalledAndMissingRootRules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*InstallationState)
	}{
		{name: "installed root has no identity", mutate: func(s *InstallationState) { s.Target.DataIdentity = nil }},
		{name: "installed root has conflicting parent", mutate: func(s *InstallationState) { s.Target.DataParentIdentity = testDataParentIdentity() }},
		{name: "missing root has no parent evidence", mutate: func(s *InstallationState) { s.Target.DataIdentity = nil; s.Target.DataParentIdentity = nil }},
		{name: "missing root is complete", mutate: func(s *InstallationState) {
			s.Target.DataIdentity = nil
			s.Target.DataParentIdentity = testDataParentIdentity()
			s.State = InstallationStateComplete
			s.Owner = nil
		}},
		{name: "uninstalled target retains binary", mutate: func(s *InstallationState) { s.Target.Installed = false }},
		{name: "uninstalled target has partial scope", mutate: func(s *InstallationState) {
			s.Target.Installed = false
			s.Target.Binary = nil
			s.Target.DefinitionSHA256 = ""
			s.Target.InstallRoot = ""
		}},
		{name: "parent evidence points elsewhere", mutate: func(s *InstallationState) {
			s.Target.DataIdentity = nil
			s.Target.DataParentIdentity = testDataParentIdentity()
			s.Target.DataRoot = `C:\Mihari\other`
		}},
		{name: "relative endpoint", mutate: func(s *InstallationState) { s.Target.Endpoint = "mihari-control" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := testInstallationState(InstallationStateApplying)
			tc.mutate(&state)
			raw, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeInstallationState(bytes.NewReader(raw)); err == nil {
				t.Fatal("DecodeInstallationState accepted invalid root or install manifest")
			}
		})
	}
}

func TestInstallationState_AcceptsApplyingMissingRootOnlyWithoutPriorBase(t *testing.T) {
	for _, operation := range []string{InstallationOperationInstall, InstallationOperationRepair} {
		t.Run(operation, func(t *testing.T) {
			state := testInstallationState(InstallationStateApplying)
			state.Operation = operation
			state.Base = nil
			state.Target.DataIdentity = nil
			state.Target.DataParentIdentity = testDataParentIdentity()
			raw, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeInstallationState(bytes.NewReader(raw)); err != nil {
				t.Fatalf("DecodeInstallationState rejected first-install parent evidence: %v", err)
			}
		})
	}
}

func TestInstallationState_RejectsRepairDegradingVerifiedRootToParentEvidence(t *testing.T) {
	state := testInstallationState(InstallationStateApplying)
	state.Target.DataIdentity = nil
	state.Target.DataParentIdentity = testDataParentIdentity()
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeInstallationState(bytes.NewReader(raw)); err == nil {
		t.Fatal("DecodeInstallationState accepted parent evidence over an existing verified base")
	}
}

func TestInstallationState_AcceptsFirstInstallAfterEmptyCompleteRecord(t *testing.T) {
	state := testInstallationState(InstallationStateApplying)
	state.Operation = InstallationOperationInstall
	state.Base = &InstallationManifest{}
	state.Target.DataIdentity = nil
	state.Target.DataParentIdentity = testDataParentIdentity()
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeInstallationState(bytes.NewReader(raw)); err != nil {
		t.Fatalf("DecodeInstallationState rejected first install after empty complete record: %v", err)
	}
}

func TestInstallationState_RepeatRepairKeepsFlatBaseAndSourceScope(t *testing.T) {
	state := testInstallationState(InstallationStateApplying)
	state.Supersedes = &InstallationStateRef{ID: "fedcba9876543210fedcba9876543210", SHA256: strings.Repeat("c", 64)}
	state.SourceScope = &InstallationSourceScope{
		DataRoot:       `/legacy/data`,
		DataIdentity:   InstallationIdentity{BootID: "boot-a", Key: "source-key", Marker: "source-marker"},
		Selection:      "legacy_v1",
		EvidenceSHA256: strings.Repeat("d", 64),
	}
	raw, err := EncodeInstallationState(state)
	if err != nil {
		t.Fatalf("EncodeInstallationState: %v", err)
	}
	if len(raw) > MaxInstallationStateBytes {
		t.Fatalf("encoded state has %d bytes", len(raw))
	}
	got, err := DecodeInstallationState(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("DecodeInstallationState: %v", err)
	}
	if got.Base == nil || got.Base.DataRoot != state.Base.DataRoot || got.SourceScope == nil || got.SourceScope.DataRoot != `/legacy/data` || got.Target.DataRoot != state.Target.DataRoot {
		t.Fatalf("repeat repair lost flat base or instance scope: %+v", got)
	}
}

func TestInstallationState_RejectsInvalidLifecycleShape(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*InstallationState)
	}{
		{name: "applying without owner", mutate: func(s *InstallationState) { s.Owner = nil }},
		{name: "complete with owner", mutate: func(s *InstallationState) { s.State = InstallationStateComplete }},
		{name: "retain with entries", mutate: func(s *InstallationState) { s.ResetEntries = []string{"logs"} }},
		{name: "reset outside fresh", mutate: func(s *InstallationState) { s.DataPolicy = "reset"; s.ResetEntries = []string{"logs"} }},
		{name: "fresh with retain", mutate: func(s *InstallationState) { s.Operation = InstallationOperationFresh }},
		{name: "repair with uninstalled target", mutate: func(s *InstallationState) { s.Target = InstallationManifest{} }},
		{name: "uninstall with installed target", mutate: func(s *InstallationState) { s.Operation = InstallationOperationUninstall }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := testInstallationState(InstallationStateApplying)
			tc.mutate(&state)
			if _, err := EncodeInstallationState(state); err == nil {
				t.Fatal("EncodeInstallationState accepted invalid lifecycle")
			}
		})
	}
}

func TestInstallationState_AcceptsCompleteInitialUninstallAndFreshReset(t *testing.T) {
	t.Run("initial uninstall", func(t *testing.T) {
		state := InstallationState{
			Schema: InstallationStateSchema, ID: "0123456789abcdef0123456789abcdef",
			State: InstallationStateComplete, Operation: InstallationOperationUninstall,
			Target: InstallationManifest{}, DataPolicy: InstallationDataRetain, ResetEntries: []string{},
		}
		if _, err := EncodeInstallationState(state); err != nil {
			t.Fatalf("EncodeInstallationState rejected initial uninstall: %v", err)
		}
	})

	t.Run("fresh reset", func(t *testing.T) {
		state := testInstallationState(InstallationStateApplying)
		state.Operation = InstallationOperationFresh
		state.DataPolicy = InstallationDataReset
		state.ResetEntries = []string{"logs"}
		if _, err := EncodeInstallationState(state); err != nil {
			t.Fatalf("EncodeInstallationState rejected fresh reset: %v", err)
		}
	})
}

func TestInstallationState_InvalidErrorIsDataFailure(t *testing.T) {
	_, err := DecodeInstallationState(strings.NewReader(`{}`))
	var apiErr interface{ Error() string }
	if !errors.As(err, &apiErr) || err.Error() != "invalid installation state" {
		t.Fatalf("error=%v", err)
	}
}

func testInstallationState(stateKind string) InstallationState {
	state := InstallationState{
		Schema:       InstallationStateSchema,
		ID:           "0123456789abcdef0123456789abcdef",
		State:        stateKind,
		Operation:    "repair",
		Base:         testInstallationManifest(),
		Target:       *testInstallationManifest(),
		DataPolicy:   "retain",
		ResetEntries: []string{},
	}
	if stateKind == InstallationStateApplying {
		state.Owner = &InstallationOwner{BootID: "boot-a", PID: 42, Start: "100"}
	}
	return state
}

func testInstallationManifest() *InstallationManifest {
	return &InstallationManifest{
		Installed:        true,
		DataRoot:         `/var/lib/mihari/data`,
		InstallRoot:      `/usr/local/lib/mihari`,
		Endpoint:         `/var/lib/mihari/control.sock`,
		Credential:       `/var/lib/mihari/control.token`,
		DataIdentity:     &InstallationIdentity{BootID: "boot-a", Key: "data-key", Marker: "data-marker"},
		Binary:           &InstallationBinary{Path: `/usr/local/lib/mihari/mihari`, SHA256: strings.Repeat("a", 64), Version: "v1.0.0"},
		DefinitionSHA256: strings.Repeat("b", 64),
		Enabled:          true,
		RunAfterInstall:  true,
	}
}

func testDataParentIdentity() *InstallationDataParentIdentity {
	return &InstallationDataParentIdentity{
		Path:     `/var/lib/mihari`,
		Identity: InstallationIdentity{BootID: "boot-a", Key: "parent-key", Marker: "parent-marker"},
		Relative: "data",
	}
}
