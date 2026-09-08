package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestInstallationLegacy_FirstAdoptionUsesVerifiedFlatBase(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeRepair)
	legacy := *cloneInstallationManifestForTest(h.target)
	legacy.Binary.Version = "legacy"
	h.raw = nil
	h.legacy = &legacy
	h.input.Instance.RecordID = ""
	h.input.Instance.RecordSHA256 = ""
	plan, err := h.manager.Plan(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan}); err != nil {
		t.Fatal(err)
	}
	if h.archiveCalls != 0 || len(h.publishedStates) != 2 {
		t.Fatalf("legacy adoption archive=%d publications=%d", h.archiveCalls, len(h.publishedStates))
	}
	applying := h.publishedStates[0]
	if applying.Base == nil || applying.Base.Binary.Version != "legacy" || applying.Supersedes != nil {
		t.Fatalf("legacy applying did not use flat verified base: %+v", applying)
	}
}

func TestInstallationRepair_RepeatedApplyingPreservesSourceScope(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeRepair)
	scope := &InstallationSourceScope{
		DataRoot: "/legacy/data", DataIdentity: InstallationIdentity{BootID: "boot-a", Key: "source-key", Marker: "source-marker"},
		Selection: "legacy_v1", EvidenceSHA256: stringsRepeat("e", 64),
	}
	current, err := DecodeInstallationState(bytes.NewReader(h.raw))
	if err != nil {
		t.Fatal(err)
	}
	current.SourceScope = scope
	h.raw, err = EncodeInstallationState(current)
	if err != nil {
		t.Fatal(err)
	}
	h.input.Instance.RecordSHA256 = fmt.Sprintf("%x", sha256.Sum256(h.raw))
	h.input.Instance.SourceScope = scope
	plan, err := h.manager.Plan(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan}); err != nil {
		t.Fatal(err)
	}
	if got := h.publishedStates[0].SourceScope; got == nil || got.DataRoot != scope.DataRoot || got.EvidenceSHA256 != scope.EvidenceSHA256 {
		t.Fatalf("source scope lost: %+v", got)
	}
}

func TestInstallationLegacy_RejectsDifferentInstanceBeforeArchive(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeRepair)
	legacy := *cloneInstallationManifestForTest(h.target)
	legacy.DataRoot += "-other"
	h.raw = nil
	h.legacy = &legacy
	h.input.Instance.RecordID = ""
	h.input.Instance.RecordSHA256 = ""
	plan, err := h.manager.Plan(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan}); err == nil {
		t.Fatal("legacy manifest from another instance was accepted")
	}
	if h.archiveCalls != 0 || h.publishCalls != 0 || h.quiesceCalls != 0 {
		t.Fatalf("foreign legacy crossed commit boundary: archive=%d publish=%d quiesce=%d", h.archiveCalls, h.publishCalls, h.quiesceCalls)
	}
}
